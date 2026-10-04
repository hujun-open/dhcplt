# dhcplt
![Build Status](https://github.com/hujun-open/dhcplt/actions/workflows/main.yml/badge.svg)

dhcplt is a DHCPv4/DHCPv6 load tester for Linux with the following features:

- Using any source MAC address and VLAN tags for DHCP packets without provisioning them in the OS, this is achieved via using [etherconn](https://github.com/hujun-open/etherconn)

- DHCPv4

      - Support DORA, Release, Renew and Rebind
      - Source address and port can be customized
      - Following DHCPv4 options can be included in the request:
            - Client Id
            - Vendor Class
            - Option82 Circuit-Id
            - Option82 Remote-Id
            - Gi Addr
            - Custom option
- DHCPv6:

      - Support DORA, Release, Renew and Rebind
      - Source address and port can be customized
      - Request for IA_NA and/or IA_PD prefix
      - Send the request in a relay-forward message to simulate a relayed message, and handle the relay-reply message
      - Following DHCPv6 options can be included in the request:
            - BBF circuit-id/remote-id (only in relay message)
            - Client id
      - Option of sending a Router Solicit and expecting a Router Advertisement with the M bit, before starting DHCPv6

- Flapping: dhcplt supports flapping, which repeatedly establishes and releases DHCP leases.
- Performant: tests show that it can do 4k DORA per sec on a single core VM

## Requirements

- Linux, and **root privileges** (raw sockets are used to send/receive frames)
- To build: Go (see `go.mod`) and the libpcap development headers, e.g. on
  Debian/Ubuntu `sudo apt-get install -y libpcap-dev`
- The default `--driver afpkt` uses an `AF_PACKET` socket; `--driver xdp` requires
  an XDP-capable NIC and kernel

## Usage

`dhcplt` uses subcommands, one per action. The available commands are:

```
dora        get address from server
rebind      rebind lease
release     release lease
renew       renew leases
```

Notes:

- `dora` requests and establishes leases; `release`, `renew` and `rebind` operate
  on leases previously written to the lease file (see `--leasefile` and
  `--savelease`).
- The subcommand must be given explicitly, e.g. `dhcplt dora -i eth1 -n 10000`.
- All global flags (interface, `--v4`, `--v6`, timeouts, ...) are accepted by
  every subcommand.

### Examples

`--vlan` accepts a single VLAN id (`100`) or a QinQ stack written as
`outer.inner` (`100.200`).

1. 10000 DHCPv4 clients doing DORA on interface eth1, no VLAN, starting MAC is
   the eth1 interface MAC, increased by 1 for each client:
```
dhcplt dora -i eth1 -n 10000
```
2. on top of example #1, stack two VLAN tags 100 (outer) and 200 (inner), all
   clients use the same VLAN tags:
```
dhcplt dora -i eth1 -n 10000 --vlan 100.200
```
3. on top of example #1, specify the starting MAC as aa:bb:cc:11:22:33:
```
dhcplt dora -i eth1 -n 10000 --mac aa:bb:cc:11:22:33
```
4. on top of example #2, each client increases the VLAN tag by 1, e.g. the 2nd
   is 100.201, the 3rd is 100.202, ...:
```
dhcplt dora -i eth1 -n 10000 --vlan 100.200 --vlanstep 1
```
5. on top of example #2, launch all clients at once, without waiting an interval:
```
dhcplt dora -i eth1 -n 10000 --vlan 100.200 --interval 0s
```
6. on top of example #2, include a Client-Id option; note "@ID" is replaced by the
   client index, e.g. the first client is "Client-0", the 2nd "Client-1", ...:
```
dhcplt dora -i eth1 -n 10000 --vlan 100.200 --clntid "Client-@ID"
```
7. example #1 version for DHCPv6:
```
dhcplt dora -i eth1 -n 10000 --v4=false --v6
```
8. example #7 variant, request both IA_NA and IA_PD:
```
dhcplt dora -i eth1 -n 10000 --v4=false --v6 --needna --needpd
```
9. example #8 variant, sending a Router Solicit first:
```
dhcplt dora -i eth1 -n 10000 --v4=false --v6 --needna --needpd --sendrsfirst
```
10. example #7 variant, simulating a relay:
```
dhcplt dora -i eth1 -n 10000 --v4=false --v6 --v6msgtype relay
```
11. example #1 variant, 5000 clients flapping:
```
dhcplt dora -i eth1 -n 10000 --flapnum 5000
```
12. example #1 variant, save the obtained leases to a file:
```
dhcplt dora -i eth1 -n 10000 --savelease
```
13. using the saved lease file to send release messages; only releases the DHCPv4
    leases in the file:
```
dhcplt release -i eth1
```
14. example #13 variant, release both DHCPv4 and DHCPv6 leases:
```
dhcplt release -i eth1 --v6
```
15. using the saved lease file to send renew messages for all DHCPv4 leases:
```
dhcplt renew -i eth1
```

## DORA Result Summary

After the DORA action completes, dhcplt displays a summary like the following:

```
Result Summary
total trans: 500
Success dial:500
Success release:0
Success renew:0
Success rebind:0
Failed trans:0
Duration:815.173804ms
Interval:1ms
Setup rate:613.3661282373594
Fastest dial success:69.320291ms
dial Success within a second:500
Slowest dial success:173.38359ms
Avg dial success time:135.940204ms
```

- Total trans: number of DHCPv4 or DHCPv6 transactions; one DORA or one release
  counts as one transaction.
- Success dial/release/renew/rebind: number of successful DORA, release, renew or
  rebind transactions.
- Duration: between the launch of the first client and the stop of the last client.
- Interval: launch interval, specified by `--interval`.
- Setup rate: number of successful DORA / duration in seconds.
- Fastest/Slowest dial success, Success within a second, Avg dial success time:
  the time a client took to complete a DORA. For example, fastest dial success
  means the least amount of time a client took to complete a DORA.

## Command Reference

Global flags (accepted by every subcommand):

```
  -d, --debug                          enable debug output
      --driver                         etherconn forward engine (default afpkt)
      --giaddr                         Gi address for DHCPv4, simulating relay agent (default 0.0.0.0)
  -h, --help                           help for dhcplt
  -i, --ifname string                  interface name
      --interval time.Duration         interval between setup of sessions (default 1s)
      --leasefile string                (default "dhcplt.lease")
      --profiling                      enable profiling, dev use only
      --retry uint                     number of setup retry (default 1)
      --srcv4                          source address for DHCPv4 (default 0.0.0.0)
      --srcv4port uint16               source port for egress DHCPv4 message (default 68)
      --srcv6                          source address for DHCPv6 (default ::)
      --srcv6port uint16               source port for egress DHCPv6 message (default 546)
      --stackdelay time.Duration       delay between setup v4 and v6, positive value means setup v4 first, negative means v6 first (default 0s)
      --timeout time.Duration          setup timeout (default 5s)
      --v4                             do DHCPv4 if true (default true)
      --v6                             do DHCPv6 if true
      --v6msgtype dhcpv6.MessageType   DHCPv6 exchange type, solicit|relay|auto (default auto)
  -v, --version                        version for dhcplt
```

`dora`-specific flags (run `dhcplt dora --help` for the live list):

```
      --applylease                            apply assigned address on the interface if true
      --cid string                            BBF circuit-id
      --clntid string                         client-id
      --customv4option dhcpv4.Option          custom DHCPv4 option, code:value format
      --customv6option dhcpv6.OptionGeneric   custom DHCPv6 option, code:value format
      --excludedvlans []uint16                a list of excluded VLAN IDs
      --flapmaxinterval time.Duration         minimal flapping interval (default 5s)
      --flapmininterval time.Duration         max flapping interval (default 30s)
      --flapnum int                           number of client flapping (default 0)
      --flapstaydowndur time.Duration         duration of stay down (default 10s)
      --mac net.HardwareAddr                  starting MAC address, use interface mac if not specified
      --macstep uint                          amount of increase between two consecutive MAC address (default 1)
      --needna                                request DHCPv6 IANA if true (default true)
      --needpd                                request DHCPv6 IAPD if true
  -n, --numofclients uint                     number of clients (default 1)
      --rid string                            BBF remote-id
      --savelease                             save the lease if true
      --sendrsfirst                           send Router Solicit first if true
      --vendorclass string                    vendor class
      --vlan                                  starting VLAN ID, Dot1Q or QinQ
      --vlanetype uint                        EthernetType for the vlan tag (default 0x8100)
      --vlanstep uint                         amount of increase between two consecutive VLAN ID (default 1)
```

Notes:

- `--interval` is the wait interval between launching clients.
- All duration values use Go `time.Duration` syntax, e.g. `1s`, `1ms`.
- `--vlan` is a single id (`100`) or a QinQ stack in `outer.inner` form (`100.200`).
- `--vlanetype` is the EtherType for the tag, as a number (`0x8100` is Dot1Q).
- `--customv4option`/`--customv6option` use `<option-id>:<value>`, e.g.
  `--customv4option 60:dhcplt` includes Option 60 with value "dhcplt".
- `--v6msgtype` sets the DHCPv6 exchange type:
      - `solicit`
      - `relay`
      - `auto`: if `--rid` or `--cid` is specified then relay, otherwise solicit
- `--flapnum` is the number of flapping clients.
- `--flapmaxinterval`, `--flapmininterval`: a flapping client stays connected for a
  random duration between these two values.
- `--flapstaydowndur`: the duration a flapping client stays disconnected.
- `--srcv4port`: by default the source port is 68, or 67 if `--giaddr` is
  specified; this flag overrides it.
- `--applylease` requires root and replaces the assigned address on the interface.

## Testing

The test strategy is documented in [doc/testing.md](doc/testing.md). In short:

- Unit and component tests (no root, no network):
```
go test -race -count=1 ./...
```
- End-to-end tests, which need root and run in an isolated network namespace:
```
sudo go test -tags e2e -count=1 -run '^TestE2E' -v .
```

Building and linking requires the libpcap development headers (see Requirements).

## Build

```
go build -o dhcplt .
```
