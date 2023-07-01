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
flag provided but not defined: -?
Usage of ./dhcplt:
  -cid string
        circuit-id
  -clntid string
        Client Identifier
  -customoption string
        add a custom option, id:value
  -d    enable debug output
  -eng string
        packet forward engine, afpkt|xdp (default "afpkt")
  -excludedvlans string
        excluded vlan IDs
  -flap int
        number of client flapping (default -1)
  -flapstaydown duration
        duration of flapping client stay down before reconnect (default 10s)
  -i string
        interface name
  -iana
        request IANA (default true)
  -iapd
        request IAPD
  -interval duration
        interval between launching client (default 1ms)
  -mac string
        mac address
  -macstep uint
        mac address step (default 1)
  -maxflapint duration
        max flapping interval (default 30s)
  -minflapint duration
        minimal flapping interval (default 5s)
  -n uint
        number of clients (default 1)
  -p    enable profiling, only for dev use
  -retry uint
        number of DHCP request retry (default 3)
  -rid string
        remote-id
  -savelease
        save leases
  -sendrs
        send RS and expect RA before dhcpv6
  -svlan int
        svlan tag (default -1)
  -svlanetype uint
        svlan tag EtherType (default 33024)
  -timeout duration
        DHCP request timeout (default 5s)
  -v    show version
  -v4
        enable/disable DHCPv4 client (default true)
  -v6
        enable/disable DHCPv6 client
  -v6m string
        v6 message type, auto|relay|solicit (default "auto")
  -vc string
        vendor class
  -vlan int
        vlan tag (default -1)
  -vlanetype uint
        vlan tag EtherType (default 33024)
  -vlanstep uint
        VLAN Id step
```
- interval: this is wait interval between launch client DORA
- all duration type could use syntax that can be parsed by GOlang flag.Duration, like "1s", "1ms"
- default value for vlanstep is 0
- vlanetype/svlanetype are EtherType for the tag as uint16 number
- customoption: format is "<option-id>:<value>" for example "60:dhcplt" means include an Option 60 with value as "dhcplt"
- -v6m: setting the DHCPv6 message type:
      - solicit
      - relay
      - auto: if rid or cid is specified, then it is relay; otherwise solict
- flap: the number of clients flapping
- maxflapint, minflapint: the duration a flapping client stay connected, it is random value between min and max
- flapstaydown: the duration a flapping client stay disconnected. 
