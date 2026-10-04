//go:build e2e && linux

// Package-level e2e tests. They are build-tagged so the normal `go test ./...`
// stays root-free and fast. Run them with:
//
//	sudo go test -tags e2e -run '^TestE2E' -v .
//
// TestMain re-executes the test binary inside a freshly created network
// namespace, so the veth pair, addresses and IPv6 setup never leak to the host.
package main

import (
	"context"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/hujun-open/dhcplt/internal/testutil"
	"github.com/hujun-open/etherconn"
	"github.com/hujun-open/myaddr"
	"github.com/vishvananda/netlink"
)

const e2eNetnsEnv = "DHCPLT_E2E_NETNS"

func TestMain(m *testing.M) {
	if os.Getenv(e2eNetnsEnv) != "" {
		os.Exit(m.Run())
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "e2e: skipping, root privileges are required")
		os.Exit(0)
	}
	if _, err := exec.LookPath("ip"); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: skipping, %v\n", err)
		os.Exit(0)
	}

	ns := fmt.Sprintf("dhcplt-e2e-%d", os.Getpid())
	if out, err := exec.Command("ip", "netns", "add", ns).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: failed to create netns: %v: %s\n", err, out)
		os.Exit(1)
	}

	cmd := exec.Command("ip", "netns", "exec", ns, os.Args[0], "-test.run", "^TestE2E", "-test.v")
	cmd.Env = append(os.Environ(), e2eNetnsEnv+"="+ns)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	runErr := cmd.Run()

	_ = exec.Command("ip", "netns", "del", ns).Run()

	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		os.Exit(1)
	}
	os.Exit(0)
}

// requireNetns skips the test unless it is running inside the dedicated e2e
// network namespace created by TestMain.
func requireNetns(t *testing.T) {
	t.Helper()
	if os.Getenv(e2eNetnsEnv) == "" {
		t.Skip("e2e test must run inside the dedicated network namespace; use -tags e2e")
	}
}

// createE2EVeth creates a veth pair "S" <-> "C" with VLAN offloading disabled,
// mirroring a real access link.
func createE2EVeth(t *testing.T) {
	t.Helper()
	la := new(netlink.Veth)
	la.Name = "S"
	la.PeerName = "C"
	_ = netlink.LinkDel(la)
	if err := netlink.LinkAdd(la); err != nil {
		t.Fatalf("LinkAdd veth: %v", err)
	}
	t.Cleanup(func() { _ = netlink.LinkDel(la) })

	lb, err := netlink.LinkByName("C")
	if err != nil {
		t.Fatalf("LinkByName C: %v", err)
	}
	if err := netlink.LinkSetUp(la); err != nil {
		t.Fatalf("LinkSetUp S: %v", err)
	}
	if err := netlink.LinkSetUp(lb); err != nil {
		t.Fatalf("LinkSetUp C: %v", err)
	}
	if err := etherconn.SetIfVLANOffloading("S", false); err != nil {
		t.Fatalf("disable S vlan offloading: %v", err)
	}
	if err := etherconn.SetIfVLANOffloading("C", false); err != nil {
		t.Fatalf("disable C vlan offloading: %v", err)
	}
	// let the links reach oper-up before sending frames
	time.Sleep(2 * time.Second)
}

func runE2ESched(t *testing.T, setup *testSetup, action actionType) *Sched {
	t.Helper()
	if err := setup.init(action); err != nil {
		t.Fatalf("setup.init: %v", err)
	}
	t.Cleanup(func() {
		if setup.pktRelay != nil {
			setup.pktRelay.Stop()
		}
	})
	sch, err := NewSched(setup, action)
	if err != nil {
		t.Fatalf("NewSched: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	wg := new(sync.WaitGroup)
	wg.Add(1)
	sch.run(ctx, wg, action)
	return sch
}

// clientMACs expands startMAC into n consecutive MACs, matching
// genClientConfigurations.
func clientMACs(t *testing.T, start net.HardwareAddr, n int, step uint) []net.HardwareAddr {
	t.Helper()
	cur := start
	result := make([]net.HardwareAddr, 0, n)
	for i := 0; i < n; i++ {
		if i > 0 {
			var err error
			cur, err = myaddr.IncMACAddr(cur, big.NewInt(int64(step)))
			if err != nil {
				t.Fatalf("IncMACAddr: %v", err)
			}
		}
		result = append(result, cur)
	}
	return result
}

func TestE2EDHCPv4OverVeth(t *testing.T) {
	requireNetns(t)
	createE2EVeth(t)

	relayS, err := etherconn.NewRawSocketRelay(context.Background(), "S",
		etherconn.WithDefaultReceival(false),
		etherconn.WithSendChanDepth(10240))
	if err != nil {
		t.Fatalf("create server relay: %v", err)
	}
	defer relayS.Stop()

	setup := loadTestSetupFromFile(t, "testdata/e2e/dora_v4.yaml")
	svr, err := testutil.StartDHCPv4Server(relayS,
		net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x01},
		setup.DORA.StartVLANs,
		net.IPv4(1, 1, 1, 1), nil)
	if err != nil {
		t.Fatalf("StartDHCPv4Server: %v", err)
	}
	defer svr.Close()

	sch := runE2ESched(t, setup, actionDORA)

	if sch.summary.Failed != 0 {
		t.Errorf("failed transactions = %d, want 0", sch.summary.Failed)
	}
	if sch.summary.Success != int(setup.DORA.NumOfClients) {
		t.Errorf("successful DORA = %d, want %d", sch.summary.Success, setup.DORA.NumOfClients)
	}
}

func TestE2EDHCPv6OverVeth(t *testing.T) {
	requireNetns(t)
	createE2EVeth(t)

	setup := loadTestSetupFromFile(t, "testdata/e2e/dora_v6.yaml")
	macs := clientMACs(t, setup.DORA.StartMAC, int(setup.DORA.NumOfClients), setup.DORA.MacStep)

	// the server addresses each reply to the client's IPv6 link-local address,
	// so map those back to the client MACs.
	llaToMAC := map[string]net.HardwareAddr{}
	for _, mac := range macs {
		llaToMAC[myaddr.GetLLAFromMac(mac).String()] = mac
	}
	resolve := func(ip net.IP) net.HardwareAddr {
		if mac, ok := llaToMAC[ip.String()]; ok {
			return mac
		}
		return etherconn.BroadCastMAC
	}

	relayS, err := etherconn.NewRawSocketRelay(context.Background(), "S",
		etherconn.WithDefaultReceival(false),
		etherconn.WithSendChanDepth(10240))
	if err != nil {
		t.Fatalf("create server relay: %v", err)
	}
	defer relayS.Stop()

	svrMAC := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x01}
	svr, err := testutil.StartDHCPv6Server(relayS, svrMAC,
		setup.DORA.StartVLANs, myaddr.GetLLAFromMac(svrMAC), resolve, nil)
	if err != nil {
		t.Fatalf("StartDHCPv6Server: %v", err)
	}
	defer svr.Close()

	sch := runE2ESched(t, setup, actionDORA)

	if sch.summary.Failed != 0 {
		t.Errorf("failed transactions = %d, want 0", sch.summary.Failed)
	}
	if sch.summary.Success != int(setup.DORA.NumOfClients) {
		t.Errorf("successful DORA = %d, want %d", sch.summary.Success, setup.DORA.NumOfClients)
	}
}
