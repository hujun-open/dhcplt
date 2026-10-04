package main

import (
	"fmt"
	"net"
	"net/netip"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/hujun-open/etherconn"
	myflags "github.com/hujun-open/myflags/v2"
	"github.com/hujun-open/myflags/v2/types"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv6"
)

// type flagConverter struct {
// 	from myflags.FromStrFunc
// 	to   myflags.ToStrFunc
// }

// func (fc *flagConverter) ToStr(in any, tag reflect.StructTag) string {
// 	return fc.to(in, tag)
// }

// func (fc *flagConverter) FromStr(s string, tag reflect.StructTag) (any, error) {
// 	return fc.from(s, tag)
// }

func init() {
	myflags.Register[dhcpv4.Option](
		&types.FlagConverter{
			From: d4OptionFromStrMyflags,
			To:   d4OptionToStrMyflags,
		})
	myflags.Register[dhcpv6.OptionGeneric](
		&types.FlagConverter{
			From: d6OptionFromStrMyflags,
			To:   d6OptionToStrMyflags,
		})
	myflags.Register[dhcpv6.MessageType](
		&types.FlagConverter{
			From: d6MsgTypeFromStrMyflags,
			To:   d6MsgTypeToStrMyflags,
		})
}

type doraStruct struct {
	NumOfClients   uint                 `short:"n" usage:"number of clients"`
	StartMAC       net.HardwareAddr     `alias:"mac" usage:"starting MAC address, use interface mac if not specified"`
	MacStep        uint                 `usage:"amount of increase between two consecutive MAC address"`
	StartVLANs     etherconn.VLANs      `alias:"vlan" usage:"starting VLAN ID, Dot1Q or QinQ"`
	VLANEType      uint                 `usage:"EthernetType for the vlan tag" base:"16"`
	VLANStep       uint                 `usage:"amount of increase between two consecutive VLAN ID"`
	ExcludedVLANs  []uint16             `usage:"a list of excluded VLAN IDs"`
	CustomV4Option dhcpv4.Option        `usage:"custom DHCPv4 option, code:value format"`
	CustomV6Option dhcpv6.OptionGeneric `usage:"custom DHCPv6 option, code:value format"`
	v4Options      []dhcpv4.Option
	v6Options      dhcpv6.Options //non-relay specific options
	SaveLease      bool           `usage:"save the lease if true"`
	ApplyLease     bool           `usage:"apply assigned address on the interface if true"`

	RID         string `usage:"BBF remote-id" `
	CID         string `usage:"BBF circuit-id"`
	ClntID      string `usage:"client-id"`
	VendorClass string `usage:"vendor class"`

	NeedNA      bool          `usage:"request DHCPv6 IANA if true"`
	NeedPD      bool          `usage:"request DHCPv6 IAPD if true"`
	Flapping    *FlappingConf `usage:"enable flapping"`
	SendRSFirst bool          `usage:"send Router Solict first if true"`
}

type testSetup struct {
	Ifname string     `short:"i" usage:"interface name"`
	DORA   doraStruct `action:"" usage:"get address from server"`
	Renew  struct {
		Dummy string
	} `usage:"renew leases" action:""`
	ZipFile struct {
		Dummy2 string
	} `usage:"test action" action:""`
	Rebind       struct{}      `action:"" usage:"rebind lease"`
	Release      struct{}      `action:"" usage:"release lease"`
	GiAddr       netip.Addr    `usage:"Gi address for DHCPv4, simulating relay agent"`
	Interval     time.Duration `usage:"interval between setup of sessions"`
	Debug        bool          `short:"d" usage:"enable debug output"`
	Retry        uint          `usage:"number of setup retry"`
	Timeout      time.Duration `usage:"setup timout"`
	SourceV4Addr netip.Addr    `usage:"source address for DHCPv4" alias:"srcv4"`
	SourceV6Port uint16        `usage:"source port for egress DHCPv6 message" alias:"srcv6port"`
	SourceV4Port uint16        `usage:"source port for egress DHCPv4 message" alias:"srcv4port"`
	//following are template str, $ID will be replaced by client id
	EnableV4 bool `alias:"v4" usage:"do DHCPv4 if true"`
	//v6 specific
	EnableV6     bool               `alias:"v6" usage:"do DHCPv6 if true"`
	V6MsgType    dhcpv6.MessageType `usage:"DHCPv6 exchange type, solict|relay|auto" choices:"solict,relay,auto"`
	SourceV6Addr netip.Addr         `usage:"source address for DHCPv6" alias:"srcv6"`
	StackDelay   time.Duration      `usage:"delay between setup v4 and v6, postive value means setup v4 first, negative means v6 first"`
	pktRelay     etherconn.PacketRelay
	Driver       etherconn.RelayType `usage:"etherconn forward engine"`

	Profiling bool `usage:"enable profiling, dev use only"`
	LeaseFile string

	saveV4Chan chan *v4LeaseWithID
	saveV6Chan chan *v6LeaseWithID
}

func newDefaultConf() *testSetup {
	return &testSetup{
		DORA: doraStruct{
			NumOfClients: 1,
			StartMAC:     []byte{},
			MacStep:      1,
			VLANEType:    etherconn.DefaultVLANEtype,
			VLANStep:     1,

			NeedNA: true,

			Flapping: &FlappingConf{
				FlapNum:     0,
				MinInterval: defaultMinFlapInt,
				MaxInterval: defualtMaxFlapInt,
				StayDownDur: 10 * time.Second,
			},
		},

		Interval:     time.Second,
		GiAddr:       netip.MustParseAddr("0.0.0.0"),
		SourceV4Addr: netip.MustParseAddr("0.0.0.0"),
		SourceV6Addr: netip.MustParseAddr("::"),
		Retry:        1,
		Timeout:      5 * time.Second,
		EnableV4:     true,
		EnableV6:     false,
		V6MsgType:    dhcpv6.MessageTypeNone,
		SourceV6Port: dhcpv6.DefaultClientPort,
		SourceV4Port: dhcpv4.ClientPort,

		Driver:    etherconn.RelayTypeAFP,
		LeaseFile: "dhcplt.lease",
	}
}

func (setup *testSetup) excluded(vids []uint16) bool {
	for _, vid := range vids {
		for _, extv := range setup.DORA.ExcludedVLANs {
			if extv == vid {
				return true
			}
		}
	}
	return false
}

const saveChanDepth = 8

func (setup *testSetup) init(action actionType) error {
	if setup.Ifname == "" {
		return fmt.Errorf("interface name can't be empty")
	}
	if setup.DORA.NumOfClients <= 0 {
		return fmt.Errorf("number of clients can't be zero")
	}
	var iff *net.Interface
	var err error

	iff, err = net.InterfaceByName(setup.Ifname)
	if err != nil {
		return fmt.Errorf("can't find interface %v,%w", setup.Ifname, err)
	}
	if len(setup.DORA.StartMAC) == 0 {
		setup.DORA.StartMAC = iff.HardwareAddr
	}

	if !setup.EnableV4 && !setup.EnableV6 {
		return fmt.Errorf("both DHCPv4 and DHCPv6 are disabled")
	}
	if setup.SourceV4Port == 0 {
		return fmt.Errorf("source v4 port can't be zero")
	}
	if setup.SourceV6Port == 0 {
		return fmt.Errorf("source v6 port can't be zero")
	}
	if setup.EnableV4 {
		if !setup.SourceV4Addr.IsUnspecified() {
			if !setup.SourceV4Addr.Is4() || !setup.SourceV4Addr.IsGlobalUnicast() {
				return fmt.Errorf("source v4 address must be an IPv4 unicast addr")
			}
		}
		if setup.GiAddr.IsValid() {
			if !setup.GiAddr.IsUnspecified() {
				if !setup.GiAddr.Is4() || !setup.GiAddr.IsGlobalUnicast() {
					return fmt.Errorf("gi address must be an IPv4 unicast addr")
				}
			}
		} else {
			setup.GiAddr = netip.MustParseAddr("0.0.0.0")
		}
		if setup.GiAddr.IsUnspecified() != setup.SourceV4Addr.IsUnspecified() {
			fmt.Printf("warning: giaddr should be specified along with srcv4 address")
		}
	}
	if setup.EnableV6 {
		if !setup.SourceV6Addr.IsUnspecified() {
			if !setup.SourceV6Addr.Is6() || !setup.SourceV6Addr.IsGlobalUnicast() {
				return fmt.Errorf("source v6 address must be an IPv4 unicast addr")
			}
		}
	}

	if setup.DORA.NumOfClients == 0 {
		return fmt.Errorf("number of client is 0")
	}
	for _, v := range setup.DORA.StartVLANs {
		v.EtherType = uint16(setup.DORA.VLANEType)
	}

	setup.DORA.ExcludedVLANs = []uint16{}
	for _, n := range setup.DORA.ExcludedVLANs {
		if n > 4096 {
			return fmt.Errorf("%v is not valid vlan number", n)
		}
		setup.DORA.ExcludedVLANs = append(setup.DORA.ExcludedVLANs, n)
	}
	if setup.DORA.VendorClass != "" {
		setup.DORA.v4Options = append(setup.DORA.v4Options, dhcpv4.OptClassIdentifier(setup.DORA.VendorClass))
		setup.DORA.v6Options.Add(&dhcpv6.OptVendorClass{
			EnterpriseNumber: BBFEnterpriseNumber,
			Data:             [][]byte{[]byte(setup.DORA.VendorClass)},
		})
	}
	if setup.DORA.CustomV4Option.Code != nil {
		setup.DORA.v4Options = append(setup.DORA.v4Options, setup.DORA.CustomV4Option)
	}
	if setup.DORA.CustomV6Option.OptionCode != 0 {
		setup.DORA.v6Options = append(setup.DORA.v6Options, &setup.DORA.CustomV6Option)
	}
	if setup.V6MsgType == dhcpv6.MessageTypeNone {
		if setup.DORA.RID != "" || setup.DORA.CID != "" {
			setup.V6MsgType = dhcpv6.MessageTypeRelayForward
		} else {
			setup.V6MsgType = dhcpv6.MessageTypeSolicit
		}
	}

	setup.pktRelay, err = createPktRelay(setup)
	if err != nil {
		return err
	}
	if setup.DORA.Flapping.FlapNum > int(setup.DORA.NumOfClients) {
		return fmt.Errorf("flapping number %d can't be bigger than client number %d", setup.DORA.Flapping.FlapNum, setup.DORA.NumOfClients)
	}
	if setup.DORA.Flapping.MinInterval > setup.DORA.Flapping.MaxInterval {
		return fmt.Errorf("minimal flapping interval %v is bigger than max value %v", setup.DORA.Flapping.MinInterval, setup.DORA.Flapping.MaxInterval)
	}

	if setup.DORA.SaveLease || action == actionRelease {
		if setup.EnableV4 {
			setup.saveV4Chan = make(chan *v4LeaseWithID, saveChanDepth)
		}
		if setup.EnableV6 {
			setup.saveV6Chan = make(chan *v6LeaseWithID, saveChanDepth)
		}
	}

	return nil
}

func parseD4CustomOptionStr(coptStr string) (dhcpv4.Option, error) {
	strList := strings.SplitN(coptStr, ":", 2)
	if len(strList) < 2 {
		return dhcpv4.Option{}, fmt.Errorf("invalid custom option %v", coptStr)
	}
	var oid int
	var err error
	if oid, err = strconv.Atoi(strList[0]); err != nil {
		return dhcpv4.Option{}, fmt.Errorf("%v is not a number", strList[0])
	}
	return dhcpv4.Option{
		Code: dhcpv4.GenericOptionCode(oid),
		Value: dhcpv4.OptionGeneric{
			Data: []byte(strList[1]),
		},
	}, nil

}

func parseD6CustomOptionStr(coptStr string) (dhcpv6.Option, error) {
	strList := strings.SplitN(coptStr, ":", 2)
	if len(strList) < 2 {
		return nil, fmt.Errorf("invalid custom option %v", coptStr)
	}
	var oid int
	var err error
	if oid, err = strconv.Atoi(strList[0]); err != nil {
		return nil, fmt.Errorf("%v is not a number", strList[0])
	}
	return &dhcpv6.OptionGeneric{
		OptionCode: dhcpv6.OptionCode(oid),
		OptionData: []byte(strList[1]),
	}, nil

}

func d4OptionFromStr(text string) (any, error) {
	if text == "" {
		return dhcpv4.Option{
			Code: nil,
		}, nil
	}
	return parseD4CustomOptionStr(text)
}
func d4OptionFromStrMyflags(text string, tags reflect.StructTag) (any, error) {
	return d4OptionFromStr(text)
}

func d4OptionToStrMyflags(in any, tag reflect.StructTag) string {
	str, _ := d4OptionToStr(in)
	return str
}
func d4OptionToStr(in any) (string, error) {
	if in == nil {
		return "", nil
	}
	v := in.(dhcpv4.Option)
	if v.Code == nil {
		return "", nil
	}
	vals := ""
	if v.Value != nil {
		vals = v.Value.String()
	}
	return fmt.Sprintf("%d:%v", v.Code.Code(), vals), nil
}

func d6OptionFromStr(text string) (any, error) {
	if text == "" {
		return dhcpv6.OptionGeneric{
			OptionCode: 0,
		}, nil
	}
	return parseD6CustomOptionStr(text)
}

func d6OptionFromStrMyflags(text string, tags reflect.StructTag) (any, error) {
	return d6OptionFromStr(text)
}

func d6OptionToStrMyflags(in any, tag reflect.StructTag) string {
	str, _ := d6OptionToStr(in)
	return str
}

func d6OptionToStr(in any) (string, error) {
	v := in.(dhcpv6.OptionGeneric)
	if v.OptionCode == 0 {
		return "", nil
	}
	return fmt.Sprintf("%d:%v", v.OptionCode, string(v.OptionData)), nil
}

func d6MsgTypeFromStr(text string) (any, error) {
	switch strings.ToLower(text) {
	case "solicit":
		return dhcpv6.MessageTypeSolicit, nil
	case "relay":
		return dhcpv6.MessageTypeRelayForward, nil
	case "auto":
		return dhcpv6.MessageTypeNone, nil
	}
	return nil, fmt.Errorf("unsupported DHCPv6 type: %v", text)
}

func d6MsgTypeFromStrMyflags(text string, tags reflect.StructTag) (any, error) {
	return d6MsgTypeFromStr(text)
}

func d6MsgTypeToStr(in any) (string, error) {
	v := in.(dhcpv6.MessageType)
	if v == dhcpv6.MessageTypeNone {
		return "auto", nil
	}
	return strings.ToLower(v.String()), nil
}

func d6MsgTypeToStrMyflags(in any, tag reflect.StructTag) string {
	str, _ := d6MsgTypeToStr(in)
	return str
}
