package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"log"
	"net"
	"os"
	"sync"

	"github.com/hujun-open/dhcplt/common"
	"github.com/hujun-open/etherconn"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/vishvananda/netlink"
)

// DHCPv4 lease
type v4Lease struct {
	Lease     *myDHCPv4Lease
	VLANList  etherconn.VLANs
	IDOptions dhcpv4.Options
}

func newV4Lease() *v4Lease {
	r := new(v4Lease)
	r.VLANList = etherconn.VLANs{}
	r.IDOptions = make(dhcpv4.Options)
	return r
}

type exportV4Lease struct {
	Lease     []byte
	VLANAList []byte
	IDOptions []byte
}

func (el *exportV4Lease) setLease(l *v4Lease) error {
	var err error
	if l.Lease == nil {
		l.Lease = new(myDHCPv4Lease)
	}
	if err = l.Lease.UnmarshalBinary(el.Lease); err != nil {
		return err
	}
	if err = l.VLANList.UnmarshalBinary(el.VLANAList); err != nil {
		return err
	}
	if err = l.IDOptions.FromBytes(el.IDOptions); err != nil {
		return err
	}
	return err
}

func getExportV4Lease(l v4Lease) (exportV4Lease, error) {
	var err error
	var r exportV4Lease
	if r.Lease, err = l.Lease.MarshalBinary(); err != nil {
		return r, err
	}
	if r.VLANAList, err = l.VLANList.MarshalBinary(); err != nil {
		return r, err
	}
	r.IDOptions = l.IDOptions.ToBytes()
	return r, nil

}

func (lease v4Lease) MarshalBinary() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)

	exportL, err := getExportV4Lease(lease)
	if err != nil {
		return nil, err
	}
	err = enc.Encode(exportL)
	return buf.Bytes(), err
}

func (lease *v4Lease) UnmarshalBinary(data []byte) error {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)
	exportL := new(exportV4Lease)
	err := dec.Decode(exportL)
	if err != nil {
		return err
	}

	return exportL.setLease(lease)

}

// addrStr returns a string as "ip/prefixlen"
func (lease *v4Lease) addrStr() string {
	ipnet := net.IPNet{
		IP:   lease.Lease.ACK.YourIPAddr,
		Mask: lease.Lease.ACK.SubnetMask(),
	}
	return ipnet.String()
}

// Apply replace/remove the lease on specified interface,
func (lease *v4Lease) Apply(ifname string, apply bool) error {
	link, err := netlink.LinkByName(ifname)
	if err != nil {
		return err
	}
	addr, err := netlink.ParseAddr(lease.addrStr())
	if err != nil {
		return err
	}
	if apply {
		return netlink.AddrReplace(link, addr)
	}
	return netlink.AddrDel(link, addr)

}

type v6Lease struct {
	MAC                       net.HardwareAddr
	ReplyOptions              dhcpv6.Options
	Type                      dhcpv6.MessageType //rely or solicit
	VLANList                  etherconn.VLANs
	IDOptions, RelayIDOptions dhcpv6.Options
}

type v6LeaseExport struct {
	MAC                                 net.HardwareAddr
	ReplyOptionsBytes                   []byte
	Type                                dhcpv6.MessageType //rely or solicit
	VLANList                            etherconn.VLANs
	IDOptionsBytes, RelayIDOptionsBytes []byte
}

func (lease v6Lease) MarshalBinary() ([]byte, error) {
	export := v6LeaseExport{
		MAC:                 lease.MAC,
		ReplyOptionsBytes:   lease.ReplyOptions.ToBytes(),
		Type:                lease.Type,
		VLANList:            lease.VLANList,
		IDOptionsBytes:      lease.IDOptions.ToBytes(),
		RelayIDOptionsBytes: lease.RelayIDOptions.ToBytes(),
	}
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(&export)
	return buf.Bytes(), err
}
func (lease *v6Lease) UnmarshalBinary(b []byte) error {
	var export v6LeaseExport
	buf := bytes.NewBuffer(b)
	dec := gob.NewDecoder(buf)
	err := dec.Decode(&export)
	if err != nil {
		return err
	}
	lease.MAC = export.MAC
	lease.Type = export.Type
	lease.VLANList = export.VLANList

	err = (&lease.ReplyOptions).FromBytes(export.ReplyOptionsBytes)
	if err != nil {
		return err
	}
	err = (&lease.IDOptions).FromBytes(export.IDOptionsBytes)
	if err != nil {
		return err
	}
	err = (&lease.RelayIDOptions).FromBytes(export.RelayIDOptionsBytes)
	if err != nil {
		return err
	}
	return nil
}

// addStr return all IANA and IAPD as following format:
// NA: addr/128
// PD: prefix/len
func (lease *v6Lease) addrStr() (r []string) {
	for _, na := range lease.ReplyOptions.Get(dhcpv6.OptionIANA) {
		for _, addr := range na.(*dhcpv6.OptIANA).Options.Addresses() {
			ipnet := net.IPNet{
				IP:   addr.IPv6Addr,
				Mask: net.CIDRMask(128, 128),
			}
			r = append(r, ipnet.String())
		}
	}
	for _, pd := range lease.ReplyOptions.Get(dhcpv6.OptionIAPD) {
		for _, prefix := range pd.(*dhcpv6.OptIAPD).Options.Prefixes() {
			r = append(r, prefix.Prefix.String())
		}
	}
	return
}

func (lease *v6Lease) Genv6Release() (*dhcpv6.Message, error) {
	msg, err := dhcpv6.NewMessage()
	if err != nil {
		return nil, err
	}
	msg.MessageType = dhcpv6.MessageTypeRelease
	msg.AddOption(lease.ReplyOptions.GetOne(dhcpv6.OptionClientID))
	msg.AddOption(lease.ReplyOptions.GetOne(dhcpv6.OptionServerID))
	msg.AddOption(dhcpv6.OptElapsedTime(0))
	for _, na := range lease.ReplyOptions.Get(dhcpv6.OptionIANA) {
		msg.AddOption(na)
	}
	for _, pd := range lease.ReplyOptions.Get(dhcpv6.OptionIAPD) {
		msg.AddOption(pd)
	}
	return msg, nil
}

func (lease *v6Lease) Apply(ifname string, apply bool) error {
	link, err := netlink.LinkByName(ifname)
	if err != nil {
		return err
	}
	for _, addrstr := range lease.addrStr() {
		addr, err := netlink.ParseAddr(addrstr)
		if err != nil {
			return err
		}
		if apply {
			return netlink.AddrReplace(link, addr)
		} else {
			netlink.AddrDel(link, addr)
		}
	}
	return nil
}

type clientID string

// clientID is the l2epkey minus ethertype
func getClientIDFromL2Key(l2epkey etherconn.L2EndpointKey) clientID {
	r := fmt.Sprintf("%v", net.HardwareAddr(l2epkey[:6]))
	for i := 6; i+2 <= etherconn.L2EndpointKeySize-2; i += 2 {
		vid := binary.BigEndian.Uint16(l2epkey[i : i+2])
		if vid != etherconn.NOVLANTAG {
			r += fmt.Sprintf("|%d", vid)
		}
	}
	return clientID(r)
}

type v4LeaseWithID struct {
	ID    clientID
	Lease *v4Lease
}

type v6LeaseWithID struct {
	ID    clientID
	Lease *v6Lease
}

type fullStackLease struct {
	V4 *v4Lease
	V6 *v6Lease
}

type exportLeaseMap map[clientID]*fullStackLease

func saveLeaseToFiles(ctx context.Context, wg *sync.WaitGroup, v4chan chan *v4LeaseWithID, v6chan chan *v6LeaseWithID, outfile string) {
	defer wg.Done()
	leaseMap := make(exportLeaseMap)
L:
	for {
		select {
		case <-ctx.Done():
			break L
		case v4 := <-v4chan:
			if fl, ok := leaseMap[v4.ID]; ok {
				fl.V4 = v4.Lease
			} else {
				leaseMap[v4.ID] = &fullStackLease{
					V4: v4.Lease,
				}
			}
		case v6 := <-v6chan:
			if fl, ok := leaseMap[v6.ID]; ok {
				fl.V6 = v6.Lease
			} else {
				leaseMap[v6.ID] = &fullStackLease{
					V6: v6.Lease,
				}
			}
		}
	}

	buf := new(bytes.Buffer)
	enc := gob.NewEncoder(buf)
	err := enc.Encode(leaseMap)
	if err != nil {
		log.Fatalf("failed to encode, %v", err)
	}
	err = os.WriteFile(outfile, buf.Bytes(), 0644)
	if err != nil {
		log.Fatalf("failed to write to file %v, %v", outfile, err)
	}
	common.MyLog("lease saved to %v", outfile)
	return
}

// skip loading if in4 or in6 is empty string
func loadLeaseFromFile(inf string) (savedMap exportLeaseMap, err error) {
	savedMap = make(exportLeaseMap)
	data, err := os.ReadFile(inf)
	if err != nil {
		return nil, fmt.Errorf("failed to read from %v, %w", inf, err)
	}
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)
	err = dec.Decode(&savedMap)
	if err != nil {
		return nil, fmt.Errorf("failed to decode, %w", err)
	}
	return
}
