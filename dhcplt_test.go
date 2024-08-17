// dhcplt_test
// To run this test in UB20.04, install kea-dhcp4-server, kea-dhcp6-server and libpcap-dev
// run with root privilage
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/hujun-open/dhcplt/common"

	"github.com/hujun-open/cmprule"
	"github.com/hujun-open/etherconn"

	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/vishvananda/netlink"
)

func replaceAddr(ifname, ipstr string) error {
	link, err := netlink.LinkByName(ifname)
	if err != nil {
		return err
	}
	addr, err := netlink.ParseAddr(ipstr)
	if err != nil {
		return err
	}
	return netlink.AddrReplace(link, addr)
}

func createVethLink(a, b string) error {
	linka := new(netlink.Veth)
	linka.Name = a
	linka.PeerName = b
	netlink.LinkDel(linka)
	time.Sleep(time.Second)
	err := netlink.LinkAdd(linka)
	if err != nil {
		return err
	}
	linkb, err := netlink.LinkByName(b)
	if err != nil {
		return err
	}
	err = netlink.LinkSetUp(linka)
	if err != nil {
		return err
	}
	err = netlink.LinkSetUp(linkb)
	if err != nil {
		return err
	}
	err = etherconn.SetIfVLANOffloading(a, false)
	if err != nil {
		return err
	}
	err = etherconn.SetIfVLANOffloading(b, false)
	if err != nil {
		return err
	}
	return nil
}

func createVLANIF(parentif string, vlans etherconn.VLANs) (netlink.Link, error) {
	pif, err := netlink.LinkByName(parentif)
	if err != nil {
		return nil, err
	}
	currentParent := pif
	for _, v := range vlans {
		vlanif := new(netlink.Vlan)
		vlanif.ParentIndex = currentParent.Attrs().Index
		vlanif.VlanId = int(v.ID)
		vlanif.VlanProtocol = netlink.VlanProtocol(v.EtherType)
		vlanif.Name = fmt.Sprintf("%v.%v", currentParent.Attrs().Name, v.ID)
		err := netlink.LinkAdd(vlanif)
		if err != nil {
			return nil, err
		}
		err = netlink.LinkSetUp(vlanif)
		if err != nil {
			return nil, err
		}
		currentParent = vlanif
	}
	return currentParent, nil
}

type testCase struct {
	desc       string
	keaConf    string
	svipstr    string
	svrvlans   etherconn.VLANs
	setup      *testSetup
	ruleList   []string
	shouldFail bool
}

func dotestv6(c testCase, eng etherconn.RelayType) error {
	fmt.Printf("initiate test case %+v\n", c)
	var err error
	err = createVethLink("S", "C")
	if err != nil {
		return err
	}
	svrif, err := createVLANIF("S", c.svrvlans)
	if err != nil {
		return err
	}
	err = replaceAddr(svrif.Attrs().Name, c.svipstr)
	if err != nil {
		return err
	}
	//NOTE: here need to wait for some time so that interface becomes oper-up
	time.Sleep(3 * time.Second)
	os.Remove("/var/lib/kea/dhcp6.leases")
	conf, err := os.CreateTemp("", "keav6conf*")
	if err != nil {
		return err
	}
	_, err = conf.Write([]byte(c.keaConf))
	if err != nil {
		return err
	}
	cmd := exec.Command("kea-dhcp6", "-d", "-c", conf.Name())
	logfile, err := os.CreateTemp("", "k6.log")
	if err != nil {
		return err
	}
	defer logfile.Close()
	cmd.Stdout = logfile
	err = cmd.Start()
	if err != nil {
		return err
	}
	defer cmd.Process.Release()
	defer cmd.Process.Kill()
	time.Sleep(time.Second)
	c.setup.Driver = eng
	c.setup.pktRelay, err = createPktRelay(c.setup)
	if err != nil {
		return err
	}
	sch, err := NewSched(c.setup)
	if err != nil {
		return fmt.Errorf("failed to create sched, %v", err)
	}
	ctx, cancelf := context.WithTimeout(context.Background(), testCaseTimeout)
	defer cancelf()
	wg := new(sync.WaitGroup)
	wg.Add(1)
	sch.run(ctx, wg)
	summary := sch.summary
	common.MyLog("%v", summary)
	defer fmt.Println(c.setup.pktRelay.GetStats())
	cmp := cmprule.NewDefaultCMPRule()
	for _, rule := range c.ruleList {
		err = cmp.ParseRule(rule)
		if err != nil {
			return err
		}
		result, err := cmp.Compare(summary)
		if err != nil {
			return err
		}
		if !result {
			return fmt.Errorf("failed to meet compare rule:%v", rule)
		}
	}
	return nil
}

const (
	testCaseTimeout = 3 * time.Minute
)

func dotest(c testCase) error {
	fmt.Printf("initiate test case %+v\n", c)
	c.setup.EnableV4 = true
	err := createVethLink("S", "C")
	if err != nil {
		return err
	}
	svrif, err := createVLANIF("S", c.svrvlans)
	if err != nil {
		return err
	}
	err = replaceAddr(svrif.Attrs().Name, c.svipstr)
	if err != nil {
		return err
	}
	conf, err := os.CreateTemp("", "keaconf*")
	if err != nil {
		return err
	}
	_, err = conf.Write([]byte(c.keaConf))
	if err != nil {
		return err
	}
	//cmd := exec.Command("/bin/sh", "-c", fmt.Sprintf("kea-dhcp4 -c %v", conf.Name()))
	cmd := exec.Command("kea-dhcp4", "-c", conf.Name())
	err = cmd.Start()
	if err != nil {
		return err
	}
	defer cmd.Process.Release()
	defer cmd.Process.Kill()
	// c.setup.ENG = ENG_AFPKT
	// c.setup.pktRelay, err = createPktRelay(c.setup)
	// if err != nil {
	// 	return err
	// }
	// defer c.setup.pktRelay.Stop()
	// ccfgs, err := genClientConfigurations(c.setup)
	// if err != nil {
	// 	return err
	// }
	// // common.MyLog("start dora in 30s")
	// time.Sleep(time.Second)
	// // common.MyLog("test starts")
	c.setup.Driver = ENG_AFPKT
	c.setup.pktRelay, err = createPktRelay(c.setup)
	if err != nil {
		return err
	}
	sch, err := NewSched(c.setup)
	if err != nil {
		return fmt.Errorf("failed to create sched, %v", err)
	}
	ctx, cancelf := context.WithTimeout(context.Background(), testCaseTimeout)
	defer cancelf()
	wg := new(sync.WaitGroup)
	wg.Add(1)
	sch.run(ctx, wg)
	summary := sch.summary
	common.MyLog("%v", summary)
	cmp := cmprule.NewDefaultCMPRule()
	for _, rule := range c.ruleList {
		err = cmp.ParseRule(rule)
		if err != nil {
			return err
		}
		result, err := cmp.Compare(summary)
		if err != nil {
			return err
		}
		if !result {
			return fmt.Errorf("failed to meet compare rule:%v", rule)
		}
	}
	return nil
}

func TestDHCPv6(t *testing.T) {
	// var err error
	// setup := &testSetup{
	// 	V6MsgType:    dhcpv6.MessageTypeRelayForward,
	// 	Ifname:       "C",
	// 	NumOfClients: 10,
	// 	StartMAC:     net.HardwareAddr{0xde, 0x8f, 0x5f, 0x3a, 0x4e, 0x33},
	// 	EnableV6:     true,
	// 	NeedNA:       true,
	// 	NeedPD:       false,
	// 	Debug:        true,
	// 	MacStep:      1,
	// 	RID:          "disk-@ID",
	// 	CID:          "MYCID-@ID",
	// 	StartVLANs: etherconn.VLANs{
	// 		&etherconn.VLAN{
	// 			ID:        100,
	// 			EtherType: 0x8100,
	// 		},
	// 		&etherconn.VLAN{
	// 			ID:        200,
	// 			EtherType: 0x8100,
	// 		},
	// 	},
	// }
	// setup.pktRelay, err = createPktRelay(setup)
	// if err != nil {
	// 	t.Fatal(err)
	// }
	// ccfgs, err := genClientConfigurations(setup)
	// if err != nil {
	// 	t.Fatal(err)
	// }
	// DORAv6(setup, ccfgs)
	testList := []testCase{

		//case 0
		{
			desc: "single vlan, both PD and NA",
			setup: &testSetup{
				EnableV4:     false,
				EnableV6:     true,
				NeedNA:       true,
				NeedPD:       true,
				V6MsgType:    dhcpv6.MessageTypeSolicit,
				Debug:        true,
				Ifname:       "C",
				NumOfClients: 10,
				StartMAC:     net.HardwareAddr{0xaa, 0xbb, 0xcc, 11, 22, 33},
				MacStep:      1,
				Timeout:      10 * time.Second,
				Retry:        2,

				StartVLANs: etherconn.VLANs{
					&etherconn.VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
			},
			svrvlans: etherconn.VLANs{
				&etherconn.VLAN{
					ID:        100,
					EtherType: 0x8100,
				},
			},
			keaConf: `
{
# DHCPv6 configuration starts on the next line
"Dhcp6": {

# First we set up global values
    "valid-lifetime": 4000,
    "renew-timer": 1000,
    "rebind-timer": 2000,
    "preferred-lifetime": 3000,

# Next we set up the interfaces to be used by the server.
    "interfaces-config": {
        "interfaces": [ "S.100" ]
    },

# And we specify the type of lease database
    "lease-database": {
        "type": "memfile",
        "persist": true,
        "name": "/var/lib/kea/dhcp6.leases"
    },

# Finally, we list the subnets from which we will be leasing addresses.
    "subnet6": [
        {
            "subnet": "2001:db8:1::/64",
            "pools": [
                 {
                     "pool": "2001:db8:1::2-2001:db8:1::ffff"
                 }
             ],
          "pd-pools": [
                {
                    "prefix": "3000:1::",
                    "prefix-len": 64,
                    "delegated-len": 96
                }
            ],
        "interface": "S.100"
        }
    ]
}
}`,
			svipstr: "2001:dead::99/128",
			ruleList: []string{
				"Success : == : 10",
				"TotalTime : < : 10s",
			},
		},
		/////////////////// case1
		{
			desc: "double vlan, both PD and NA",
			setup: &testSetup{
				EnableV4:     false,
				EnableV6:     true,
				NeedNA:       true,
				NeedPD:       true,
				V6MsgType:    dhcpv6.MessageTypeSolicit,
				Debug:        false,
				Ifname:       "C",
				NumOfClients: 10,
				StartMAC:     net.HardwareAddr{0xaa, 0xbb, 0xcc, 11, 22, 33},
				MacStep:      1,
				Timeout:      3 * time.Second,
				Retry:        2,

				StartVLANs: etherconn.VLANs{
					&etherconn.VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
					&etherconn.VLAN{
						ID:        200,
						EtherType: 0x8100,
					},
				},
			},
			svrvlans: etherconn.VLANs{
				&etherconn.VLAN{
					ID:        100,
					EtherType: 0x8100,
				},
				&etherconn.VLAN{
					ID:        200,
					EtherType: 0x8100,
				},
			},
			keaConf: `
{
# DHCPv6 configuration starts on the next line
"Dhcp6": {

# First we set up global values
    "valid-lifetime": 4000,
    "renew-timer": 1000,
    "rebind-timer": 2000,
    "preferred-lifetime": 3000,

# Next we set up the interfaces to be used by the server.
    "interfaces-config": {
        "interfaces": [ "S.100.200" ]
    },

# And we specify the type of lease database
    "lease-database": {
        "type": "memfile",
        "persist": true,
        "name": "/var/lib/kea/dhcp6.leases"
    },

# Finally, we list the subnets from which we will be leasing addresses.
    "subnet6": [
        {
            "subnet": "2001:db8:1::/64",
            "pools": [
                 {
                     "pool": "2001:db8:1::2-2001:db8:1::ffff"
                 }
             ],
          "pd-pools": [
                {
                    "prefix": "3000:1::",
                    "prefix-len": 64,
                    "delegated-len": 96
                }
            ],
        "interface": "S.100.200"
        }
    ]
}
}`,
			svipstr: "2001:dead::99/128",
			ruleList: []string{
				"Success : == : 10",
				"TotalTime : < : 3s",
			},
		},
		////////////////////case2
		{
			desc: "single vlan, PD only",
			setup: &testSetup{
				EnableV4:     false,
				EnableV6:     true,
				NeedNA:       false,
				NeedPD:       true,
				V6MsgType:    dhcpv6.MessageTypeSolicit,
				Debug:        false,
				Ifname:       "C",
				NumOfClients: 10,
				StartMAC:     net.HardwareAddr{0xaa, 0xbb, 0xcc, 11, 22, 33},
				MacStep:      1,
				Timeout:      3 * time.Second,
				Retry:        2,

				StartVLANs: etherconn.VLANs{
					&etherconn.VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
			},
			svrvlans: etherconn.VLANs{
				&etherconn.VLAN{
					ID:        100,
					EtherType: 0x8100,
				},
			},
			keaConf: `
{
# DHCPv6 configuration starts on the next line
"Dhcp6": {

# First we set up global values
    "valid-lifetime": 4000,
    "renew-timer": 1000,
    "rebind-timer": 2000,
    "preferred-lifetime": 3000,

# Next we set up the interfaces to be used by the server.
    "interfaces-config": {
        "interfaces": [ "S.100" ]
    },

# And we specify the type of lease database
    "lease-database": {
        "type": "memfile",
        "persist": true,
        "name": "/var/lib/kea/dhcp6.leases"
    },

# Finally, we list the subnets from which we will be leasing addresses.
    "subnet6": [
        {
            "subnet": "2001:db8:1::/64",
            "pools": [
                 {
                     "pool": "2001:db8:1::2-2001:db8:1::ffff"
                 }
             ],
          "pd-pools": [
                {
                    "prefix": "3000:1::",
                    "prefix-len": 64,
                    "delegated-len": 96
                }
            ],
        "interface": "S.100"
        }
    ]
}
}`,
			svipstr: "2001:dead::99/128",
			ruleList: []string{
				"Success : == : 10",
				"TotalTime : < : 3s",
			},
		},
		////////////////////////case3
		{
			desc: "double vlan, NA only",
			setup: &testSetup{
				EnableV4:     false,
				EnableV6:     true,
				NeedNA:       true,
				NeedPD:       false,
				V6MsgType:    dhcpv6.MessageTypeSolicit,
				Debug:        false,
				Ifname:       "C",
				NumOfClients: 10,
				StartMAC:     net.HardwareAddr{0xaa, 0xbb, 0xcc, 11, 22, 33},
				MacStep:      1,
				Timeout:      3 * time.Second,
				Retry:        2,

				StartVLANs: etherconn.VLANs{
					&etherconn.VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
					&etherconn.VLAN{
						ID:        200,
						EtherType: 0x8100,
					},
				},
			},
			svrvlans: etherconn.VLANs{
				&etherconn.VLAN{
					ID:        100,
					EtherType: 0x8100,
				},
				&etherconn.VLAN{
					ID:        200,
					EtherType: 0x8100,
				},
			},
			keaConf: `
{
# DHCPv6 configuration starts on the next line
"Dhcp6": {

# First we set up global values
    "valid-lifetime": 4000,
    "renew-timer": 1000,
    "rebind-timer": 2000,
    "preferred-lifetime": 3000,

# Next we set up the interfaces to be used by the server.
    "interfaces-config": {
        "interfaces": [ "S.100.200" ]
    },

# And we specify the type of lease database
    "lease-database": {
        "type": "memfile",
        "persist": true,
        "name": "/var/lib/kea/dhcp6.leases"
    },

# Finally, we list the subnets from which we will be leasing addresses.
    "subnet6": [
        {
            "subnet": "2001:db8:1::/64",
            "pools": [
                 {
                     "pool": "2001:db8:1::2-2001:db8:1::ffff"
                 }
             ],
          "pd-pools": [
                {
                    "prefix": "3000:1::",
                    "prefix-len": 64,
                    "delegated-len": 96
                }
            ],
        "interface": "S.100.200"
        }
    ]
}
}`,
			svipstr: "2001:dead::99/128",
			ruleList: []string{
				"Success : == : 10",
				"TotalTime : < : 3s",
			},
		},
		////////////////////cas4
		{
			desc: "double vlan, both PD and NA, relayed",
			setup: &testSetup{
				EnableV4:     false,
				EnableV6:     true,
				NeedNA:       true,
				NeedPD:       true,
				V6MsgType:    dhcpv6.MessageTypeRelayForward,
				Debug:        false,
				Ifname:       "C",
				CID:          "mycid@ID",
				NumOfClients: 10,
				StartMAC:     net.HardwareAddr{0xaa, 0xbb, 0xcc, 11, 22, 33},
				MacStep:      1,
				Timeout:      3 * time.Second,
				Retry:        2,

				StartVLANs: etherconn.VLANs{
					&etherconn.VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
					&etherconn.VLAN{
						ID:        200,
						EtherType: 0x8100,
					},
				},
			},
			svrvlans: etherconn.VLANs{
				&etherconn.VLAN{
					ID:        100,
					EtherType: 0x8100,
				},
				&etherconn.VLAN{
					ID:        200,
					EtherType: 0x8100,
				},
			},
			keaConf: `
{
# DHCPv6 configuration starts on the next line
"Dhcp6": {

# First we set up global values
    "valid-lifetime": 4000,
    "renew-timer": 1000,
    "rebind-timer": 2000,
    "preferred-lifetime": 3000,

# Next we set up the interfaces to be used by the server.
    "interfaces-config": {
        "interfaces": [ "S.100.200" ]
    },

# And we specify the type of lease database
    "lease-database": {
        "type": "memfile",
        "persist": true,
        "name": "/var/lib/kea/dhcp6.leases"
    },

# Finally, we list the subnets from which we will be leasing addresses.
    "subnet6": [
        {
            "subnet": "2001:db8:1::/64",
            "pools": [
                 {
                     "pool": "2001:db8:1::2-2001:db8:1::ffff"
                 }
             ],
          "pd-pools": [
                {
                    "prefix": "3000:1::",
                    "prefix-len": 64,
                    "delegated-len": 96
                }
            ],
        "interface": "S.100.200"
        }
    ]
}
}`,
			svipstr: "2001:dead::99/128",
			ruleList: []string{
				"Success : == : 10",
				"TotalTime : < : 3s",
			},
		},
		////////////////////

	}
	for i, c := range testList {
		time.Sleep(6 * time.Second)
		t.Logf("testing case %d %v with rawrelay", i, c.desc)
		err := dotestv6(c, ENG_AFPKT)
		if err != nil {
			if c.shouldFail {
				fmt.Printf("case %d failed as expected,%v\n", i, err)
			} else {
				t.Fatalf("case %d failed,%v", i, err)
			}
		} else {
			if c.shouldFail {
				t.Fatalf("case %d succeed but should fail", i)
			}
		}
		t.Logf("testing case %d %v with xdprelay", i, c.desc)
		// err = dotestv6(c, ENG_XDP)
		// if err != nil {
		// 	if c.shouldFail {
		// 		fmt.Printf("case %d failed as expected,%v\n", i, err)
		// 	} else {
		// 		t.Fatalf("case %d failed,%v", i, err)
		// 	}
		// } else {
		// 	if c.shouldFail {
		// 		t.Fatalf("case %d succeed but should fail", i)
		// 	}
		// }
	}

}

func TestDHCPLT(t *testing.T) {
	testList := []testCase{
		{ //case 0
			setup: &testSetup{
				Debug:        true,
				Ifname:       "C",
				NumOfClients: 10,
				StartMAC:     net.HardwareAddr{0xaa, 0xbb, 0xcc, 11, 22, 33},
				MacStep:      1,
				Timeout:      3 * time.Second,
				Retry:        2,

				StartVLANs: etherconn.VLANs{
					&etherconn.VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
				},
			},
			svrvlans: etherconn.VLANs{
				&etherconn.VLAN{
					ID:        100,
					EtherType: 0x8100,
				},
			},
			keaConf: `
{                                                                         
# DHCPv4 configuration starts on the next line                            
"Dhcp4": {                                                                
                                                                          
# First we set up global values                                           
    "valid-lifetime": 4000,                                               
    "renew-timer": 1000,                                                  
    "rebind-timer": 2000,                                                 
                                                                          
# Next we set up the interfaces to be used by the server.                 
    "interfaces-config": {                                                
        "interfaces": [ "S.100" ]                                         
    },                                                                    
                                                                          
# And we specify the type of lease database                               
    "lease-database": {                                                   
        "type": "memfile",                                                
        "persist": true,                                                  
        "name": "/var/lib/kea/dhcp4.leases"                               
    },                                                                    
                                                                          
# Finally, we list the subnets from which we will be leasing addresses.   
    "subnet4": [                                                          
        {                                                                 
            "subnet": "192.0.2.0/24",                                     
            "pools": [                                                    
                {                                                         
                     "pool": "192.0.2.1 - 192.0.2.200"                    
                }                                                         
            ]                                                             
        }                                                                 
    ]                                                                     
# DHCPv4 configuration ends with the next line                            
}
}`,
			svipstr: "192.0.2.254/24",
			ruleList: []string{
				"Success : == : 10",
				"TotalTime : < : 10s",
			},
		},

		//two vlans
		{ //case 1
			setup: &testSetup{
				Ifname:       "C",
				Debug:        true,
				NumOfClients: 10,
				StartMAC:     net.HardwareAddr{0xaa, 0xbb, 0xcc, 11, 22, 33},
				MacStep:      1,
				Timeout:      3 * time.Second,
				Retry:        2,
				StartVLANs: etherconn.VLANs{
					&etherconn.VLAN{
						ID:        100,
						EtherType: 0x8100,
					},
					&etherconn.VLAN{
						ID:        200,
						EtherType: 0x8100,
					},
				},
			},
			svrvlans: etherconn.VLANs{
				&etherconn.VLAN{
					ID:        100,
					EtherType: 0x8100,
				},
				&etherconn.VLAN{
					ID:        200,
					EtherType: 0x8100,
				},
			},
			keaConf: `
{                                                                         
# DHCPv4 configuration starts on the next line                            
"Dhcp4": {                                                                
                                                                          
# First we set up global values                                           
    "valid-lifetime": 4000,                                               
    "renew-timer": 1000,                                                  
    "rebind-timer": 2000,                                                 
                                                                          
# Next we set up the interfaces to be used by the server.                 
    "interfaces-config": {                                                
        "interfaces": [ "S.100.200" ]                                         
    },                                                                    
                                                                          
# And we specify the type of lease database                               
    "lease-database": {                                                   
        "type": "memfile",                                                
        "persist": true,                                                  
        "name": "/var/lib/kea/dhcp4.leases"                               
    },                                                                    
                                                                          
# Finally, we list the subnets from which we will be leasing addresses.   
    "subnet4": [                                                          
        {                                                                 
            "subnet": "192.0.2.0/24",                                     
            "pools": [                                                    
                {                                                         
                     "pool": "192.0.2.1 - 192.0.2.200"                    
                }                                                         
            ]                                                             
        }                                                                 
    ]                                                                     
# DHCPv4 configuration ends with the next line                            
}
}`,
			svipstr: "192.0.2.254/24",
			ruleList: []string{
				"Success : == : 10",
				"TotalTime : < : 10s",
			},
		},

		//negative case, wrong vlans
		{ // case 2
			setup: &testSetup{
				Ifname:       "C",
				NumOfClients: 10,
				StartMAC:     net.HardwareAddr{0xaa, 0xbb, 0xcc, 11, 22, 33},
				MacStep:      1,
				Timeout:      3 * time.Second,
				Retry:        2,
				StartVLANs: etherconn.VLANs{
					&etherconn.VLAN{
						ID:        300,
						EtherType: 0x8100,
					},
					&etherconn.VLAN{
						ID:        200,
						EtherType: 0x8100,
					},
				},
			},
			svrvlans: etherconn.VLANs{
				&etherconn.VLAN{
					ID:        100,
					EtherType: 0x8100,
				},
				&etherconn.VLAN{
					ID:        200,
					EtherType: 0x8100,
				},
			},
			keaConf: `
{                                                                         
# DHCPv4 configuration starts on the next line                            
"Dhcp4": {                                                                
                                                                          
# First we set up global values                                           
    "valid-lifetime": 4000,                                               
    "renew-timer": 1000,                                                  
    "rebind-timer": 2000,                                                 
                                                                          
# Next we set up the interfaces to be used by the server.                 
    "interfaces-config": {                                                
        "interfaces": [ "S.100.200" ]                                         
    },                                                                    
                                                                          
# And we specify the type of lease database                               
    "lease-database": {                                                   
        "type": "memfile",                                                
        "persist": true,                                                  
        "name": "/var/lib/kea/dhcp4.leases"                               
    },                                                                    
                                                                          
# Finally, we list the subnets from which we will be leasing addresses.   
    "subnet4": [                                                          
        {                                                                 
            "subnet": "192.0.2.0/24",                                     
            "pools": [                                                    
                {                                                         
                     "pool": "192.0.2.1 - 192.0.2.200"                    
                }                                                         
            ]                                                             
        }                                                                 
    ]                                                                     
# DHCPv4 configuration ends with the next line                            
}
}`,
			svipstr: "192.0.2.254/24",
			ruleList: []string{
				"Success : == : 10",
				"TotalTime : < : 10s",
			},
			shouldFail: true,
		},
	}
	for i, c := range testList {
		// if i != 2 {
		// 	continue
		// }
		time.Sleep(6 * time.Second)
		t.Logf("testing case %d", i)
		err := dotest(c)
		if err != nil {
			if c.shouldFail {
				fmt.Printf("case %d failed as expected,%v\n", i, err)
			} else {
				t.Fatalf("case %d failed,%v", i, err)
			}
		} else {
			if c.shouldFail {
				t.Fatalf("case %d succeed but should fail", i)
			}
		}
	}
}

func TestMain(m *testing.M) {
	runtime.SetBlockProfileRate(1000000000)
	go func() {
		log.Println(http.ListenAndServe("0.0.0.0:6060", nil))
	}()

	log.SetFlags(log.Lshortfile | log.Ltime)
	common.Logger = log.New(os.Stderr, "", log.Ldate|log.Ltime)
	result := m.Run()
	os.Exit(result)
}
