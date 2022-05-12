package main

import (
	"bytes"
	"encoding/gob"
	"net"

	"github.com/hujun-open/etherconn"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/vishvananda/netlink"
)

//DHCPv4 lease
type v4Lease struct {
	Lease     *nclient4.Lease
	VLANList  etherconn.VLANs
	IDOptions dhcpv4.Options
}

func newV4Lease() *v4Lease {
	r := new(v4Lease)
	r.VLANList = etherconn.VLANs{}
	r.IDOptions = make(dhcpv4.Options)
	return r
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
