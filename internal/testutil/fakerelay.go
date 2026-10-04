// Package testutil provides in-memory test doubles for dhcplt's component
// tests. A FakeRelay is an etherconn.PacketRelay that forwards Ethernet frames
// between registered EtherConns without touching a real interface or requiring
// root, so the real etherconn/nclient4/nclient6 stack can be exercised
// in-process.
package testutil

import (
	"encoding/binary"
	"net"
	"sync"
	"sync/atomic"

	"github.com/hujun-open/etherconn"
)

// FakeRelayIfName is the interface name reported by a FakeRelay.
const FakeRelayIfName = "fakerelay0"

const (
	etherTypeIPv4 = 0x0800
	etherTypeIPv6 = 0x86dd

	recvChanDepth = 1024
	sendChanDepth = 1024
)

type fakeRegistration struct {
	keys   []etherconn.L2EndpointKey
	recv   chan *etherconn.RelayReceival
	send   chan []byte
	stop   chan struct{}
	vlans  []uint16
	etypes []uint16

	closeOnce sync.Once
}

func (reg *fakeRegistration) stopNow() {
	reg.closeOnce.Do(func() { close(reg.stop) })
}

// matches reports whether a frame with the given VLANs and inner EtherType could
// be destined to this registration (used for broadcast/multicast fan-out).
func (reg *fakeRegistration) matches(vlans []uint16, etype uint16) bool {
	if !equalUint16(reg.vlans, vlans) {
		return false
	}
	for _, et := range reg.etypes {
		if et == etype {
			return true
		}
	}
	return false
}

// FakeRelay is an in-memory etherconn.PacketRelay. Frames written by one
// registered EtherConn are parsed and delivered to the registration whose
// L2EndpointKey matches the frame's destination MAC/VLANs/EtherType; frames
// sent to a broadcast/multicast MAC are fanned out to all matching
// registrations.
type FakeRelay struct {
	mu        sync.RWMutex
	byKey     map[etherconn.L2EndpointKey]*fakeRegistration
	regs      map[*fakeRegistration]struct{}
	defaultR  *fakeRegistration
	stats     *etherconn.RelayPacketStats
	stopOnce  sync.Once
	stoppedCh chan struct{}
}

// NewFakeRelay creates an empty in-memory relay.
func NewFakeRelay() *FakeRelay {
	return &FakeRelay{
		byKey:     make(map[etherconn.L2EndpointKey]*fakeRegistration),
		regs:      make(map[*fakeRegistration]struct{}),
		stats:     newFakeRelayStats(),
		stoppedCh: make(chan struct{}),
	}
}

func newFakeRelayStats() *etherconn.RelayPacketStats {
	s := new(etherconn.RelayPacketStats)
	s.Tx = new(uint64)
	s.RxOffered = new(uint64)
	s.RxInvalid = new(uint64)
	s.RxBufferFull = new(uint64)
	s.RxMiss = new(uint64)
	s.Rx = new(uint64)
	s.RxDefault = new(uint64)
	s.RxNonHitMulticast = new(uint64)
	s.RxMulticastIgnored = new(uint64)
	return s
}

// Register implements etherconn.PacketRelay.
func (f *FakeRelay) Register(ks []etherconn.L2EndpointKey, _ bool) (chan *etherconn.RelayReceival, chan []byte, chan struct{}) {
	reg := &fakeRegistration{
		keys: append([]etherconn.L2EndpointKey{}, ks...),
		recv: make(chan *etherconn.RelayReceival, recvChanDepth),
		send: make(chan []byte, sendChanDepth),
		stop: make(chan struct{}),
	}
	if len(ks) > 0 {
		reg.vlans, reg.etypes = decodeKeyMeta(ks[0])
	}

	f.mu.Lock()
	for _, k := range ks {
		f.byKey[k] = reg
	}
	f.regs[reg] = struct{}{}
	f.mu.Unlock()

	go f.forward(reg)
	return reg.recv, reg.send, reg.stop
}

// RegisterDefault implements etherconn.PacketRelay.
func (f *FakeRelay) RegisterDefault() (chan *etherconn.RelayReceival, chan []byte, chan struct{}) {
	reg := &fakeRegistration{
		recv: make(chan *etherconn.RelayReceival, recvChanDepth),
		send: make(chan []byte, sendChanDepth),
		stop: make(chan struct{}),
	}

	f.mu.Lock()
	f.defaultR = reg
	f.regs[reg] = struct{}{}
	f.mu.Unlock()

	go f.forward(reg)
	return reg.recv, reg.send, reg.stop
}

// Deregister implements etherconn.PacketRelay.
func (f *FakeRelay) Deregister(ks []etherconn.L2EndpointKey) {
	f.mu.Lock()
	defer f.mu.Unlock()

	toStop := map[*fakeRegistration]struct{}{}
	for _, k := range ks {
		if reg, ok := f.byKey[k]; ok {
			toStop[reg] = struct{}{}
			delete(f.byKey, k)
		}
	}
	for reg := range toStop {
		delete(f.regs, reg)
		if reg == f.defaultR {
			f.defaultR = nil
		}
		reg.stopNow()
	}
}

// Stop implements etherconn.PacketRelay.
func (f *FakeRelay) Stop() {
	f.stopOnce.Do(func() {
		close(f.stoppedCh)
		f.mu.Lock()
		for reg := range f.regs {
			reg.stopNow()
		}
		f.regs = make(map[*fakeRegistration]struct{})
		f.byKey = make(map[etherconn.L2EndpointKey]*fakeRegistration)
		f.defaultR = nil
		f.mu.Unlock()
	})
}

// IfName implements etherconn.PacketRelay.
func (f *FakeRelay) IfName() string { return FakeRelayIfName }

// GetStats implements etherconn.PacketRelay.
func (f *FakeRelay) GetStats() *etherconn.RelayPacketStats { return f.stats }

// Type implements etherconn.PacketRelay.
func (f *FakeRelay) Type() etherconn.RelayType { return etherconn.RelayTypeAFP }

func (f *FakeRelay) forward(reg *fakeRegistration) {
	for {
		select {
		case <-reg.stop:
			return
		case <-f.stoppedCh:
			return
		case frame := <-reg.send:
			atomic.AddUint64(f.stats.Tx, 1)
			f.deliver(reg, frame)
		}
	}
}

func (f *FakeRelay) deliver(sender *fakeRegistration, frame []byte) {
	rcv := frameToReceival(frame)
	if rcv == nil {
		atomic.AddUint64(f.stats.RxInvalid, 1)
		return
	}
	atomic.AddUint64(f.stats.RxOffered, 1)

	dstMAC := rcv.LocalEndpoint.HwAddr
	dstVLANs := rcv.LocalEndpoint.VLANs
	dstEtype := rcv.LocalEndpoint.Etype

	f.mu.RLock()
	var targets []*fakeRegistration
	if isMulticastMAC(dstMAC) {
		for reg := range f.regs {
			if reg == sender || reg.isDefault() {
				continue
			}
			if reg.matches(dstVLANs, dstEtype) {
				targets = append(targets, reg)
			}
		}
		if len(targets) == 0 && f.defaultR != nil && f.defaultR != sender {
			targets = append(targets, f.defaultR)
			atomic.AddUint64(f.stats.RxNonHitMulticast, 1)
		}
	} else {
		key := makeKey(dstMAC, dstVLANs, dstEtype)
		if reg, ok := f.byKey[key]; ok {
			targets = append(targets, reg)
		} else if f.defaultR != nil && f.defaultR != sender {
			targets = append(targets, f.defaultR)
		} else {
			atomic.AddUint64(f.stats.RxMiss, 1)
		}
	}
	f.mu.RUnlock()

	for _, reg := range targets {
		if reg == sender {
			continue
		}
		// Count the delivery before handing the frame off so that the receiver
		// observes Rx as soon as it reads; roll back if the channel is full.
		atomic.AddUint64(f.stats.Rx, 1)
		select {
		case reg.recv <- rcv:
		default:
			atomic.AddUint64(f.stats.Rx, ^uint64(0))
			atomic.AddUint64(f.stats.RxBufferFull, 1)
		}
	}
}

func (reg *fakeRegistration) isDefault() bool {
	return len(reg.keys) == 0
}

func newEndpoint() *etherconn.L2Endpoint {
	return &etherconn.L2Endpoint{
		HwAddr: make(net.HardwareAddr, 6),
		VLANs:  []uint16{},
	}
}

// frameToReceival parses an Ethernet frame the same way etherconn's raw relay
// does (without AFP VLAN ancillary data), so that RUDPConn gets a fully
// populated RelayReceival.
func frameToReceival(p []byte) *etherconn.RelayReceival {
	if len(p) < 14 {
		return nil
	}
	rcv := &etherconn.RelayReceival{
		LocalEndpoint:  newEndpoint(),
		RemoteEndpoint: newEndpoint(),
		EtherBytes:     p,
	}
	copy(rcv.LocalEndpoint.HwAddr, p[:6])    // destination
	copy(rcv.RemoteEndpoint.HwAddr, p[6:12]) // source

	index := 12
	var etype uint16
	for {
		if index+2 > len(p) {
			return nil
		}
		etype = binary.BigEndian.Uint16(p[index : index+2])
		if etype == 0x8100 || etype == 0x88a8 {
			if index+4 > len(p) {
				return nil
			}
			vid := binary.BigEndian.Uint16(p[index+2:index+4]) & 0x0fff
			rcv.LocalEndpoint.VLANs = append(rcv.LocalEndpoint.VLANs, vid)
			index += 4
			continue
		}
		rcv.LocalEndpoint.Etype = etype
		break
	}

	payload := p[index+2:]
	rcv.EtherPayloadBytes = payload

	var l4index int
	switch rcv.LocalEndpoint.Etype {
	case etherTypeIPv4:
		if len(payload) < 20 {
			break
		}
		rcv.RemoteIP = payload[12:16]
		rcv.LocalIP = payload[16:20]
		rcv.Protocol = payload[9]
		l4index = 20
	case etherTypeIPv6:
		if len(payload) < 40 {
			break
		}
		rcv.Protocol = payload[6]
		rcv.RemoteIP = payload[8:24]
		rcv.LocalIP = payload[24:40]
		l4index = 40
	}
	if rcv.Protocol == 17 && len(payload) >= l4index+8 {
		rcv.RemotePort = binary.BigEndian.Uint16(payload[l4index : l4index+2])
		rcv.LocalPort = binary.BigEndian.Uint16(payload[l4index+2 : l4index+4])
		rcv.TransportPayloadBytes = payload[l4index+8:]
	}

	rcv.RemoteEndpoint.Etype = rcv.LocalEndpoint.Etype
	rcv.RemoteEndpoint.VLANs = rcv.LocalEndpoint.VLANs
	return rcv
}

func isMulticastMAC(mac net.HardwareAddr) bool {
	return len(mac) > 0 && mac[0]&0x01 == 1
}

func decodeKeyMeta(key etherconn.L2EndpointKey) (vlans []uint16, etypes []uint16) {
	for i := 6; i+2 <= etherconn.L2EndpointKeySize-2; i += 2 {
		v := binary.BigEndian.Uint16(key[i : i+2])
		if v != etherconn.NOVLANTAG {
			vlans = append(vlans, v)
		}
	}
	etypes = []uint16{binary.BigEndian.Uint16(key[etherconn.L2EndpointKeySize-2:])}
	return
}

func makeKey(mac net.HardwareAddr, vlans []uint16, etype uint16) etherconn.L2EndpointKey {
	var k etherconn.L2EndpointKey
	copy(k[:6], mac)
	for i := 6; i+2 <= etherconn.L2EndpointKeySize-2; i += 2 {
		binary.BigEndian.PutUint16(k[i:i+2], etherconn.NOVLANTAG)
	}
	for i, v := range vlans {
		off := 6 + i*2
		if off+2 > etherconn.L2EndpointKeySize-2 {
			break
		}
		binary.BigEndian.PutUint16(k[off:off+2], v)
	}
	binary.BigEndian.PutUint16(k[etherconn.L2EndpointKeySize-2:], etype)
	return k
}

func equalUint16(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
