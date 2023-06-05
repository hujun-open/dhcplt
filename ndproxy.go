// ndproxy
package main

import (
	// "context"
	// "fmt"
	"log"
	"net"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/hujun-open/etherconn"
)

type L2Encap struct {
	HwAddr net.HardwareAddr
	Vlans  etherconn.VLANs
}

type NDPProxy struct {
	targets map[string]L2Encap //key is stringify IP
	relay   etherconn.PacketRelay
	econn   *etherconn.EtherConn
}

func NewNDPProxyFromRelay(targets map[string]L2Encap, relay etherconn.PacketRelay) *NDPProxy {
	r := new(NDPProxy)
	r.relay = relay
	r.targets = targets
	r.econn = etherconn.NewEtherConn(net.HardwareAddr{0, 0, 0, 0, 0, 0},
		r.relay, etherconn.WithDefault())
	go r.recv()
	return r
}

func (proxy *NDPProxy) processReq(pbuf []byte, peermac net.HardwareAddr) {
	gpkt := gopacket.NewPacket(pbuf, layers.LayerTypeIPv6, gopacket.DecodeOptions{Lazy: true, NoCopy: true})
	if icmp6Layer := gpkt.Layer(layers.LayerTypeICMPv6NeighborSolicitation); icmp6Layer != nil {
		req := icmp6Layer.(*layers.ICMPv6NeighborSolicitation)
		if l2ep, ok := proxy.targets[req.TargetAddress.String()]; ok {
			peerIPlayer := gpkt.Layer(layers.LayerTypeIPv6)
			resp := &layers.ICMPv6NeighborAdvertisement{
				TargetAddress: req.TargetAddress,
				Flags:         0b01000000,
				Options: []layers.ICMPv6Option{
					{
						Type: layers.ICMPv6OptTargetAddress,
						Data: []byte(l2ep.HwAddr),
					},
				},
			}
			respicmp6Layer := &layers.ICMPv6{
				TypeCode: layers.CreateICMPv6TypeCode(136, 0),
			}

			buf := gopacket.NewSerializeBuffer()
			iplayer := &layers.IPv6{
				Version:    6,
				SrcIP:      req.TargetAddress,
				DstIP:      peerIPlayer.(*layers.IPv6).SrcIP,
				NextHeader: layers.IPProtocol(58),
				HopLimit:   255, //must be 255, otherwise won't be acceptedz
			}
			respicmp6Layer.SetNetworkLayerForChecksum(iplayer)
			opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
			gopacket.SerializeLayers(buf, opts,
				iplayer,
				respicmp6Layer,
				resp)
			_, err := proxy.econn.WriteIPPktToFrom(buf.Bytes(), l2ep.HwAddr, peermac, l2ep.Vlans)
			if err != nil {
				log.Printf("failed to send resp, %v", err)
			}
		}

	}
}
func (proxy *NDPProxy) recv() {
	for {
		pkt, remote, err := proxy.econn.ReadPkt()
		if err != nil {
			log.Fatalf("failed from recv, %v", err)
		}
		go proxy.processReq(pkt, remote.HwAddr)
	}

}
