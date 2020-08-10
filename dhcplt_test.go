// dhcplt_test
// This test require kea-dhcp4 server, and root priviliage

package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/hujun-open/cmprule"
	"github.com/hujun-open/etherconn"
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
	return netlink.LinkSetUp(linkb)
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
	keaConf    string
	svipstr    string
	svrvlans   etherconn.VLANs
	setup      *testSetup
	ruleList   []string
	shouldFail bool
}

func dotest(c testCase) error {
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
	conf, err := ioutil.TempFile("", "keaconf*")
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
	time.Sleep(time.Second)
	summary := DORA(c.setup)
	myLog("%v", summary)
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

func TestDHCPLT(t *testing.T) {
	testList := []testCase{
		testCase{
			setup: &testSetup{
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
				"TotalTime : < : 1s",
			},
		},

		//two vlans
		testCase{
			setup: &testSetup{
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
				"TotalTime : < : 1s",
			},
		},

		//negative case, wrong vlans
		testCase{
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
				"TotalTime : < : 1s",
			},
			shouldFail: true,
		},
	}
	for i, c := range testList {
		time.Sleep(time.Second)
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
	logger = log.New(os.Stderr, "", log.Ldate|log.Ltime)
	result := m.Run()
	os.Exit(result)
}
