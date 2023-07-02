package main

import (
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"

	"github.com/hujun-open/dhcplt/common"
	"github.com/hujun-open/etherconn"
	"github.com/hujun-open/myaddr"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv6"
)

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
		ccfg.V4Options = append(ccfg.V4Options, setup.v4Options...)
		ccfg.V6Options = []dhcpv6.Option{}
		ccfg.V6Options = append(ccfg.V6Options, setup.v6Options...)
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

type FlappingConf struct {
	FlapNum     int           `alias:"flapnum" usage:"number of client flapping"`
	MinInterval time.Duration `alias:"flapmaxinterval" usage:"minimal flapping interval"`
	MaxInterval time.Duration `alias:"flapmininterval"usage:"max flapping interval"`
	StayDownDur time.Duration `alias:"flapstaydowndur" usage:"duriation of stay down"`
}

const (
	defaultMinFlapInt   = 5 * time.Second
	defualtMaxFlapInt   = 30 * time.Second
	defaultFlapStayDown = 10 * time.Second
)
