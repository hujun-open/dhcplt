package main

import (
	"fmt"
	"math/big"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/hujun-open/dhcplt/common"
	"github.com/hujun-open/etherconn"
	"github.com/hujun-open/myaddr"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv6"
)

func newSetupviaFlags(
	Ifname string,
	NumOfClients uint,
	retry uint,
	timeout time.Duration,
	StartMAC string,
	MacStep uint,
	vlan int,
	vlanetype uint,
	svlan int,
	svlanetype uint,
	VLANStep uint,
	excludevlanid string,
	Interval time.Duration,
	Debug bool,
	rid, cid, clntid, vclass, customop string,
	isv4, isv6 bool, v6mtype string, needNA, needPD bool, sendRS bool,
	SaveLease bool,
	ApplyLease bool,
	eng string,
	flapnum int,
	minflap, maxflap, flapdown time.Duration,
) (*testSetup, error) {
	var r testSetup
	r.ENG = eng
	switch r.ENG {
	case ENG_AFPKT:
	case ENG_XDP:
	default:
		return nil, fmt.Errorf("unknown engine %v", r.ENG)
	}
	if Ifname == "" {
		return nil, fmt.Errorf("interface name can't be empty")
	}
	if NumOfClients == 0 {
		return nil, fmt.Errorf("number of clients can't be zero")
	}
	iff, err := net.InterfaceByName(Ifname)
	if err != nil {
		return nil, fmt.Errorf("can't find interface %v,%w", Ifname, err)
	}
	r.Ifname = Ifname
	if NumOfClients == 0 {
		return nil, fmt.Errorf("number of client is 0")
	}
	r.NumOfClients = NumOfClients
	if StartMAC == "" {
		r.StartMAC = iff.HardwareAddr
	} else {
		r.StartMAC, err = net.ParseMAC(StartMAC)
		if err != nil {
			return nil, err
		}
	}
	r.MacStep = MacStep
	r.Retry = retry
	r.Timeout = timeout
	r.SendRSFirst = sendRS
	chkVIDFunc := func(vid int) bool {
		if vid < 0 || vid > 4095 {
			return false
		}
		return true
	}

	newvlanFunc := func(id int, etype uint) *etherconn.VLAN {
		if id >= 0 {
			return &etherconn.VLAN{
				ID:        uint16(id),
				EtherType: uint16(etype),
			}
		}
		return nil
	}
	if chkVIDFunc(vlan) {
		r.StartVLANs = etherconn.VLANs{}
		if v := newvlanFunc(vlan, vlanetype); v != nil {
			r.StartVLANs = append(r.StartVLANs, v)
		}
		if chkVIDFunc(svlan) {
			if v := newvlanFunc(svlan, svlanetype); v != nil {
				r.StartVLANs = append(etherconn.VLANs{v}, r.StartVLANs...)
			}
		}

	} else {
		if chkVIDFunc(svlan) {
			return nil, fmt.Errorf("spcifying svlan also require specifying a valid vlan")
		}
	}

	r.VLANStep = VLANStep
	strToExtListFunc := func(exts string) []uint16 {
		vidstrList := strings.Split(exts, ",")
		r := []uint16{}
		for _, vidstr := range vidstrList {
			vid, err := strconv.Atoi(vidstr)
			if err == nil {
				if vid >= 0 && vid <= 4095 {
					r = append(r, uint16(vid))
				}
			}
		}
		return r
	}
	r.ExcludedVLANs = strToExtListFunc(excludevlanid)

	r.Interval = Interval
	r.Debug = Debug
	r.V4Options = []dhcpv4.Option{}
	r.RID = rid
	r.CID = cid
	r.ClntID = clntid
	if vclass != "" {
		r.V4Options = append(r.V4Options, dhcpv4.OptClassIdentifier(vclass))
		r.V6Options.Add(&dhcpv6.OptVendorClass{
			EnterpriseNumber: BBFEnterpriseNumber,
			Data:             [][]byte{[]byte(vclass)},
		})
	}
	if customop != "" {
		cop, err := parseCustomOptionStr(customop)
		if err != nil {
			return nil, err
		}
		r.V4Options = append(r.V4Options, cop)
	}
	r.SaveLease = SaveLease
	r.ApplyLease = ApplyLease
	//DHCPv6
	r.EnableV6 = isv6
	r.EnableV4 = isv4

	switch v6mtype {
	case "relay":
		r.V6MsgType = dhcpv6.MessageTypeRelayForward
	case "solicit":
		r.V6MsgType = dhcpv6.MessageTypeSolicit
	case "auto":
		if r.RID != "" || r.CID != "" {
			r.V6MsgType = dhcpv6.MessageTypeRelayForward
		} else {
			r.V6MsgType = dhcpv6.MessageTypeSolicit
		}
	default:
		r.V6MsgType = dhcpv6.MessageTypeSolicit
	}
	r.NeedNA = needNA
	r.NeedPD = needPD
	r.pktRelay, err = createPktRelay(&r)
	if err != nil {
		return nil, err
	}
	if flapnum > int(r.NumOfClients) {
		return nil, fmt.Errorf("flapping number %d can't be bigger than client number %d", flapnum, r.NumOfClients)
	}
	if minflap > maxflap {
		return nil, fmt.Errorf("minimal flapping interval %v is bigger than max value %v", minflap, maxflap)
	}
	r.Flapping = &flappingConf{
		flapNum:     flapnum,
		minInterval: minflap,
		maxInterval: maxflap,
		stayDownDur: flapdown,
	}
	return &r, nil
}

type clientConfig struct {
	Mac              net.HardwareAddr
	VLANs            etherconn.VLANs
	V4Options        []dhcpv4.Option
	V6Options        dhcpv6.Options
	V6RelayOptions   dhcpv6.Options
	setup            *testSetup
	v4econn, v6econn *etherconn.EtherConn
}

func genClientConfigurations(setup *testSetup) ([]clientConfig, error) {
	r := []clientConfig{}
	clntmac := setup.StartMAC
	vlans := setup.StartVLANs
	var err error
	for i := 0; i < int(setup.NumOfClients); i++ {
		ccfg := clientConfig{}
		ccfg.setup = setup
		//assign mac
		ccfg.Mac = clntmac
		if i > 0 {
			ccfg.Mac, err = myaddr.IncMACAddr(clntmac, big.NewInt(int64(setup.MacStep)))
			if err != nil {
				return []clientConfig{}, fmt.Errorf("failed to generate mac address,%v", err)
			}

		}
		clntmac = ccfg.Mac
		//assign vlan
		ccfg.VLANs = vlans.Clone()

		incvidFunc := func(ids, excludes []uint16, step int) ([]uint16, error) {
			newids := ids
			for i := 0; i < 10; i++ {
				newids, err = myaddr.IncreaseVLANIDs(newids, step)
				if err != nil {
					return []uint16{}, err
				}
				excluded := false
			L1:
				for _, v := range newids {
					for _, exc := range excludes {
						if v == exc {
							excluded = true
							break L1
						}
					}
				}
				if !excluded {
					return newids, nil
				}
			}
			return []uint16{}, fmt.Errorf("you shouldn't see this")
		}

		if (len(vlans) > 0 && i > 0) || setup.excluded(vlans.IDs()) {
			rids, err := incvidFunc(vlans.IDs(), setup.ExcludedVLANs, int(setup.VLANStep))
			if err != nil {
				return []clientConfig{}, fmt.Errorf("failed to generate vlan id,%v", err)
			}
			err = ccfg.VLANs.SetIDs(rids)
			if err != nil {
				return []clientConfig{}, fmt.Errorf("failed to generate and apply vlan id,%v", err)
			}
		}
		vlans = ccfg.VLANs
		//options
		ccfg.V4Options = []dhcpv4.Option{}
		ccfg.V4Options = append(ccfg.V4Options, setup.V4Options...)
		ccfg.V6Options = []dhcpv6.Option{}
		ccfg.V6Options = append(ccfg.V6Options, setup.V6Options...)
		genStrFunc := func(s string, id int) string {
			const varname = "@ID"
			if strings.Contains(s, varname) {
				ss := strings.ReplaceAll(s, varname, "%d")
				return fmt.Sprintf(ss, id)
			}
			return s
		}

		if setup.RID != "" || setup.CID != "" {
			subOptList := []dhcpv4.Option{}
			if setup.RID != "" {
				subOptList = append(subOptList, dhcpv4.OptGeneric(dhcpv4.AgentRemoteIDSubOption, []byte(genStrFunc(setup.RID, i))))
				ccfg.V6RelayOptions.Add(&dhcpv6.OptRemoteID{
					EnterpriseNumber: BBFEnterpriseNumber,
					RemoteID:         []byte(genStrFunc(setup.RID, i)),
				})
			}
			if setup.CID != "" {
				subOptList = append(subOptList, dhcpv4.OptGeneric(dhcpv4.AgentCircuitIDSubOption, []byte(genStrFunc(setup.CID, i))))
				ccfg.V6RelayOptions.Add(dhcpv6.OptInterfaceID([]byte((genStrFunc(setup.CID, i)))))
			}

			ccfg.V4Options = append(ccfg.V4Options, dhcpv4.OptRelayAgentInfo(subOptList...))

		}
		if setup.ClntID != "" {
			common.MyLog("gened clnt id is %v", genStrFunc(setup.ClntID, i))
			ccfg.V4Options = append(ccfg.V4Options, dhcpv4.OptClientIdentifier([]byte(genStrFunc(setup.ClntID, i))))
			ccfg.V6Options.Add(dhcpv6.OptClientID(
				&dhcpv6.DUIDEN{
					EnterpriseNumber:     BBFEnterpriseNumber,
					EnterpriseIdentifier: []byte(genStrFunc(setup.ClntID, i)),
				}))
		}
		if setup.EnableV4 {
			ccfg.v4econn = etherconn.NewEtherConn(ccfg.Mac, setup.pktRelay,
				etherconn.WithVLANs(ccfg.VLANs),
				etherconn.WithEtherTypes([]uint16{EthernetTypeIPv4}))
		}
		if setup.EnableV6 {
			ccfg.v6econn = etherconn.NewEtherConn(ccfg.Mac, setup.pktRelay,
				etherconn.WithVLANs(ccfg.VLANs),
				etherconn.WithEtherTypes([]uint16{EthernetTypeIPv6}))
		}
		r = append(r, ccfg)
	}
	return r, nil
}

type flappingConf struct {
	flapNum                  int
	minInterval, maxInterval time.Duration
	stayDownDur              time.Duration
}

const (
	defaultMinFlapInt   = 5 * time.Second
	defualtMaxFlapInt   = 30 * time.Second
	defaultFlapStayDown = 10 * time.Second
)
