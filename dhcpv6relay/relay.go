// relay
package dhcpv6relay

import (
	"context"
	"net"
	"time"

	"github.com/hujun-open/dhcplt/common"

	"github.com/hujun-open/dhcplt/conpair"

	"github.com/hujun-open/etherconn"
	"github.com/insomniacslk/dhcp/dhcpv6"
)

type DHCPConn interface {
	// net.PacketConn methods
	ReadFrom(p []byte) (n int, addr net.Addr, err error)
	WriteTo(p []byte, addr net.Addr) (n int, err error)
	Close() error
	LocalAddr() net.Addr
	SetDeadline(t time.Time) error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	// new methods
	ReadFromIP(p []byte) (n int, addr net.IP, err error)
}

type UDPDHCPConn net.UDPConn

func (udpc *UDPDHCPConn) ReadFromIP(p []byte) (int, net.IP, error) {
	n, udpaddr, err := (*net.UDPConn)(udpc).ReadFromUDP(p)
	if udpaddr != nil {
		return n, udpaddr.IP, err
	}
	return n, nil, err
}

type RUDPDHCPConn struct {
	*etherconn.RUDPConn
}

func (rudpc *RUDPDHCPConn) ReadFromIP(p []byte) (int, net.IP, error) {
	n, udpaddr, err := rudpc.RUDPConn.ReadFrom(p)
	if udpaddr != nil {
		return n, udpaddr.(*net.UDPAddr).IP, err
	}
	return n, nil, err
}

type PairDHCPConn struct {
	*conpair.PacketConnPair
}

func (pc *PairDHCPConn) ReadFromIP(p []byte) (int, net.IP, error) {
	n, _, err := pc.PacketConnPair.ReadFrom(p)
	return n, net.ParseIP("::"), err
}

type RelayAgent struct {
	accessConn, networkConn DHCPConn
	accessZone              string
	options                 dhcpv6.Options
	linkAddr                net.IP
	peerAddr                net.IP
	svrAdddr                *net.UDPAddr
}

func NewRelayAgent(ctx context.Context, access, network DHCPConn, options ...Modifier) *RelayAgent {
	r := new(RelayAgent)
	r.accessConn = access
	r.networkConn = network
	r.svrAdddr = &net.UDPAddr{
		IP:   net.ParseIP("ff02::1:2"), //site-scope ALL_DHCP_Servers
		Port: dhcpv6.DefaultServerPort,
	}
	for _, o := range options {
		o(r)
	}
	go r.recvAccess(ctx)
	go r.recvNetwork(ctx)
	return r
}

type Modifier func(*RelayAgent)

func WithSvrAddr(addr *net.UDPAddr) Modifier {
	return func(relay *RelayAgent) {
		relay.svrAdddr = addr
	}
}
func WithLinkAddr(addr net.IP) Modifier {
	return func(relay *RelayAgent) {
		relay.linkAddr = addr
	}
}
func WithPeerAddr(addr net.IP) Modifier {
	return func(relay *RelayAgent) {
		relay.peerAddr = addr
	}
}

func WithOptions(opts dhcpv6.Options) Modifier {
	return func(relay *RelayAgent) {
		relay.options = opts
	}
}

const (
	maxDHCPv6Size = 1500
)

func (relay *RelayAgent) recvAccess(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		buf := make([]byte, maxDHCPv6Size)
		n, peerAddr, err := relay.accessConn.ReadFromIP(buf)
		if err != nil {
			if !err.(net.Error).Timeout() {
				common.MyLog("failed to receive from access, %v", err)
				return
			}
		}
		msg, err := dhcpv6.MessageFromBytes(buf[:n])
		if err != nil {
			common.MyLog("recvd an invalid DHCPv6 msg from access with src addr %v", peerAddr)
			continue
		}
		duid := msg.Options.ClientID()
		if duid == nil {
			common.MyLog("recvd a DHCPv6 msg without client-id from access with src addr %v", peerAddr)
			continue
		}
		usePeerAddr := peerAddr
		if usePeerAddr == nil || usePeerAddr.IsUnspecified() || usePeerAddr.Equal(net.ParseIP("::")) {
			usePeerAddr = relay.peerAddr
		}
		relayfwd, err := dhcpv6.EncapsulateRelay(msg, dhcpv6.MessageTypeRelayForward,
			relay.linkAddr, usePeerAddr)
		if err != nil {
			common.MyLog("failed to create relay-fwd msg, %v", err)
			continue
		}
		for _, o := range relay.options {
			relayfwd.AddOption(o)
		}
		common.MyLog("sending relay-fwd,%v", relayfwd.Summary())
		_, err = relay.networkConn.WriteTo(relayfwd.ToBytes(), relay.svrAdddr)
		if err != nil {
			common.MyLog("failed to send relay-fwd msg, abort, %v", err)
			return
		}
	}
}

func (relay *RelayAgent) recvNetwork(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		buf := make([]byte, maxDHCPv6Size)
		n, peerAddr, err := relay.networkConn.ReadFromIP(buf)
		if err != nil {
			if !err.(net.Error).Timeout() {
				common.MyLog("failed to receive from access, %v", err)
				return
			}
		}
		msg, err := dhcpv6.RelayMessageFromBytes(buf[:n])
		if err != nil {
			common.MyLog("recvd an invalid DHCPv6 relay msg from svr %v", peerAddr)
			continue
		}
		if msg.MessageType != dhcpv6.MessageTypeRelayReply {
			common.MyLog("drop an %v msg from svr %v", msg.MessageType)
			continue
		}
		common.MyLog("got a relay-reply %v", msg.Summary())
		_, err = relay.accessConn.WriteTo(msg.Options.RelayMessage().ToBytes(),
			&net.UDPAddr{
				IP:   msg.PeerAddr,
				Port: dhcpv6.DefaultClientPort,
				Zone: relay.accessZone,
			})
		if err != nil {
			common.MyLog("failed to send relay-fwd msg, abort, %v", err)
			return
		}
	}
}
