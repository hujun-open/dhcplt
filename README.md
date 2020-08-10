# dhcplt
[![Build Status](https://travis-ci.org/hujun-open/dhcplt.svg?branch=master)](https://travis-ci.org/hujun-open/dhcplt)

dhcplt is a DHCPv4 load tester for Linux with following features:

- Using any source mac address and VLAN tags for DHCP packets without provisioning them in OS, this is achieved via using [etherconn](https://github.com/hujun-open/etherconn)
- Support DORA and Release
- Following DHCP options could be included in request:
    - Client Id
    - Vendor Class
    - Option82 Circuit-Id
    - Option82 Remote-Id
    - Custom option

## Performance
dhcplt completed 1889 DORA per second on following setup:
 - two linux VMs on a laptop with Intel Core i7-7600U, one for dhcplt, one for kea-dhcpv4 (1.6.0) server 

To compare, perdhcp completed 1439 DORA per second with the same setup

## Usage Example
**Note: using dhcplt requires root privilege**

1. 10000 clients doing DORA on interface eth1, no VLAN, starting MAC address is the eth1 interface mac, increase by 1 for each client
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

7. release pervious saved leases for eth1
```
dhcplt -i eth1 -action release
```

## DORA Result Summary
With action DORA, dhcplt will display a summary of results after it s done like following:
```
Result Summary
total: 10000
Success:10000
Failed:0
Duration:7.065089682s
Interval:0s
Setup rate:1415.4102000258233
Fastest success:793.139638ms
Success within a second:207
Slowest success:6.045893163s
Avg success time:4.350656333s
```
- Total/Success/Failed are the number of clients, success means number of clients successfully completed DORA;
- Duration: between launch 1st client and stop of last client
- Interval: launch interval, specified by "-interval"
- Setup rate: the number of success client / number of section between launch of 1st client and completion of last success client
- Fastest success/Success within a second/slowest success/Avg success time: these are amount time of a client complete DORA, e.g fast success means amount of time fast client took to complete DORA

## Command Line Parameters

```
Usage of ./dhcplt:
  -action string
        DHCP action (default "dora")
  -cid string
        option82 circuit-id
  -clntid string
        Client Identifier
  -customoption string
        add a custom option, id:value
  -d    enable debug output
  -excludedvlans string
        excluded vlan IDs
  -i string
        interface name
  -interval duration
        interval between DORA (default 1ms)
  -mac string
        mac address
  -macstep uint
        mac address step (default 1)
  -n uint
        number of clients (default 1)
  -p    enable profiling, only for dev use
  -retry uint
        number of retry (default 3)
  -rid string
        option82 remote-id
  -savelease
        save leases
  -svlan int
        svlan tag (default -1)
  -svlanetype uint
        svlan tag EtherType (default 33024)
  -timeout duration
        timeout (default 5s)
  -v    show version
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
- savelease: save leases to `/tmp/dhcplt_leases/<ifname>`
- -action release: release previous saved lease, other parameter beside "-i" andn "-d" will be ignored;