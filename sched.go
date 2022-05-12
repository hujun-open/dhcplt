package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/hujun-open/dhcplt/common"
	"github.com/hujun-open/dhcplt/conpair"
	"github.com/hujun-open/dhcplt/dhcpv6relay"
	"github.com/hujun-open/etherconn"
	"github.com/hujun-open/myaddr"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/insomniacslk/dhcp/dhcpv6/nclient6"
	"github.com/insomniacslk/dhcp/iana"
)

type actionType int

const (
	actionDORA actionType = iota
	actionRelease
)

type dialResult struct {
	IsDHCPv6   bool
	action     actionType
	ExecResult execResult
	L2EP       etherconn.L2EndpointKey
	StartTime  time.Time
	FinishTime time.Time
}

type DClient struct {
	d4            *nclient4.Client
	d6            *nclient6.Client
	d4ReleaseClnt *nclient4.Client
	d6ReleaseClnt *nclient6.Client
	d6relay       *dhcpv6relay.RelayAgent
	d4Lease       *v4Lease
	d6Lease       *v6Lease
	cfg           *clientConfig
	id            etherconn.L2EndpointKey
	dialResultCh  chan *dialResult
	// saveLeaseCh  chan interface{}
}

func (dc *DClient) createV4ReleaseClnt() error {
	if dc.d4Lease == nil {
		return fmt.Errorf("can't create v4 release client for %v without v4 lease", dc.id)
	}
	rudpconn, err := etherconn.NewRUDPConn(
		myaddr.GenConnectionAddrStr("", dc.d4Lease.Lease.ACK.YourIPAddr, 68), dc.cfg.v4econn)
	if err != nil {
		return fmt.Errorf("failed to create raw udp conn for %v release,%v", dc.id, err)
	}
	clntModList := []nclient4.ClientOpt{nclient4.WithHWAddr(dc.d4Lease.Lease.ACK.ClientHWAddr)}
	if dc.cfg.setup.Debug {
		clntModList = append(clntModList, nclient4.WithDebugLogger())
	}
	dc.d4ReleaseClnt, err = nclient4.NewWithConn(rudpconn, dc.d4Lease.Lease.ACK.ClientHWAddr, clntModList...)
	if err != nil {
		return fmt.Errorf("failed to create dhcpv4 release client for %v,%v", dc.id, err)
	}
	return nil
}

func (dc *DClient) createV6ReleaseClnt() error {
	if dc.d6Lease == nil {
		return fmt.Errorf("can't create v6 release client for %v without v6 lease", dc.id)
	}
	dc.d6ReleaseClnt = dc.d6
	return nil
}

func (dc *DClient) dialAll(wg *sync.WaitGroup) {
	var err error
	if wg != nil {
		defer wg.Done()
	}
	if dc.d4 != nil {
		err = dc.dialv4()
		if err != nil {
			common.MyLog("failed to dial DHCPv4, %v", err)
		}
	}
	if dc.d6 != nil {
		err = dc.dialv6()
		if err != nil {
			common.MyLog("failed to dial DHCPv6, %v", err)
		}
	}
}

func (dc *DClient) dialv6() error {
	if dc.d6 == nil {
		return fmt.Errorf("dhcpv6 is not configured")
	}
	checkResp := func(msg *dhcpv6.Message) error {
		if dc.cfg.setup.NeedNA {

			if len(msg.Options.OneIANA().Options.Addresses()) == 0 {
				return fmt.Errorf("no IANA address is assigned")
			}
		}
		if dc.cfg.setup.NeedPD {
			if len(msg.Options.OneIAPD().Options.Prefixes()) == 0 {
				return fmt.Errorf("no IAPD prefix is assigned")
			}
		}
		return nil
	}
	result := new(dialResult)
	result.action = actionDORA
	result.IsDHCPv6 = true
	result.ExecResult = resultFailure
	result.StartTime = time.Now()
	defer func() {
		result.L2EP = dc.id
		result.FinishTime = time.Now()
		dc.dialResultCh <- result
	}()
	solicitMsg, err := buildSolicit(*dc.cfg)
	if err != nil {
		return fmt.Errorf("failed to create solicit msg for %v, %v", dc.id, err)
	}
	var reply *dhcpv6.Message
	switch dc.cfg.setup.V6MsgType {
	case dhcpv6.MessageTypeSolicit:
		adv, err := dc.d6.SendAndRead(context.Background(),
			nclient6.AllDHCPRelayAgentsAndServers, solicitMsg,
			nclient6.IsMessageType(dhcpv6.MessageTypeAdvertise))
		if err != nil {
			return fmt.Errorf("failed recv DHCPv6 advertisement for %v, %v", dc.id, err)
		}
		err = checkResp(adv)
		if err != nil {
			return fmt.Errorf("got invalid advertise msg for clnt %v, %v", dc.id, err)
		}
		request, err := NewRequestFromAdv(adv)
		if err != nil {
			return fmt.Errorf("failed to build request msg for clnt %v, %v", dc.id, err)
		}
		reply, err = dc.d6.SendAndRead(context.Background(),
			nclient6.AllDHCPRelayAgentsAndServers,
			request, nclient6.IsMessageType(dhcpv6.MessageTypeReply))
		if err != nil {
			return fmt.Errorf("failed to recv DHCPv6 reply for %v, %v", dc.id, err)
		}
		err = checkResp(reply)
		if err != nil {
			return fmt.Errorf("got invalid reply msg for %v, %v", dc.id, err)
		}
	case dhcpv6.MessageTypeRelayForward:
		adv, err := dc.d6.SendAndRead(context.Background(),
			nclient6.AllDHCPRelayAgentsAndServers, solicitMsg,
			nclient6.IsMessageType(dhcpv6.MessageTypeAdvertise))
		if err != nil {
			return fmt.Errorf("failed recv DHCPv6 advertisement for %v, %v", dc.id, err)
		}
		err = checkResp(adv)
		if err != nil {
			return fmt.Errorf("got invalid advertise msg for clnt %v, %v", dc.id, err)
		}
		request, err := NewRequestFromAdv(adv)
		if err != nil {
			return fmt.Errorf("failed to build request msg for clnt %v, %v", dc.id, err)
		}
		reply, err = dc.d6.SendAndRead(context.Background(),
			nclient6.AllDHCPRelayAgentsAndServers,
			request, nclient6.IsMessageType(dhcpv6.MessageTypeReply))
		if err != nil {
			return fmt.Errorf("failed to recv DHCPv6 reply for %v, %v", dc.id, err)
		}
		err = checkResp(reply)
		if err != nil {
			return fmt.Errorf("got invalid reply msg for %v, %v", dc.id, err)
		}

	}
	lease := &v6Lease{
		MAC:            dc.cfg.Mac,
		ReplyOptions:   reply.Options.Options,
		Type:           dc.cfg.setup.V6MsgType,
		VLANList:       dc.cfg.VLANs,
		IDOptions:      dc.cfg.V6Options,
		RelayIDOptions: dc.cfg.V6RelayOptions,
	}
	dc.d6Lease = lease
	if dc.cfg.setup.ApplyLease {
		err = lease.Apply(dc.cfg.setup.Ifname, true)
		if err != nil {
			return fmt.Errorf("failed to apply v6 lease for clnt %v, %v", dc.id, err)
		}
	}
	// if dc.cfg.setup.SaveLease {
	// 	dc.saveLeaseCh <- lease
	// }

	result.ExecResult = resultSuccess
	return nil

}

func (dc *DClient) dialv4() error {
	if dc.d4 == nil {
		return fmt.Errorf("dhcpv4 is not configured")
	}
	result := new(dialResult)
	result.action = actionDORA
	result.StartTime = time.Now()
	result.IsDHCPv6 = false
	result.ExecResult = resultFailure
	defer func() {
		result.L2EP = dc.id
		result.FinishTime = time.Now()
		dc.dialResultCh <- result
	}()
	common.MyLog("doing DORA for %v , on if %v",
		dc.id, dc.cfg.setup.Ifname)

	dhcpModList := []dhcpv4.Modifier{}
	for _, op := range dc.cfg.V4Options {
		dhcpModList = append(dhcpModList, dhcpv4.WithOption(op))
	}
	result.StartTime = time.Now()
	result.IsDHCPv6 = false
	lease, err := dc.d4.Request(context.Background(), dhcpModList...)
	if err != nil {
		return fmt.Errorf("failed complete DORA for %v,%v", dc.id, err)
	}
	dc.d4Lease = newV4Lease()
	dc.d4Lease.Lease = lease
	dc.d4Lease.VLANList = dc.cfg.VLANs
	if dc.cfg.setup.ApplyLease {
		err = dc.d4Lease.Apply(dc.cfg.setup.Ifname, true)
		if err != nil {
			return fmt.Errorf("failed to apply v4 lease for clnt %v, %v", dc.id, err)
		}
	}
	// if dc.cfg.setup.SaveLease {
	// 	dc.saveLeaseCh <- dc.d4Lease
	// }
	result.ExecResult = resultSuccess

	return nil
}

func (dc *DClient) releasev4() error {
	modList := []dhcpv4.Modifier{}
	for t := range dc.d4Lease.IDOptions {
		modList = append(modList,
			dhcpv4.WithOption(dhcpv4.OptGeneric(dhcpv4.GenericOptionCode(t),
				dc.d4Lease.IDOptions.Get(dhcpv4.GenericOptionCode(t)))))
	}
	var err error
	for i := 0; i < 3; i++ {
		err = dc.d4ReleaseClnt.Release(dc.d4Lease.Lease, modList...)
		if err == nil {
			break
		}
	}
	result := new(dialResult)
	result.StartTime = time.Now()
	result.ExecResult = resultSuccess
	result.action = actionRelease
	result.IsDHCPv6 = false
	result.L2EP = dc.id
	defer func() {
		result.FinishTime = time.Now()
		dc.dialResultCh <- result
	}()
	if err != nil {
		result.ExecResult = resultFailure
		return fmt.Errorf("failed to release v4 lease for clnt %v, %v", dc.id, err)
	}
	return nil
}

func (dc *DClient) releasev6() error {
	result := new(dialResult)
	result.ExecResult = resultSuccess
	result.action = actionRelease
	result.StartTime = time.Now()
	result.IsDHCPv6 = true
	result.L2EP = dc.id
	defer func() {
		result.FinishTime = time.Now()
		dc.dialResultCh <- result
	}()
	releaseMsg, err := dc.d6Lease.Genv6Release()
	if err != nil {
		return fmt.Errorf("failed to create v6 release msg for clnt %v, %v", dc.id, err)
	}
	for i := 0; i < 3; i++ {
		_, err = dc.d6ReleaseClnt.SendAndRead(context.Background(),
			nclient6.AllDHCPRelayAgentsAndServers, releaseMsg,
			nclient6.IsMessageType(dhcpv6.MessageTypeReply))
		if err == nil {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("failed to release v6 lease for clnt %v, %v", dc.id, err)
	}
	return nil
}

type Sched struct {
	ClntList     map[etherconn.L2EndpointKey]*DClient
	dialResultCh chan *dialResult
	summary      *resultSummary
	setup        *testSetup
}

const (
	dialResultChanLen = 1024
)

func NewSched(setup *testSetup) (*Sched, error) {
	r := new(Sched)
	r.setup = setup
	r.summary = newResultSummary(setup)
	r.dialResultCh = make(chan *dialResult, dialResultChanLen)
	r.ClntList = make(map[etherconn.L2EndpointKey]*DClient)
	clntConfs, err := genClientConfigurations(setup)
	if err != nil {
		return nil, err
	}
	for _, cfg := range clntConfs {
		dc := new(DClient)
		dc.cfg = new(clientConfig)
		*dc.cfg = cfg
		var key etherconn.L2EndpointKey
		if dc.cfg.v4econn != nil {
			key = dc.cfg.v4econn.LocalAddr().GetKey()
			rudpconn, err := etherconn.NewRUDPConn("0.0.0.0:68", dc.cfg.v4econn,
				etherconn.WithAcceptAny(true))
			if err != nil {
				return nil, fmt.Errorf("failed to create raw udp conn for %v,%v", dc.cfg.Mac, err)
			}
			clntModList := []nclient4.ClientOpt{
				nclient4.WithRetry(int(dc.cfg.setup.Retry)),
				nclient4.WithTimeout(dc.cfg.setup.Timeout),
			}
			if setup.Debug {
				clntModList = append(clntModList, nclient4.WithDebugLogger())
			}
			clntModList = append(clntModList, nclient4.WithHWAddr(dc.cfg.Mac))
			dc.d4, err = nclient4.NewWithConn(rudpconn, dc.cfg.Mac, clntModList...)
			if err != nil {
				return nil, fmt.Errorf("failed to create dhcpv4 client for %v,%v", dc.cfg.Mac, err)
			}
		}

		if dc.cfg.v6econn != nil {
			key = dc.cfg.v6econn.LocalAddr().GetKey()
			rudpconn, err := etherconn.NewRUDPConn(fmt.Sprintf("[%v]:%v",
				myaddr.GetLLAFromMac(dc.cfg.Mac),
				dhcpv6.DefaultClientPort), dc.cfg.v6econn, etherconn.WithAcceptAny(true))
			if err != nil {
				return nil, fmt.Errorf("failed to create raw udp conn for %v,%v", dc.cfg.Mac, err)
			}
			mods := []nclient6.ClientOpt{}
			if setup.Debug {
				mods = []nclient6.ClientOpt{nclient6.WithDebugLogger(), nclient6.WithLogDroppedPackets()}
			}
			switch dc.cfg.setup.V6MsgType {
			case dhcpv6.MessageTypeSolicit:
				dc.d6, err = nclient6.NewWithConn(rudpconn, dc.cfg.Mac, mods...)
				if err != nil {
					return nil, fmt.Errorf("failed to create DHCPv6 client for %v, %v", dc.cfg.Mac, err)
				}
			case dhcpv6.MessageTypeRelayForward:
				accessConClnt, accessConRelay := conpair.NewPacketConnPair()
				dc.d6, err = nclient6.NewWithConn(accessConClnt, dc.cfg.Mac, mods...)
				if err != nil {
					return nil, fmt.Errorf("failed to create DHCPv6 client for %v, %v", dc.cfg.Mac, err)
				}
				dc.d6relay = dhcpv6relay.NewRelayAgent(context.Background(),
					&dhcpv6relay.PairDHCPConn{PacketConnPair: accessConRelay},
					&dhcpv6relay.RUDPDHCPConn{RUDPConn: rudpconn},
					dhcpv6relay.WithLinkAddr(net.ParseIP("::")),
					dhcpv6relay.WithPeerAddr(myaddr.GetLLAFromMac(dc.cfg.Mac)),
					dhcpv6relay.WithOptions(dc.cfg.V6RelayOptions))
			default:
				return nil, fmt.Errorf("un-supported DHCPv6 msg type %v", dc.cfg.setup.V6MsgType)

			}
		}
		dc.id = key
		dc.dialResultCh = r.dialResultCh
		r.ClntList[key] = dc
	}
	//start NDPProxy for DHCPv6
	if r.setup.EnableV6 {
		llaList := make(map[string]L2Encap)
		for _, cfg := range clntConfs {
			llaList[myaddr.GetLLAFromMac(cfg.Mac).String()] = L2Encap{
				HwAddr: cfg.Mac,
				Vlans:  cfg.VLANs,
			}
		}
		NewNDPProxyFromRelay(llaList, r.setup.pktRelay)
	}
	return r, nil
}

func (sch *Sched) collectResults(wg *sync.WaitGroup) {
	defer wg.Done()
	var beginTime, endTime time.Time
	totalSuccessTime := time.Duration(0)
	beginTime = time.Now().AddDate(10, 0, 0)
	endTime = time.Time{}
	for r := range sch.dialResultCh {
		completeTime := r.FinishTime.Sub(r.StartTime)
		if r.FinishTime.After(endTime) {
			endTime = r.FinishTime
		}
		if completeTime < 0 {
			completeTime = 0
		}
		if r.StartTime.Before(beginTime) {
			beginTime = r.StartTime
		}
		sch.summary.Total++
		switch r.action {
		case actionRelease:
			sch.summary.Released++

		}
		switch r.ExecResult {
		case resultFailure:
			sch.summary.Failed++
		case resultSuccess:
			if r.action == actionDORA {
				sch.summary.Success++
				sch.summary.AvgSuccessTime.Add(float64(completeTime))
				totalSuccessTime += completeTime
				if completeTime < time.Second {
					sch.summary.LessThanSecond++
				}
				if completeTime > sch.summary.Longest {
					sch.summary.Longest = completeTime
				}
				if completeTime < sch.summary.Shortest {
					sch.summary.Shortest = completeTime
				}
			}

		}
		sch.summary.TotalTime = endTime.Sub(beginTime)
		if sch.summary.Shortest == maxDuration {
			sch.summary.Shortest = 0
		}
		fmt.Printf("\rdial succed: %7d\t released: %7d\t trans failed: %7d",
			sch.summary.Success, sch.summary.Released, sch.summary.Failed)
	}

}
func (sch *Sched) Stop() {
	close(sch.dialResultCh)
}
func (sch *Sched) run(ctx context.Context, taskWG *sync.WaitGroup) {
	defer taskWG.Done()
	//intial dialing
	wg := new(sync.WaitGroup)
	otherTG := new(sync.WaitGroup)
	otherTG.Add(1)
	go sch.collectResults(otherTG)
	var err error
	for _, c := range sch.ClntList {
		wg.Add(1)
		go c.dialAll(wg)
		time.Sleep(sch.setup.Interval)
	}
	wg.Wait()
	common.MyLog("dial finished")
	time.Sleep(time.Second)
	fmt.Printf("\ninitial dialing resutls are:\n%v", sch.summary)
	if sch.setup.Flapping != nil {
		if sch.setup.Flapping.flapNum > 0 {
			for _, cc := range sch.ClntList {
				if cc.d4ReleaseClnt == nil && cc.d4Lease != nil {
					err = cc.createV4ReleaseClnt()
					if err != nil {
						common.MyLog("%v", err)
						return
					}
				}
				if cc.d6ReleaseClnt == nil && cc.d6Lease != nil {
					err = cc.createV6ReleaseClnt()
					if err != nil {
						common.MyLog("%v", err)
						return
					}
				}
			}
			fmt.Printf("\nstart flapping %d clients...\n", sch.setup.Flapping.flapNum)
			intervalRange := sch.setup.Flapping.maxInterval - sch.setup.Flapping.minInterval
			flapFunc := func(ctx context.Context, dc *DClient, wg *sync.WaitGroup) {
				defer wg.Done()
				for {
					select {
					case <-ctx.Done():
						return
					default:
					}
					time.Sleep(sch.setup.Flapping.minInterval + time.Duration(rand.Int63n(int64(intervalRange))))
					select {
					case <-ctx.Done():
						return
					default:
					}
					if dc.d4Lease != nil {
						err = dc.releasev4()
						if err != nil {
							common.MyLog("%v", err)
						}
					}
					select {
					case <-ctx.Done():
						return
					default:
					}
					if dc.d6Lease != nil {
						err = dc.releasev6()
						if err != nil {
							common.MyLog("%v", err)
						}
					}
					select {
					case <-ctx.Done():
						return
					default:
					}
					time.Sleep(sch.setup.Flapping.stayDownDur)
					select {
					case <-ctx.Done():
						return
					default:
					}
					dc.dialAll(nil)
				}
			}
			i := 0
			wg = new(sync.WaitGroup)
			for _, dc := range sch.ClntList {
				if i < sch.setup.Flapping.flapNum {
					wg.Add(1)
					go flapFunc(ctx, dc, wg)
				}
			}
			wg.Wait()
			fmt.Printf("\nFinal result:\n%v", sch.summary)
		}
	}
	close(sch.dialResultCh)
	otherTG.Wait()

}

func getIAIDviaInt(v uint32) (r [4]byte) {
	binary.BigEndian.PutUint32(r[:], v)
	return
}

func buildSolicit(ccfg clientConfig) (*dhcpv6.Message, error) {
	optModList := []dhcpv6.Modifier{}
	for _, o := range ccfg.V6Options {
		optModList = append(optModList, dhcpv6.WithOption(o))
	}
	if ccfg.setup.NeedNA {
		optModList = append(optModList, dhcpv6.WithIAID(getIAIDviaInt(0)))
	}
	if ccfg.setup.NeedPD {
		optModList = append(optModList, dhcpv6.WithIAPD(getIAIDviaInt(1)))
	}
	duid := dhcpv6.Duid{
		Type:          dhcpv6.DUID_LL,
		HwType:        iana.HWTypeEthernet,
		Time:          dhcpv6.GetTime(),
		LinkLayerAddr: ccfg.Mac,
	}
	m, err := dhcpv6.NewMessage()
	if err != nil {
		return nil, err
	}
	m.MessageType = dhcpv6.MessageTypeSolicit
	m.AddOption(dhcpv6.OptClientID(duid))
	m.AddOption(dhcpv6.OptRequestedOption(
		dhcpv6.OptionDNSRecursiveNameServer,
		dhcpv6.OptionDomainSearchList,
	))
	m.AddOption(dhcpv6.OptElapsedTime(0))
	for _, mod := range optModList {
		mod(m)
	}
	return m, nil

}
func NewRequestFromAdv(adv *dhcpv6.Message, modifiers ...dhcpv6.Modifier) (*dhcpv6.Message, error) {
	if adv == nil {
		return nil, fmt.Errorf("ADVERTISE cannot be nil")
	}
	if adv.MessageType != dhcpv6.MessageTypeAdvertise {
		return nil, fmt.Errorf("the passed ADVERTISE must have ADVERTISE type set")
	}
	// build REQUEST from ADVERTISE
	req, err := dhcpv6.NewMessage()
	if err != nil {
		return nil, err
	}
	req.MessageType = dhcpv6.MessageTypeRequest
	// add Client ID
	cid := adv.GetOneOption(dhcpv6.OptionClientID)
	if cid == nil {
		return nil, fmt.Errorf("client ID cannot be nil in ADVERTISE when building REQUEST")
	}
	req.AddOption(cid)
	// add Server ID
	sid := adv.GetOneOption(dhcpv6.OptionServerID)
	if sid == nil {
		return nil, fmt.Errorf("server ID cannot be nil in ADVERTISE when building REQUEST")
	}
	req.AddOption(sid)
	// add Elapsed Time
	req.AddOption(dhcpv6.OptElapsedTime(0))
	// add IA_NA
	if iana := adv.Options.OneIANA(); iana != nil {
		req.AddOption(iana)
	}
	// if iana == nil {
	// 	return nil, fmt.Errorf("IA_NA cannot be nil in ADVERTISE when building REQUEST")
	// }
	// req.AddOption(iana)
	// add IA_PD
	if iaPd := adv.GetOneOption(dhcpv6.OptionIAPD); iaPd != nil {
		req.AddOption(iaPd)
	}
	req.AddOption(dhcpv6.OptRequestedOption(
		dhcpv6.OptionDNSRecursiveNameServer,
		dhcpv6.OptionDomainSearchList,
	))
	// add OPTION_VENDOR_CLASS, only if present in the original request
	vClass := adv.GetOneOption(dhcpv6.OptionVendorClass)
	if vClass != nil {
		req.AddOption(vClass)
	}

	// apply modifiers
	for _, mod := range modifiers {
		mod(req)
	}
	return req, nil
}
