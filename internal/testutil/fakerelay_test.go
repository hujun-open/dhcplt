package testutil

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hujun-open/etherconn"
)

func newTestUDPConn(t *testing.T, relay *FakeRelay, mac net.HardwareAddr, etype uint16, local string) *etherconn.RUDPConn {
	t.Helper()
	econn := etherconn.NewEtherConn(mac, relay, etherconn.WithEtherTypes([]uint16{etype}))
	conn, err := etherconn.NewRUDPConn(local, econn, etherconn.WithAcceptAny(true))
	if err != nil {
		t.Fatalf("NewRUDPConn(%s) error: %v", local, err)
	}
	return conn
}

func readUDP(t *testing.T, conn *etherconn.RUDPConn) (string, *net.UDPAddr) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline error: %v", err)
	}
	buf := make([]byte, 256)
	n, addr, err := conn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom error: %v", err)
	}
	ua, ok := addr.(*net.UDPAddr)
	if !ok {
		t.Fatalf("remote addr type = %T, want *net.UDPAddr", addr)
	}
	return string(buf[:n]), ua
}

func TestFakeRelayForwardsUDPv4(t *testing.T) {
	relay := NewFakeRelay()
	defer relay.Stop()

	connA := newTestUDPConn(t, relay, net.HardwareAddr{0x02, 0, 0, 0, 0, 0x01}, etherTypeIPv4, "10.0.0.1:1000")
	connB := newTestUDPConn(t, relay, net.HardwareAddr{0x02, 0, 0, 0, 0, 0x02}, etherTypeIPv4, "10.0.0.2:2000")

	// default resolve returns broadcast, so this exercises multicast fan-out.
	if _, err := connA.WriteTo([]byte("hello-v4"), &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 2000}); err != nil {
		t.Fatalf("WriteTo error: %v", err)
	}
	got, addr := readUDP(t, connB)
	if got != "hello-v4" {
		t.Errorf("payload = %q, want %q", got, "hello-v4")
	}
	if addr.IP.String() != "10.0.0.1" || addr.Port != 1000 {
		t.Errorf("remote addr = %v, want 10.0.0.1:1000", addr)
	}
}

func TestFakeRelayForwardsUDPv6(t *testing.T) {
	relay := NewFakeRelay()
	defer relay.Stop()

	connA := newTestUDPConn(t, relay, net.HardwareAddr{0x02, 0, 0, 0, 0, 0x01}, etherTypeIPv6, "[2001:db8::1]:1000")
	connB := newTestUDPConn(t, relay, net.HardwareAddr{0x02, 0, 0, 0, 0, 0x02}, etherTypeIPv6, "[2001:db8::2]:2000")

	if _, err := connA.WriteTo([]byte("hello-v6"), &net.UDPAddr{IP: net.ParseIP("2001:db8::2"), Port: 2000}); err != nil {
		t.Fatalf("WriteTo error: %v", err)
	}
	got, addr := readUDP(t, connB)
	if got != "hello-v6" {
		t.Errorf("payload = %q, want %q", got, "hello-v6")
	}
	if addr.IP.String() != "2001:db8::1" || addr.Port != 1000 {
		t.Errorf("remote addr = %v, want [2001:db8::1]:1000", addr)
	}
}

func TestFakeRelayUnicastDelivery(t *testing.T) {
	relay := NewFakeRelay()
	defer relay.Stop()

	macB := net.HardwareAddr{0x02, 0, 0, 0, 0, 0x02}
	macC := net.HardwareAddr{0x02, 0, 0, 0, 0, 0x03}

	econnA := etherconn.NewEtherConn(net.HardwareAddr{0x02, 0, 0, 0, 0, 0x01}, relay,
		etherconn.WithEtherTypes([]uint16{etherTypeIPv4}))
	// resolve to B's MAC so the frame is unicast.
	connA, err := etherconn.NewRUDPConn("10.0.0.1:1000", econnA,
		etherconn.WithAcceptAny(true),
		etherconn.WithResolveNextHopMacFunc(func(net.IP) net.HardwareAddr { return macB }))
	if err != nil {
		t.Fatalf("NewRUDPConn error: %v", err)
	}
	connB := newTestUDPConn(t, relay, macB, etherTypeIPv4, "10.0.0.2:2000")
	connC := newTestUDPConn(t, relay, macC, etherTypeIPv4, "10.0.0.3:3000")

	if _, err := connA.WriteTo([]byte("unicast"), &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 2000}); err != nil {
		t.Fatalf("WriteTo error: %v", err)
	}
	if got, _ := readUDP(t, connB); got != "unicast" {
		t.Errorf("B payload = %q, want %q", got, "unicast")
	}

	// C must not have received anything.
	if err := connC.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline error: %v", err)
	}
	if _, _, err := connC.ReadFrom(make([]byte, 256)); err == nil {
		t.Error("C received a unicast frame destined to B")
	}
}

func TestFakeRelayStats(t *testing.T) {
	relay := NewFakeRelay()
	defer relay.Stop()

	connA := newTestUDPConn(t, relay, net.HardwareAddr{0x02, 0, 0, 0, 0, 0x01}, etherTypeIPv4, "10.0.0.1:1000")
	connB := newTestUDPConn(t, relay, net.HardwareAddr{0x02, 0, 0, 0, 0, 0x02}, etherTypeIPv4, "10.0.0.2:2000")

	if _, err := connA.WriteTo([]byte("x"), &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 2000}); err != nil {
		t.Fatalf("WriteTo error: %v", err)
	}
	readUDP(t, connB)

	if tx := atomic.LoadUint64(relay.GetStats().Tx); tx == 0 {
		t.Error("stats.Tx = 0, want > 0")
	}
	if rx := atomic.LoadUint64(relay.GetStats().Rx); rx == 0 {
		t.Error("stats.Rx = 0, want > 0")
	}
}
