// ndproxy
package main

import (
	"context"
	"fmt"
	"log"
	"net"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/hujun-open/etherconn"
)

type NDPProxy struct {
	targets map[string]net.HardwareAddr //key is stringify IP
	// mac     net.HardwareAddr
	relay *etherconn.RawSocketRelay
	econn *etherconn.EtherConn
}

func NewNDPProxyFromRelay(targets map[string]net.HardwareAddr, relay *etherconn.RawSocketRelay) *NDPProxy {
	r := new(NDPProxy)
	r.relay = relay
	r.targets = targets
	r.econn = etherconn.NewEtherConn(net.HardwareAddr{0, 0, 0, 0, 0, 0}, r.relay, etherconn.WithRecvMulticast(true))
	go r.recv()
	return r
}

func NewNDPProxy(targets map[string]net.HardwareAddr, ifname string) (*NDPProxy, error) {
	bpfFilter := "icmp6 or (vlan and icmp6)"
	relay, err := etherconn.NewRawSocketRelay(context.Background(), ifname, etherconn.WithBPFFilter(bpfFilter), etherconn.WithDebug(true))
	if err != nil {
		return nil, fmt.Errorf("failed to create raw socket fzr if %v", ifname)
	}
	return NewNDPProxyFromRelay(targets, relay), nil
}
func (proxy *NDPProxy) processReq(pbuf []byte, peermac net.HardwareAddr) {
	gpkt := gopacket.NewPacket(pbuf, layers.LayerTypeIPv6, gopacket.DecodeOptions{Lazy: true, NoCopy: true})
	if icmp6Layer := gpkt.Layer(layers.LayerTypeICMPv6NeighborSolicitation); icmp6Layer != nil {
		req := icmp6Layer.(*layers.ICMPv6NeighborSolicitation)
		log.Printf("got ND req for %v, while have %+v", req.TargetAddress, proxy.targets)
		if ownmac, ok := proxy.targets[req.TargetAddress.String()]; ok {
			// for _, t := range proxy.targets {
			// if t.Equal(req.TargetAddress) {
			peerIPlayer := gpkt.Layer(layers.LayerTypeIPv6)
			resp := &layers.ICMPv6NeighborAdvertisement{
				TargetAddress: req.TargetAddress,
				Flags:         0b01000000,
				Options: []layers.ICMPv6Option{
					layers.ICMPv6Option{
						Type: layers.ICMPv6OptTargetAddress,
						Data: []byte(ownmac),
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
			_, err := proxy.econn.WriteIPPktToFrom(buf.Bytes(), ownmac, peermac)
			if err != nil {
				log.Printf("failed to send resp, %v", err)
			}
			log.Printf("reponded to NS for %v", req.TargetAddress)
		}
		// }

	}
}
func (proxy *NDPProxy) recv() {
	for {
		pkt, remote, err := proxy.econn.ReadPkt()
		if err != nil {
			log.Fatalf("failed from recv, %v", err)
		}
		go proxy.processReq(pkt, remote)
	}

}
