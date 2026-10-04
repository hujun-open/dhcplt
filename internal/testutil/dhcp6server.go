package testutil

import (
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"github.com/hujun-open/etherconn"
	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/insomniacslk/dhcp/dhcpv6/server6"
	"github.com/insomniacslk/dhcp/iana"
)

// Default IPv6 resources handed out by the built-in DHCPv6 handler.
var (
	DefaultV6NAAddr   = net.ParseIP("2001:db8:1::1000")
	DefaultV6PDPrefix = &net.IPNet{IP: net.ParseIP("3000:1::"), Mask: net.CIDRMask(64, 128)}
)

// EncodedDHCPv6Handler returns a server6.Handler that answers SOLICIT with
// ADVERTISE and REQUEST/RENEW/REBIND with REPLY, always including an IA_NA and
// an IA_PD so that clients requesting either are satisfied.
func EncodedDHCPv6Handler(serverMAC net.HardwareAddr) server6.Handler {
	serverDUID := &dhcpv6.DUIDLLT{
		HWType:        iana.HWTypeEthernet,
		LinkLayerAddr: serverMAC,
	}
	var pdIAID [4]byte
	binary.BigEndian.PutUint32(pdIAID[:], 1)

	return func(conn net.PacketConn, peer net.Addr, m dhcpv6.DHCPv6) {
		req, ok := m.(*dhcpv6.Message)
		if !ok {
			return
		}
		var respType dhcpv6.MessageType
		switch req.MessageType {
		case dhcpv6.MessageTypeSolicit:
			respType = dhcpv6.MessageTypeAdvertise
		case dhcpv6.MessageTypeRequest, dhcpv6.MessageTypeRenew, dhcpv6.MessageTypeRebind:
			respType = dhcpv6.MessageTypeReply
		default:
			return
		}
		resp, err := dhcpv6.NewMessage()
		if err != nil {
			return
		}
		resp.MessageType = respType
		resp.TransactionID = req.TransactionID
		if cid := req.Options.ClientID(); cid != nil {
			resp.AddOption(dhcpv6.OptClientID(cid))
		}
		resp.AddOption(dhcpv6.OptServerID(serverDUID))
		resp.AddOption(dhcpv6.OptElapsedTime(0))
		dhcpv6.WithIANA(dhcpv6.OptIAAddress{
			IPv6Addr:          DefaultV6NAAddr,
			PreferredLifetime: time.Hour,
			ValidLifetime:     time.Hour,
		})(resp)
		dhcpv6.WithIAPD(pdIAID, &dhcpv6.OptIAPrefix{
			Prefix:            DefaultV6PDPrefix,
			PreferredLifetime: time.Hour,
			ValidLifetime:     time.Hour,
		})(resp)
		_, _ = conn.WriteTo(resp.ToBytes(), peer)
	}
}

// DHCPv6Server is an in-process DHCPv6 server bound to a FakeRelay.
type DHCPv6Server struct {
	EtherConn *etherconn.EtherConn
	Conn      *etherconn.RUDPConn
	Server    *server6.Server
}

// StartDHCPv6Server starts a DHCPv6 server on relay using serverMAC and vlans.
// resolve maps a reply's destination IP back to a destination MAC; for a
// single-client test it can simply return the client's MAC. If handler is nil,
// EncodedDHCPv6Handler(serverMAC) is used.
func StartDHCPv6Server(relay etherconn.PacketRelay, serverMAC net.HardwareAddr,
	vlans etherconn.VLANs, serverIP net.IP,
	resolve func(net.IP) net.HardwareAddr, handler server6.Handler) (*DHCPv6Server, error) {
	if handler == nil {
		handler = EncodedDHCPv6Handler(serverMAC)
	}
	if resolve == nil {
		resolve = func(net.IP) net.HardwareAddr { return serverMAC }
	}
	econn := etherconn.NewEtherConn(serverMAC, relay,
		etherconn.WithVLANs(vlans),
		etherconn.WithEtherTypes([]uint16{etherTypeIPv6}),
		etherconn.WithRecvMulticast(true))
	conn, err := etherconn.NewRUDPConn(
		fmt.Sprintf("[%v]:%d", serverIP, dhcpv6.DefaultServerPort), econn,
		etherconn.WithAcceptAny(true),
		etherconn.WithResolveNextHopMacFunc(resolve))
	if err != nil {
		return nil, fmt.Errorf("failed to create server RUDPConn: %w", err)
	}
	srv, err := server6.NewServer(FakeRelayIfName,
		&net.UDPAddr{IP: serverIP, Port: dhcpv6.DefaultServerPort}, handler,
		server6.WithConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create DHCPv6 server: %w", err)
	}
	go func() { _ = srv.Serve() }()
	return &DHCPv6Server{EtherConn: econn, Conn: conn, Server: srv}, nil
}

// Close stops the server and releases its relay registration.
func (s *DHCPv6Server) Close() error {
	return s.Conn.Close()
}
