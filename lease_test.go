package main

import (
	"context"
	"encoding/binary"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hujun-open/etherconn"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/insomniacslk/dhcp/iana"
)

func TestGetClientIDFromL2Key(t *testing.T) {
	mac := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0x11, 0x22, 0x33}
	makeKey := func(vlans ...uint16) etherconn.L2EndpointKey {
		var k etherconn.L2EndpointKey
		copy(k[:6], mac)
		// mark all vlan slots as "no tag" first
		for i := 6; i+2 <= etherconn.L2EndpointKeySize-2; i += 2 {
			binary.BigEndian.PutUint16(k[i:i+2], etherconn.NOVLANTAG)
		}
		for i, v := range vlans {
			binary.BigEndian.PutUint16(k[6+i*2:8+i*2], v)
		}
		return k
	}

	cases := []struct {
		name string
		key  etherconn.L2EndpointKey
		want clientID
	}{
		{name: "no vlan", key: makeKey(), want: "aa:bb:cc:11:22:33"},
		{name: "one vlan", key: makeKey(100), want: "aa:bb:cc:11:22:33|100"},
		{name: "two vlans", key: makeKey(100, 200), want: "aa:bb:cc:11:22:33|100|200"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := getClientIDFromL2Key(tc.key); got != tc.want {
				t.Errorf("getClientIDFromL2Key = %q, want %q", got, tc.want)
			}
		})
	}
}

// buildV4Msgs builds a matching OFFER/ACK pair used by the lease tests.
func buildV4Msgs(t *testing.T) (*dhcpv4.DHCPv4, *dhcpv4.DHCPv4) {
	t.Helper()
	mac := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0x11, 0x22, 0x33}
	mk := func(mt dhcpv4.MessageType) *dhcpv4.DHCPv4 {
		msg, err := dhcpv4.New(
			dhcpv4.WithMessageType(mt),
			dhcpv4.WithHwAddr(mac),
			dhcpv4.WithYourIP(net.IPv4(192, 0, 2, 10)),
			dhcpv4.WithNetmask(net.CIDRMask(24, 32)),
			dhcpv4.WithServerIP(net.IPv4(192, 0, 2, 254)),
		)
		if err != nil {
			t.Fatalf("dhcpv4.New(%v) error: %v", mt, err)
		}
		return msg
	}
	return mk(dhcpv4.MessageTypeOffer), mk(dhcpv4.MessageTypeAck)
}

func TestMyDHCPv4LeaseRoundTrip(t *testing.T) {
	offer, ack := buildV4Msgs(t)
	ts := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	orig := myDHCPv4Lease{Offer: offer, ACK: ack, CreationTime: ts}

	data, err := orig.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary error: %v", err)
	}
	var got myDHCPv4Lease
	if err := got.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary error: %v", err)
	}
	if !got.CreationTime.Equal(ts) {
		t.Errorf("CreationTime = %v, want %v", got.CreationTime, ts)
	}
	if got.ACK == nil || got.ACK.YourIPAddr.String() != "192.0.2.10" {
		t.Errorf("ACK.YourIPAddr = %v, want 192.0.2.10", got.ACK)
	}
	if got.ACK.MessageType() != dhcpv4.MessageTypeAck {
		t.Errorf("ACK type = %v, want ACK", got.ACK.MessageType())
	}
}

func TestV4LeaseRoundTrip(t *testing.T) {
	offer, ack := buildV4Msgs(t)
	orig := newV4Lease()
	orig.Lease = &myDHCPv4Lease{Offer: offer, ACK: ack, CreationTime: time.Now()}
	orig.VLANList = etherconn.VLANs{
		{ID: 100, EtherType: 0x8100},
		{ID: 200, EtherType: 0x8100},
	}
	orig.IDOptions = ack.Options

	if got := orig.addrStr(); got != "192.0.2.10/24" {
		t.Fatalf("addrStr = %q, want 192.0.2.10/24", got)
	}

	data, err := orig.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary error: %v", err)
	}
	var got v4Lease
	if err := got.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary error: %v", err)
	}
	if len(got.VLANList) != 2 {
		t.Fatalf("VLANList len = %d, want 2", len(got.VLANList))
	}
	if got.VLANList[0].ID != 100 || got.VLANList[1].ID != 200 {
		t.Errorf("VLANList IDs = %d,%d, want 100,200", got.VLANList[0].ID, got.VLANList[1].ID)
	}
	if got.Lease == nil || got.Lease.ACK == nil {
		t.Fatal("unmarshaled lease or ACK is nil")
	}
	if got.Lease.ACK.YourIPAddr.String() != "192.0.2.10" {
		t.Errorf("ACK.YourIPAddr = %v, want 192.0.2.10", got.Lease.ACK.YourIPAddr)
	}
	if len(got.IDOptions) != len(orig.IDOptions) {
		t.Errorf("IDOptions len = %d, want %d", len(got.IDOptions), len(orig.IDOptions))
	}
}

func v6TestOptions() (dhcpv6.Option, dhcpv6.Option) {
	mac := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0x11, 0x22, 0x33}
	srvmac := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x01}
	cid := dhcpv6.OptClientID(&dhcpv6.DUIDLLT{HWType: iana.HWTypeEthernet, LinkLayerAddr: mac})
	sid := dhcpv6.OptServerID(&dhcpv6.DUIDLLT{HWType: iana.HWTypeEthernet, LinkLayerAddr: srvmac})
	return cid, sid
}

func TestV6LeaseRoundTrip(t *testing.T) {
	mac := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0x11, 0x22, 0x33}
	cid, sid := v6TestOptions()
	orig := v6Lease{
		MAC:            mac,
		Type:           dhcpv6.MessageTypeReply,
		VLANList:       etherconn.VLANs{{ID: 100, EtherType: 0x8100}, {ID: 200, EtherType: 0x8100}},
		ReplyOptions:   dhcpv6.Options{cid, sid},
		IDOptions:      dhcpv6.Options{cid},
		RelayIDOptions: dhcpv6.Options{sid},
	}

	data, err := orig.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary error: %v", err)
	}
	var got v6Lease
	if err := got.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary error: %v", err)
	}
	if got.Type != dhcpv6.MessageTypeReply {
		t.Errorf("Type = %v, want REPLY", got.Type)
	}
	if got.MAC.String() != mac.String() {
		t.Errorf("MAC = %v, want %v", got.MAC, mac)
	}
	if len(got.VLANList) != 2 || got.VLANList[0].ID != 100 || got.VLANList[1].ID != 200 {
		t.Errorf("VLANList = %v, want 100|200", got.VLANList)
	}
	if got.ReplyOptions.GetOne(dhcpv6.OptionClientID) == nil {
		t.Error("ReplyOptions is missing client-id")
	}
	if got.ReplyOptions.GetOne(dhcpv6.OptionServerID) == nil {
		t.Error("ReplyOptions is missing server-id")
	}
	if got.IDOptions.GetOne(dhcpv6.OptionClientID) == nil {
		t.Error("IDOptions is missing client-id")
	}
	if got.RelayIDOptions.GetOne(dhcpv6.OptionServerID) == nil {
		t.Error("RelayIDOptions is missing server-id")
	}
}

func TestV6LeaseGenv6Release(t *testing.T) {
	cid, sid := v6TestOptions()
	lease := &v6Lease{ReplyOptions: dhcpv6.Options{cid, sid}}

	msg, err := lease.Genv6Release(dhcpv6.MessageTypeRelease)
	if err != nil {
		t.Fatalf("Genv6Release error: %v", err)
	}
	if msg.MessageType != dhcpv6.MessageTypeRelease {
		t.Errorf("MessageType = %v, want RELEASE", msg.MessageType)
	}
	if msg.GetOneOption(dhcpv6.OptionClientID) == nil {
		t.Error("release message is missing client-id")
	}
	if msg.GetOneOption(dhcpv6.OptionServerID) == nil {
		t.Error("release message is missing server-id")
	}
}

func TestSaveAndLoadLeaseFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "dhcplt.lease")

	ctx, cancel := context.WithCancel(context.Background())
	wg := new(sync.WaitGroup)
	wg.Add(1)
	// unbuffered channel: a successful send guarantees the writer consumed the
	// lease before we cancel the context.
	v4ch := make(chan *v4LeaseWithID)
	var v6ch chan *v6LeaseWithID
	go saveLeaseToFiles(ctx, wg, v4ch, v6ch, out)

	offer, ack := buildV4Msgs(t)
	lease := newV4Lease()
	lease.Lease = &myDHCPv4Lease{Offer: offer, ACK: ack, CreationTime: time.Now()}
	lease.VLANList = etherconn.VLANs{{ID: 100, EtherType: 0x8100}}
	id := clientID("aa:bb:cc:11:22:33|100")

	v4ch <- &v4LeaseWithID{ID: id, Lease: lease}
	cancel()
	wg.Wait()

	saved, err := loadLeaseFromFile(out)
	if err != nil {
		t.Fatalf("loadLeaseFromFile error: %v", err)
	}
	fl, ok := saved[id]
	if !ok {
		t.Fatalf("saved lease for id %q not found; got %v", id, saved)
	}
	if fl.V4 == nil {
		t.Fatal("saved v4 lease is nil")
	}
	if got := fl.V4.addrStr(); got != "192.0.2.10/24" {
		t.Errorf("saved v4 addr = %q, want 192.0.2.10/24", got)
	}
	if fl.V6 != nil {
		t.Errorf("saved v6 lease = %v, want nil", fl.V6)
	}
}

func TestLoadLeaseFromMissingFile(t *testing.T) {
	_, err := loadLeaseFromFile(filepath.Join(t.TempDir(), "nope.lease"))
	if err == nil {
		t.Error("loadLeaseFromFile(missing) = nil error, want error")
	}
}
