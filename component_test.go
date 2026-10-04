package main

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/hujun-open/dhcplt/internal/testutil"
	"github.com/hujun-open/etherconn"
	"github.com/hujun-open/myaddr"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/insomniacslk/dhcp/dhcpv6/nclient6"
)

var (
	componentClntMAC = net.HardwareAddr{0xaa, 0xbb, 0xcc, 0x11, 0x22, 0x33}
	componentSvrMAC  = net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x01}
	componentVLANs   = etherconn.VLANs{{ID: 100, EtherType: 0x8100}}
)

// buildStandaloneDClient wires a single clientConfig into a DClient, mirroring
// the relevant part of NewSched without invoking the real relay/client
// factories. It lets component tests drive the real dialv4/dialv6 code over a
// FakeRelay.
func buildStandaloneDClient(t *testing.T, cfg clientConfig) *DClient {
	t.Helper()
	dc := &DClient{cfg: &cfg, dialResultCh: make(chan *dialResult, 4)}

	var key etherconn.L2EndpointKey
	if cfg.v4econn != nil {
		key = cfg.v4econn.LocalAddr().GetKey()
		rudp, err := etherconn.NewRUDPConn(
			fmt.Sprintf("0.0.0.0:%d", dhcpv4.ClientPort), cfg.v4econn,
			etherconn.WithAcceptAny(true))
		if err != nil {
			t.Fatalf("failed to create client v4 RUDPConn: %v", err)
		}
		dc.d4, err = nclient4.NewWithConn(rudp, cfg.Mac,
			nclient4.WithRetry(int(cfg.setup.Retry)),
			nclient4.WithTimeout(cfg.setup.Timeout),
			nclient4.WithHWAddr(cfg.Mac))
		if err != nil {
			t.Fatalf("failed to create nclient4: %v", err)
		}
	}
	if cfg.v6econn != nil {
		key = cfg.v6econn.LocalAddr().GetKey()
		localaddr := fmt.Sprintf("[%v]:%d", myaddr.GetLLAFromMac(cfg.Mac), cfg.setup.SourceV6Port)
		rudp, err := etherconn.NewRUDPConn(localaddr, cfg.v6econn, etherconn.WithAcceptAny(true))
		if err != nil {
			t.Fatalf("failed to create client v6 RUDPConn: %v", err)
		}
		dc.d6, err = nclient6.NewWithConn(rudp, cfg.Mac)
		if err != nil {
			t.Fatalf("failed to create nclient6: %v", err)
		}
	}
	dc.id = getClientIDFromL2Key(key)
	return dc
}

func singleClientSetup(v4, v6 bool) *testSetup {
	setup := newDefaultConf()
	setup.Ifname = testutil.FakeRelayIfName
	setup.EnableV4 = v4
	setup.EnableV6 = v6
	setup.DORA.NumOfClients = 1
	setup.DORA.MacStep = 1
	setup.DORA.StartMAC = componentClntMAC
	setup.DORA.StartVLANs = componentVLANs
	setup.DORA.VLANStep = 0
	setup.Timeout = 2 * time.Second
	setup.Retry = 2
	return setup
}

func TestComponentDHCPv4DORA(t *testing.T) {
	relay := testutil.NewFakeRelay()
	defer relay.Stop()

	svr, err := testutil.StartDHCPv4Server(relay, componentSvrMAC, componentVLANs, net.IPv4(1, 1, 1, 1), nil)
	if err != nil {
		t.Fatalf("StartDHCPv4Server error: %v", err)
	}
	defer svr.Close()

	setup := singleClientSetup(true, false)
	setup.pktRelay = relay

	cfgs, err := genClientConfigurations(setup)
	if err != nil {
		t.Fatalf("genClientConfigurations error: %v", err)
	}
	if len(cfgs) != 1 {
		t.Fatalf("got %d client configs, want 1", len(cfgs))
	}

	dc := buildStandaloneDClient(t, cfgs[0])
	dc.dialAll(nil)

	res := <-dc.dialResultCh
	if res.ExecResult != resultSuccess {
		t.Fatalf("DORA result = %v, want success", res.ExecResult)
	}
	if dc.d4Lease == nil {
		t.Fatal("v4 lease is nil after successful DORA")
	}
	// the built-in server assigns the client MAC's last 4 bytes as the address:
	// aa:bb:cc:11:22:33 -> 204.17.34.51/24
	if got := dc.d4Lease.addrStr(); got != "204.17.34.51/24" {
		t.Errorf("assigned address = %q, want 204.17.34.51/24", got)
	}
}

func TestComponentDHCPv6DORA(t *testing.T) {
	relay := testutil.NewFakeRelay()
	defer relay.Stop()

	svrIP := myaddr.GetLLAFromMac(componentSvrMAC)
	// single client, so any reply can simply be addressed to the client MAC.
	resolve := func(net.IP) net.HardwareAddr { return componentClntMAC }
	svr, err := testutil.StartDHCPv6Server(relay, componentSvrMAC, componentVLANs, svrIP, resolve, nil)
	if err != nil {
		t.Fatalf("StartDHCPv6Server error: %v", err)
	}
	defer svr.Close()

	setup := singleClientSetup(false, true)
	setup.V6MsgType = dhcpv6.MessageTypeSolicit
	setup.DORA.NeedNA = true
	setup.DORA.NeedPD = true
	setup.SourceV6Port = dhcpv6.DefaultClientPort
	setup.pktRelay = relay

	cfgs, err := genClientConfigurations(setup)
	if err != nil {
		t.Fatalf("genClientConfigurations error: %v", err)
	}
	if len(cfgs) != 1 {
		t.Fatalf("got %d client configs, want 1", len(cfgs))
	}

	dc := buildStandaloneDClient(t, cfgs[0])
	dc.dialAll(nil)

	res := <-dc.dialResultCh
	if res.ExecResult != resultSuccess {
		t.Fatalf("DORA result = %v, want success", res.ExecResult)
	}
	if dc.d6Lease == nil {
		t.Fatal("v6 lease is nil after successful DORA")
	}
	if len(dc.d6Lease.addrStr()) == 0 {
		t.Error("v6 lease has no assigned address/prefix")
	}
	if dc.d6Lease.ReplyOptions.GetOne(dhcpv6.OptionIANA) == nil {
		t.Error("v6 lease is missing IA_NA")
	}
	if dc.d6Lease.ReplyOptions.GetOne(dhcpv6.OptionIAPD) == nil {
		t.Error("v6 lease is missing IA_PD")
	}
}
