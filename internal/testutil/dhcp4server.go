package testutil

import (
	"fmt"
	"net"

	"github.com/hujun-open/etherconn"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/server4"
)

// EncodedMACPrefix is prepended to the assigned IPv4 address bytes to build the
// client MAC by MACFromAssignedIP. The default DHCPv4 handler assigns an address
// whose 4 bytes are the client MAC's last 4 bytes, so this prefix must match the
// first two bytes of the client MAC used in tests (aa:bb:...).
var EncodedMACPrefix = []byte{0xaa, 0xbb}

// MACFromAssignedIP reverses the address assignment done by
// EncodedDHCPv4Handler: it maps an assigned address back to the client MAC so a
// server can address its reply to the right client without ARP.
func MACFromAssignedIP(ip net.IP) net.HardwareAddr {
	v4 := ip.To4()
	if v4 == nil {
		return nil
	}
	mac := make(net.HardwareAddr, 0, len(EncodedMACPrefix)+4)
	mac = append(mac, EncodedMACPrefix...)
	mac = append(mac, v4...)
	return mac
}

// EncodedDHCPv4Handler returns a server4.Handler that hands out
// 192.0.2.0/24-style addresses encoded from the client MAC (the last 4 bytes),
// mirroring the manual test server in testsvr. serverIP is used as the DHCP
// server identifier.
func EncodedDHCPv4Handler(serverIP net.IP) server4.Handler {
	return func(conn net.PacketConn, _ net.Addr, m *dhcpv4.DHCPv4) {
		if len(m.ClientHWAddr) < 6 {
			return
		}
		assignedIP := net.IP(append([]byte{}, m.ClientHWAddr[2:6]...))
		mods := []dhcpv4.Modifier{
			dhcpv4.WithReply(m),
			dhcpv4.WithYourIP(assignedIP),
			dhcpv4.WithNetmask(net.IPv4Mask(255, 255, 255, 0)),
			dhcpv4.WithLeaseTime(1000),
			dhcpv4.WithServerIP(serverIP),
		}
		switch m.MessageType() {
		case dhcpv4.MessageTypeDiscover:
			mods = append(mods, dhcpv4.WithMessageType(dhcpv4.MessageTypeOffer))
		case dhcpv4.MessageTypeRequest:
			mods = append(mods, dhcpv4.WithMessageType(dhcpv4.MessageTypeAck))
		default:
			return
		}
		resp, err := dhcpv4.New(mods...)
		if err != nil {
			return
		}
		peerAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%v:%v", assignedIP, dhcpv4.ClientPort))
		if err != nil {
			return
		}
		_, _ = conn.WriteTo(resp.ToBytes(), peerAddr)
	}
}

// DHCPv4Server is an in-process DHCPv4 server bound to a FakeRelay. It uses the
// real etherconn stack (EtherConn + RUDPConn) plus insomniacslk's server4, so it
// exercises the same code path as a real server minus the wire.
type DHCPv4Server struct {
	EtherConn *etherconn.EtherConn
	Conn      *etherconn.RUDPConn
	Server    *server4.Server
}

// StartDHCPv4Server starts a DHCPv4 server on relay using serverMAC and vlans.
// If handler is nil, EncodedDHCPv4Handler(serverIP) is used.
func StartDHCPv4Server(relay etherconn.PacketRelay, serverMAC net.HardwareAddr,
	vlans etherconn.VLANs, serverIP net.IP, handler server4.Handler) (*DHCPv4Server, error) {
	if handler == nil {
		handler = EncodedDHCPv4Handler(serverIP)
	}
	econn := etherconn.NewEtherConn(serverMAC, relay,
		etherconn.WithVLANs(vlans),
		etherconn.WithEtherTypes([]uint16{etherTypeIPv4}),
		etherconn.WithRecvMulticast(true))
	conn, err := etherconn.NewRUDPConn(
		fmt.Sprintf("%v:%d", serverIP, dhcpv4.ServerPort), econn,
		etherconn.WithAcceptAny(true),
		etherconn.WithResolveNextHopMacFunc(MACFromAssignedIP))
	if err != nil {
		return nil, fmt.Errorf("failed to create server RUDPConn: %w", err)
	}
	srv, err := server4.NewServer(FakeRelayIfName,
		&net.UDPAddr{IP: serverIP, Port: dhcpv4.ServerPort}, handler,
		server4.WithConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create DHCPv4 server: %w", err)
	}
	go func() { _ = srv.Serve() }()
	return &DHCPv4Server{EtherConn: econn, Conn: conn, Server: srv}, nil
}

// Close stops the server and releases its relay registration.
func (s *DHCPv4Server) Close() error {
	return s.Conn.Close()
}
