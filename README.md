# dhcplt
![Build Status](https://github.com/hujun-open/dhcplt/actions/workflows/main.yml/badge.svg)

dhcplt is a DHCPv4/DHCPv6 load tester for Linux with following features: 

- Using any source mac address and VLAN tags for DHCP packets without provisioning them in OS, this is achieved via using [etherconn](https://github.com/hujun-open/etherconn)

- DHCPv4

      - Support DORA and Release
      - Following DHCPv4 options could be included in request:
            - Client Id
            - Vendor Class
            - Option82 Circuit-Id
            - Option82 Remote-Id
            - Custom option
- DHCPv6:

      - Request for IA_NA and/or IA_PD prefix
      - Send request in relay-forward message to simulate a relayed message, and handle the relay-reply message
      - following DHCPv6 options could be included in request:
            - BBF circuit-id/remote-id (only in relay message)
            - client id 
      - option of sending Router Solicit and expect Router Advertisement with M bit, before starting DHCPv6 

- Flapping: dhcplt support flapping, which repeatly establish and release DHCP leases. 
- performant: test shows that it could do 4k DORA per sec on a single core VM

## Usage Example
**Note: using dhcplt requires root privilege**

1. 10000 DHCPv4 clients doing DORA on interface eth1, no VLAN, starting MAC address is the eth1 interface mac, increase by 1 for each client
```
dhcplt -i eth1 -n 10000
```
2. on top of example #1, using VLAN tag 100 and SVLAN tag 200, all clients use same VLAN tags
```
dhcplt -i eth1 -n 10000 -vlan 100 -svlan 200
```
3. on top of example #1, specifing starting MAC is aa:bb:cc:11:22:33
```
dhcplt -i eth1 -n 10000 -mac aa:bb:cc:11:22:33
```
4. on top of example #2, starting VLAN tags is 200.100, but each client increase VLAN tag by 1, e.g 2nd is 200.101, 3rd is 200.102..etc
```
dhcplt -i eth1 -n 10000 -vlan 100 -svlan 200 -vlanstep 1
```
5. on top of example #2, launch all clients at once, without waiting interval
```
dhcplt -i eth1 -n 10000 -vlan 100 -svlan 200 -interval 0s
```
6. on top of example #2, include Client-Id option, note "@ID" will be replaced by client index, e.g. first client is "Client-0", 2nd "Client-1", 3rd "Client-2" ..etc;
```
dhcplt -i eth1 -n 10000 -vlan 100 -svlan 200 -clntid "Client-@ID"
```

8. example 1 version for DHCPv6
```
dhcplt -i eth1 -n 10000 -v4=false -v6=true
```

9. example 8 variant, request both IA_NA and IA_PD
```
dhcplt -i eth1 -n 10000 -v4=false -v6=true -iapd=true -iana=true
```

10. example 9 variant, sending RS first
```
dhcplt -i eth1 -n 10000 -v4=false -v6=true -iapd=true -iana=true -sendrs
```

11. example 8 variant, simulating relay
```
dhcplt -i eth1 -n 10000 -v4=false -v6=true -v6m=relay
```

12. example 1 variant, 5000 clients flapping
```
dhcplt -i eth1 -n 10000 -flap 5000 
```

## DORA Result Summary
With action DORA, dhcplt will display a summary of results after it s done like following:
```
Result Summary
total trans: 500
Success dial:500
Success release:0
Failed trans:0
Duration:815.173804ms
Interval:1ms
Setup rate:613.3661282373594
Fastest dial success:69.320291ms
dial Success within a second:500
Slowest dial success:173.38359ms
Avg dial success time:135.940204ms
```
- Total trans: number of DHCPv4 or DHCPv6 transatctions, one DORA or one release is counted as one transaction.
- Success dial/release: number of success DORA or release transactions.
- Duration: between launch 1st client and stop of last client
- Interval: launch interval, specified by "-interval"
- Setup rate: the number of success DORA / duration in second
- Fastest/Slowest dial success/Success within a second/slowest success/Avg dial success time: these are amount time of a client complete DORA, e.g fastest dial success means least amount of time a client took to complete DORA

## Command Line Parameters

```
Usage:
  -f <filepath> : read from config file <filepath>
  -applylease <struct> : apply assigned address on the interface if true
        default:false
  -cid <struct> : BBF circuit-id
  -clntid <struct> : client-id
  -customv4option <struct> : custom DHCPv4 option, code:value format
  -customv6option <struct> : custom DHCPv6 option, code:value format
  -d <struct> : enable debug output
        default:false
  -driver <struct> : etherconn forward engine
        default:afpkt
  -excludedvlans <struct> : a list of excluded VLAN IDs
  -flapmaxinterval <struct> : minimal flapping interval
        default:5s
  -flapmininterval <struct> : max flapping interval
        default:30s
  -flapnum <struct> : number of client flapping
        default:0
  -flapstaydowndur <struct> : duriation of stay down
        default:10s
  -i <struct> : interface name
  -interval <struct> : interval between setup of sessions
        default:1s
  -mac <struct> : starting MAC address
  -macstep <struct> : amount of increase between two consecutive MAC address
        default:1
  -n <struct> : number of clients
        default:1
  -needna <struct> : request DHCPv6 IANA if true
        default:false
  -needpd <struct> : request DHCPv6 IAPD if true
        default:false
  -profiling <struct> : enable profiling, dev use only
        default:false
  -retry <struct> : number of setup retry
        default:1
  -rid <struct> : BBF remote-id
  -savelease <struct> : save the lease if true
        default:false
  -sendrsfirst <struct> : send Router Solict first if true
        default:false
  -stackdelay <struct> : delay between setup v4 and v6, postive value means setup v4 first, negative means v6 first
        default:0s
  -timeout <struct> : setup timout
        default:5s
  -v4 <struct> : do DHCPv4 if true
        default:true
  -v6 <struct> : do DHCPv6 if true
        default:false
  -v6msgtype <struct> : DHCPv6 exchange type, solict|relay|auto
        default:auto
  -vendorclass <struct> : vendor class
  -vlan <struct> : starting VLAN ID, Dot1Q or QinQ
  -vlanetype <struct> : EthernetType for the vlan tag
        default:33024
  -vlanstep <struct> : amount of increase between two consecutive VLAN ID
        default:1

```
- interval: this is wait interval between launch client DORA
- all duration type could use syntax that can be parsed by GOlang flag.Duration, like "1s", "1ms"
- vlanetype are EtherType for the tag as uint16 number
- customv4option/customv6option: format is "<option-id>:<value>" for example "60:dhcplt" means include an Option 60 with value as "dhcplt"
- -v6msgtype: setting the DHCPv6 message type:
      - solicit
      - relay
      - auto: if rid or cid is specified, then it is relay; otherwise solict
- flapnum: the number of clients flapping
- flapmaxinterval, flapmininterval: the duration a flapping client stay connected, it is random value between min and max
- flapstaydowndur: the duration a flapping client stay disconnected. 
