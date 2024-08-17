package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/hujun-open/etherconn"
	"github.com/hujun-open/shouchan"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv6"
)

func init() {
	shouchan.Register[dhcpv4.Option](d4OptionToStr, d4OptionFromStr)
	shouchan.Register[dhcpv6.OptionGeneric](d6OptionToStr, d6OptionFromStr)
	shouchan.Register[dhcpv6.MessageType](d6MsgTypeToStr, d6MsgTypeFromStr)
}

type testSetup struct {
	Ifname       string           `alias:"i" usage:"interface name"`
	NumOfClients uint             `alias:"n" usage:"number of clients"`
	StartMAC     net.HardwareAddr `alias:"mac" usage:"starting MAC address"`
	MacStep      uint             `usage:"amount of increase between two consecutive MAC address"`
	StartVLANs   etherconn.VLANs  `alias:"vlan" usage:"starting VLAN ID, Dot1Q or QinQ"`
	VLANEType    uint             `usage:"EthernetType for the vlan tag" base:"16"`
	VLANStep     uint             `usage:"amount of increase between two consecutive VLAN ID"`

	ExcludedVLANs  []uint16             `usage:"a list of excluded VLAN IDs"`
	Interval       time.Duration        `usage:"interval between setup of sessions"`
	CustomV4Option dhcpv4.Option        `usage:"custom DHCPv4 option, code:value format"`
	CustomV6Option dhcpv6.OptionGeneric `usage:"custom DHCPv6 option, code:value format"`
	v4Options      []dhcpv4.Option
	v6Options      dhcpv6.Options //non-relay specific options
	Debug          bool           `alias:"d" usage:"enable debug output"`
	SaveLease      bool           `usage:"save the lease if true"`
	ApplyLease     bool           `usage:"apply assigned address on the interface if true"`
	Retry          uint           `usage:"number of setup retry"`
	Timeout        time.Duration  `usage:"setup timout"`
	//following are template str, $ID will be replaced by client id
	RID         string `usage:"BBF remote-id"`
	CID         string `usage:"BBF circuit-id"`
	ClntID      string `usage:"client-id"`
	VendorClass string `usage:"vendor class"`
	EnableV4    bool   `alias:"v4" usage:"do DHCPv4 if true"`
	//v6 specific
	EnableV6    bool               `alias:"v6" usage:"do DHCPv6 if true"`
	StackDelay  time.Duration      `usage:"delay between setup v4 and v6, postive value means setup v4 first, negative means v6 first"`
	V6MsgType   dhcpv6.MessageType `usage:"DHCPv6 exchange type, solict|relay|auto"`
	NeedNA      bool               `usage:"request DHCPv6 IANA if true"`
	NeedPD      bool               `usage:"request DHCPv6 IAPD if true"`
	pktRelay    etherconn.PacketRelay
	Driver      etherconn.RelayType `usage:"etherconn forward engine"`
	Flapping    *FlappingConf       `usage:"enable flapping"`
	SendRSFirst bool                `usage:"send Router Solict first if true"`
	Profiling   bool                `usage:"enable profiling, dev use only"`
	LeaseFile   string
	Action      actionType `usage:"dora | release"`
	saveV4Chan  chan *v4LeaseWithID
	saveV6Chan  chan *v6LeaseWithID
}

func newDefaultConf() *testSetup {
	return &testSetup{
		Action:       actionDORA,
		NumOfClients: 1,
		StartMAC:     []byte{},
		MacStep:      1,
		VLANEType:    etherconn.DefaultVLANEtype,
		VLANStep:     1,
		Interval:     time.Second,
		Retry:        1,
		Timeout:      5 * time.Second,
		EnableV4:     true,
		EnableV6:     false,
		V6MsgType:    dhcpv6.MessageTypeNone,
		Driver:       etherconn.RelayTypeAFP,
		LeaseFile:    "dhcplt.lease",
		Flapping: &FlappingConf{
			FlapNum:     0,
			MinInterval: defaultMinFlapInt,
			MaxInterval: defualtMaxFlapInt,
			StayDownDur: 10 * time.Second,
		},
	}
}

func (setup *testSetup) excluded(vids []uint16) bool {
	for _, vid := range vids {
		for _, extv := range setup.ExcludedVLANs {
			if extv == vid {
				return true
			}
		}
	}
	return false
}

const saveChanDepth = 8

func (setup *testSetup) init() error {
	if setup.Ifname == "" {
		return fmt.Errorf("interface name can't be empty")
	}
	if setup.NumOfClients <= 0 {
		return fmt.Errorf("number of clients can't be zero")
	}
	iff, err := net.InterfaceByName(setup.Ifname)
	if err != nil {
		return fmt.Errorf("can't find interface %v,%w", setup.Ifname, err)
	}
	if len(setup.StartMAC) == 0 {
		setup.StartMAC = iff.HardwareAddr
	}
	if !setup.EnableV4 && !setup.EnableV6 {
		return fmt.Errorf("both DHCPv4 and DHCPv6 are disabled")
	}
	if setup.NumOfClients == 0 {
		return fmt.Errorf("number of client is 0")
	}
	for _, v := range setup.StartVLANs {
		v.EtherType = uint16(setup.VLANEType)
	}
	setup.ExcludedVLANs = []uint16{}
	for _, n := range setup.ExcludedVLANs {
		if n > 4096 {
			return fmt.Errorf("%v is not valid vlan number", n)
		}
		setup.ExcludedVLANs = append(setup.ExcludedVLANs, n)
	}
	if setup.VendorClass != "" {
		setup.v4Options = append(setup.v4Options, dhcpv4.OptClassIdentifier(setup.VendorClass))
		setup.v6Options.Add(&dhcpv6.OptVendorClass{
			EnterpriseNumber: BBFEnterpriseNumber,
			Data:             [][]byte{[]byte(setup.VendorClass)},
		})
	}
	if setup.CustomV4Option.Code != nil {
		setup.v4Options = append(setup.v4Options, setup.CustomV4Option)
	}
	if setup.CustomV6Option.OptionCode != 0 {
		setup.v6Options = append(setup.v6Options, &setup.CustomV6Option)
	}
	if setup.V6MsgType == dhcpv6.MessageTypeNone {
		if setup.RID != "" || setup.CID != "" {
			setup.V6MsgType = dhcpv6.MessageTypeRelayForward
		} else {
			setup.V6MsgType = dhcpv6.MessageTypeSolicit
		}
	}
	setup.pktRelay, err = createPktRelay(setup)
	if err != nil {
		return err
	}
	if setup.Flapping.FlapNum > int(setup.NumOfClients) {
		return fmt.Errorf("flapping number %d can't be bigger than client number %d", setup.Flapping.FlapNum, setup.NumOfClients)
	}
	if setup.Flapping.MinInterval > setup.Flapping.MaxInterval {
		return fmt.Errorf("minimal flapping interval %v is bigger than max value %v", setup.Flapping.MinInterval, setup.Flapping.MaxInterval)
	}

	if setup.SaveLease || setup.Action == actionRelease {
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

func d4OptionToStr(in any) (string, error) {
	v := in.(dhcpv4.Option)
	if v.Code == nil {
		return "", nil
	}
	return fmt.Sprintf("%d:%v", v.Code.Code(), v.Value.String()), nil
}

func d6OptionFromStr(text string) (any, error) {
	if text == "" {
		return dhcpv6.OptionGeneric{
			OptionCode: 0,
		}, nil
	}
	return parseD6CustomOptionStr(text)
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

func d6MsgTypeToStr(in any) (string, error) {
	v := in.(dhcpv6.MessageType)
	if v == dhcpv6.MessageTypeNone {
		return "auto", nil
	}
	return strings.ToLower(v.String()), nil
}
