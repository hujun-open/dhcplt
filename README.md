# dhcplt
![Build Status](https://github.com/hujun-open/dhcplt/actions/workflows/main.yml/badge.svg)

dhcplt is a DHCPv4/DHCPv6 load tester for Linux with following features: 

- Using any source mac address and VLAN tags for DHCP packets without provisioning them in OS, this is achieved via using [etherconn](https://github.com/hujun-open/etherconn)

- DHCPv4

      - Support DORA, Release, Renew and Rebind
      - source addr, port could be customized
      - Following DHCPv4 options could be included in request:
            - Client Id
            - Vendor Class
            - Option82 Circuit-Id
            - Option82 Remote-Id
            - Gi Addr
            - Custom option
- DHCPv6:

      - Support DORA Release, Renew and Rebind
      - source addr, port could be customized
      - Request for IA_NA and/or IA_PD prefix
      - Send request in relay-forward message to simulate a relayed message, and handle the relay-reply message
      - following DHCPv6 options could be included in request:
            - BBF circuit-id/remote-id (only in relay message)
            - client id 
      - option of sending Router Solicit and expect Router Advertisement with M bit, before starting DHCPv6 

- Flapping: dhcplt support flapping, which repeatly establish and release DHCP leases. 
- performant: test shows that it could do 4k DORA per sec on a single core VM

## Usage Example
Notes: 

- **using dhcplt requires root privilege**
- action release, renew and rebind require a previous saved lease file



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
dhcplt -i eth1 -n 10000 -v4=false -v6=true -v6msgtype relay
```

12. example 1 variant, 5000 clients flapping
```
dhcplt -i eth1 -n 10000 -flap 5000 
```

13. example 1 variant, save lease to file
```
dhcplt -i eth1 -n 10000 -savelease
```

14. using saved lease file to send release msg, only release v4 leases in the file
```
dhcplt -i eth1 -action release
```

15. example 14 variant, release both v4 and v6 leases
```
dhcplt -i eth1 -action release -v6
```

16. using saved lease file to send renew msg, only send renew for all dhcpv4 leases in the lease file
```
dhcplt -i eth1 -action renew 
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
a DHCP load tester, unversioned
  - action: dora | release | renew | rebind
        default:dora
  - applylease: apply assigned address on the interface if true
        default:false
  - cid: BBF circuit-id
  - clntid: client-id
  - customv4option: custom DHCPv4 option, code:value format
  - customv6option: custom DHCPv6 option, code:value format
  - d: enable debug output
        default:false
  - driver: etherconn forward engine
        default:afpkt
  - excludedvlans: a list of excluded VLAN IDs
  - flapmaxinterval: minimal flapping interval
        default:5s
  - flapmininterval: max flapping interval
        default:30s
  - flapnum: number of client flapping
        default:0
  - flapstaydowndur: duriation of stay down
        default:10s
  - giaddr: Gi address for DHCPv4, simulating relay agent
        default:0.0.0.0
  - i: interface name
  - interval: interval between setup of sessions
        default:1s
  - leasefile: 
        default:dhcplt.lease
  - mac: starting MAC address
  - macstep: amount of increase between two consecutive MAC address
        default:1
  - n: number of clients
        default:1
  - needna: request DHCPv6 IANA if true
        default:true
  - needpd: request DHCPv6 IAPD if true
        default:false
  - profiling: enable profiling, dev use only
        default:false
  - retry: number of setup retry
        default:1
  - rid: BBF remote-id
  - savelease: save the lease if true
        default:false
  - sendrsfirst: send Router Solict first if true
        default:false
  - srcv4: source address for DHCPv4
        default:0.0.0.0
  - srcv4port: source port for egress DHCPv4 message
        default:68
  - srcv6: source address for DHCPv6
        default:::
  - srcv6port: source port for egress DHCPv6 message
        default:546
  - stackdelay: delay between setup v4 and v6, postive value means setup v4 first, negative means v6 first
        default:0s
  - timeout: setup timout
        default:5s
  - v4: do DHCPv4 if true
        default:true
  - v6: do DHCPv6 if true
        default:false
  - v6msgtype: DHCPv6 exchange type, solict|relay|auto
        default:auto
  - vendorclass: vendor class
  - vlan: starting VLAN ID, Dot1Q or QinQ
  - vlanetype: EthernetType for the vlan tag
        default:0x8100
  - vlanstep: amount of increase between two consecutive VLAN ID
        default:1

  -cfgfromfile: load configuration from the specified file
        default:dhcplt.conf
        
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
- srcv4port: by default source port is 68, 67 if giaddr is specified; however it could overriden by this parameter


### Config File
Thanks to [shouchan](https://github.com/hujun-open/shouchan), beside using CLI parameters, a YAML config file could also be used via "-f <conf_file>", the content of YAML is the `testSetup` struct 