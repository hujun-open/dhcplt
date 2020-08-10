// dhcplt
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/big"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hujun-open/etherconn"
	"github.com/hujun-open/myaddr"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
)

//DHCPv4 lease
type v4Lease struct {
	Lease     *nclient4.Lease
	VLANList  etherconn.VLANs
	IDOptions dhcpv4.Options
}

func newV4Lease() *v4Lease {
	r := new(v4Lease)
	r.VLANList = etherconn.VLANs{}
	r.IDOptions = make(dhcpv4.Options)
	return r
}

func getLeaseDir() string {
	tempRoot := os.TempDir()
	leaseDIR := filepath.Join(tempRoot, "dhcplt_leases")
	if _, err := os.Stat(leaseDIR); os.IsNotExist(err) {
		os.Mkdir(leaseDIR, 0755)
	}
	return leaseDIR
}

type testSetup struct {
	Ifname        string
	NumOfClients  uint
	StartMAC      net.HardwareAddr
	MacStep       uint
	StartVLANs    etherconn.VLANs
	VLANStep      uint
	ExcludedVLANs []uint16
	Interval      time.Duration
	V4Options     []dhcpv4.Option
	Debug         bool
	SaveLease     bool
	Retry         uint
	Timeout       time.Duration
	//following are template str, $ID will be replaced by client id
	RID    string
	CID    string
	ClntID string
}

func (setup *testSetup) excluded(vids []uint16) bool {
	for _, vid := range vids {
		for _, extv := range setup.ExcludedVLANs {
			if extv == vid {
				return true
			}
		}
	}
	return false
}

func parseCustomOptionStr(coptStr string) (dhcpv4.Option, error) {
	strList := strings.SplitN(coptStr, ":", 2)
	if len(strList) < 2 {
		return dhcpv4.Option{}, fmt.Errorf("invalid custom option %v", coptStr)
	}
	var oid int
	var err error
	if oid, err = strconv.Atoi(strList[0]); err != nil {
		return dhcpv4.Option{}, fmt.Errorf("%v is not a number", strList[0])
	}
	return dhcpv4.Option{
		Code: dhcpv4.GenericOptionCode(oid),
		Value: dhcpv4.OptionGeneric{
			Data: []byte(strList[1]),
		},
	}, nil

}

func newSetupviaFlags(
	Ifname string,
	NumOfClients uint,
	retry uint,
	timeout time.Duration,
	StartMAC string,
	MacStep uint,
	vlan int,
	vlanetype uint,
	svlan int,
	svlanetype uint,
	VLANStep uint,
	excludevlanid string,
	Interval time.Duration,
	Debug bool,
	rid, cid, clntid, vclass, customop string,
	SaveLease bool,
) (*testSetup, error) {
	var r testSetup
	if Ifname == "" {
		return nil, fmt.Errorf("interface name can't be empty")
	}
	if NumOfClients == 0 {
		return nil, fmt.Errorf("number of clients can't be zero")
	}
	iff, err := net.InterfaceByName(Ifname)
	if err != nil {
		return nil, fmt.Errorf("can't find interface %v,%w", Ifname, err)
	}
	r.Ifname = Ifname
	if NumOfClients == 0 {
		return nil, fmt.Errorf("number of client is 0")
	}
	r.NumOfClients = NumOfClients
	if StartMAC == "" {
		r.StartMAC = iff.HardwareAddr
	} else {
		r.StartMAC, err = net.ParseMAC(StartMAC)
		if err != nil {
			return nil, err
		}
	}
	r.MacStep = MacStep
	r.Retry = retry
	r.Timeout = timeout
	chkVIDFunc := func(vid int) bool {
		if vid < 0 || vid > 4095 {
			return false
		}
		return true
	}

	newvlanFunc := func(id int, etype uint) *etherconn.VLAN {
		if id >= 0 {
			return &etherconn.VLAN{
				ID:        uint16(id),
				EtherType: uint16(etype),
			}
		}
		return nil
	}
	if chkVIDFunc(vlan) {
		r.StartVLANs = etherconn.VLANs{}
		if v := newvlanFunc(vlan, vlanetype); v != nil {
			r.StartVLANs = append(r.StartVLANs, v)
		}
		if chkVIDFunc(svlan) {
			if v := newvlanFunc(svlan, svlanetype); v != nil {
				r.StartVLANs = append(etherconn.VLANs{v}, r.StartVLANs...)
			}
		}

	} else {
		if chkVIDFunc(svlan) {
			return nil, fmt.Errorf("spcifying svlan also require specifying a valid vlan")
		}
	}

	r.VLANStep = VLANStep
	strToExtListFunc := func(exts string) []uint16 {
		vidstrList := strings.Split(exts, ",")
		r := []uint16{}
		for _, vidstr := range vidstrList {
			vid, err := strconv.Atoi(vidstr)
			if err == nil {
				if vid >= 0 && vid <= 4095 {
					r = append(r, uint16(vid))
				}
			}
		}
		return r
	}
	r.ExcludedVLANs = strToExtListFunc(excludevlanid)

	r.Interval = Interval
	r.Debug = Debug
	r.V4Options = []dhcpv4.Option{}
	r.RID = rid
	r.CID = cid
	r.ClntID = clntid

	if vclass != "" {
		r.V4Options = append(r.V4Options, dhcpv4.OptClassIdentifier(vclass))
	}
	if customop != "" {
		cop, err := parseCustomOptionStr(customop)
		if err != nil {
			return nil, err
		}
		r.V4Options = append(r.V4Options, cop)
	}
	r.SaveLease = SaveLease
	return &r, nil
}

type clientConfig struct {
	Mac       net.HardwareAddr
	VLANs     etherconn.VLANs
	V4Options []dhcpv4.Option
	setup     *testSetup
}

func genClientConfigurations(setup *testSetup) ([]clientConfig, error) {
	r := []clientConfig{}
	clntmac := setup.StartMAC
	vlans := setup.StartVLANs
	var err error
	for i := 0; i < int(setup.NumOfClients); i++ {
		ccfg := clientConfig{}
		ccfg.setup = setup
		//assign mac
		ccfg.Mac = clntmac
		if i > 0 {
			ccfg.Mac, err = myaddr.IncMACAddr(clntmac, big.NewInt(int64(setup.MacStep)))
			if err != nil {
				return []clientConfig{}, fmt.Errorf("failed to generate mac address,%v", err)
			}

		}
		clntmac = ccfg.Mac
		//assign vlan
		ccfg.VLANs = vlans.Clone()

		incvidFunc := func(ids, excludes []uint16, step int) ([]uint16, error) {
			newids := ids
			for i := 0; i < 10; i++ {
				newids, err = myaddr.IncreaseVLANIDs(newids, step)
				if err != nil {
					return []uint16{}, err
				}
				excluded := false
			L1:
				for _, v := range newids {
					for _, exc := range excludes {
						if v == exc {
							excluded = true
							break L1
						}
					}
				}
				if !excluded {
					return newids, nil
				}
			}
			return []uint16{}, fmt.Errorf("you shouldn't see this")
		}

		if (len(vlans) > 0 && i > 0) || setup.excluded(vlans.IDs()) {
			rids, err := incvidFunc(vlans.IDs(), setup.ExcludedVLANs, int(setup.VLANStep))
			if err != nil {
				return []clientConfig{}, fmt.Errorf("failed to generate vlan id,%v", err)
			}
			err = ccfg.VLANs.SetIDs(rids)
			if err != nil {
				return []clientConfig{}, fmt.Errorf("failed to generate and apply vlan id,%v", err)
			}
		}
		vlans = ccfg.VLANs
		//options
		ccfg.V4Options = []dhcpv4.Option{}
		for _, o := range setup.V4Options {
			ccfg.V4Options = append(ccfg.V4Options, o)
		}
		genStrFunc := func(s string, id int) string {
			const varname = "@ID"
			if strings.Contains(s, varname) {
				ss := strings.ReplaceAll(s, varname, "%d")
				return fmt.Sprintf(ss, id)
			}
			return s
		}

		if setup.RID != "" || setup.CID != "" {
			subOptList := []dhcpv4.Option{}
			if setup.RID != "" {
				subOptList = append(subOptList, dhcpv4.OptGeneric(dhcpv4.AgentRemoteIDSubOption, []byte(genStrFunc(setup.RID, i))))
			}
			if setup.CID != "" {
				subOptList = append(subOptList, dhcpv4.OptGeneric(dhcpv4.AgentCircuitIDSubOption, []byte(genStrFunc(setup.CID, i))))
			}

			ccfg.V4Options = append(ccfg.V4Options, dhcpv4.OptRelayAgentInfo(subOptList...))
		}
		if setup.ClntID != "" {
			myLog("gened clnt id is %v", genStrFunc(setup.ClntID, i))
			ccfg.V4Options = append(ccfg.V4Options, dhcpv4.OptClientIdentifier([]byte(genStrFunc(setup.ClntID, i))))
		}
		r = append(r, ccfg)
	}
	return r, nil
}

//release release all leases recorded for the specified interface
func release(setup *testSetup) {
	sdir := getLeaseDir()
	fpath := filepath.Join(sdir, setup.Ifname)
	var leaseList []*v4Lease
	jsb, err := ioutil.ReadFile(fpath)
	if err != nil {
		log.Fatal(err)
	}
	err = json.Unmarshal(jsb, &leaseList)
	if err != nil {
		log.Fatal(err)
	}

	relay, err := etherconn.NewRawSocketRelay(context.Background(), setup.Ifname, etherconn.WithDebug(setup.Debug))
	if err != nil {
		log.Fatalf("failed to create raw socket for if %v", setup.Ifname)
	}
	defer relay.Stop()
	realeaeFunc := func(l *v4Lease, wg *sync.WaitGroup) {
		defer wg.Done()
		econn := etherconn.NewEtherConn(l.Lease.ACK.ClientHWAddr,
			relay, etherconn.WithVLANs(l.VLANList))
		rudpconn, err := etherconn.NewRUDPConn(
			myaddr.GenConnectionAddrStr("", l.Lease.ACK.YourIPAddr, 68), econn)
		if err != nil {
			myLog("failed to create raw udp conn for %v,%v", l.Lease.ACK.ClientHWAddr, err)
			return
		}
		clntModList := []nclient4.ClientOpt{nclient4.WithHWAddr(l.Lease.ACK.ClientHWAddr)}
		if setup.Debug {
			clntModList = append(clntModList, nclient4.WithDebugLogger())
		}
		clnt, err := nclient4.NewWithConn(rudpconn, l.Lease.ACK.ClientHWAddr, clntModList...)
		if err != nil {
			myLog("failed to create dhcpv4 client for %v,%v", l.Lease.ACK.ClientHWAddr, err)
			return
		}
		modList := []dhcpv4.Modifier{}
		for t := range l.IDOptions {
			modList = append(modList,
				dhcpv4.WithOption(dhcpv4.OptGeneric(dhcpv4.GenericOptionCode(t),
					l.IDOptions.Get(dhcpv4.GenericOptionCode(t)))))
		}
		for i := 0; i < 3; i++ {
			err = clnt.Release(l.Lease, modList...)
			if err != nil {
				myLog("failed to send release for %v,%v", l.Lease.ACK.ClientHWAddr, err)
				continue
			}
		}

	}
	wg := new(sync.WaitGroup)
	for _, l := range leaseList {
		//create etherconn & rudpconn
		myLog("releaseing mac %v", l.Lease.ACK.ClientHWAddr)
		wg.Add(1)
		go realeaeFunc(l, wg)
		time.Sleep(setup.Interval)
	}
	wg.Wait()
	log.Print("done")

}

func saveLease(ch chan *v4Lease, ifname string, savewg *sync.WaitGroup) {
	defer savewg.Done()
	sdir := getLeaseDir()
	fpath := filepath.Join(sdir, ifname)
	leaseList := []*v4Lease{}
	for l := range ch {
		leaseList = append(leaseList, l)
	}
	rs, err := json.Marshal(leaseList)
	if err != nil {
		log.Fatal(err)
	}
	err = ioutil.WriteFile(fpath, rs, 0644)
	if err != nil {
		log.Fatal(err)
	}
	return
}

func doDORA(ccfg clientConfig, relay *etherconn.RawSocketRelay,
	wg *sync.WaitGroup, saveleasechan chan *v4Lease, debug bool,
	collectchan chan *testResult) {
	defer wg.Done()
	result := new(testResult)
	result.ExecResult = resultFailure
	defer func() {
		result.L2EP = etherconn.NewL2EndpointFromMACVLAN(ccfg.Mac, ccfg.VLANs).GetKey()
		collectchan <- result
	}()

	myLog("doing DORA for %v with VLANs %v, on if %v", ccfg.Mac, ccfg.VLANs.String(), ccfg.setup.Ifname)
	result.StartTime = time.Now()
	//create etherconn & rudpconn
	econn := etherconn.NewEtherConn(ccfg.Mac, relay, etherconn.WithVLANs(ccfg.VLANs))
	rudpconn, err := etherconn.NewRUDPConn("0.0.0.0:68", econn, etherconn.WithAcceptAny(true))
	if err != nil {
		//return nil, fmt.Errorf()
		myLog("failed to create raw udp conn for %v,%v", ccfg.Mac, err)
		return
	}
	mylease := newV4Lease()
	clntModList := []nclient4.ClientOpt{
		nclient4.WithRetry(int(ccfg.setup.Retry)),
		nclient4.WithTimeout(ccfg.setup.Timeout),
	}
	if debug {
		clntModList = append(clntModList, nclient4.WithDebugLogger())
	}
	clntModList = append(clntModList, nclient4.WithHWAddr(ccfg.Mac))
	clnt, err := nclient4.NewWithConn(rudpconn, ccfg.Mac, clntModList...)
	if err != nil {
		myLog("failed to create dhcpv4 client for %v,%v", ccfg.Mac, err)
		return
	}
	dhcpModList := []dhcpv4.Modifier{}
	for _, op := range ccfg.V4Options {
		dhcpModList = append(dhcpModList, dhcpv4.WithOption(op))
	}
	result.StartTime = time.Now()
	lease, err := clnt.Request(context.Background(), dhcpModList...)
	if err != nil {
		myLog("failed complete DORA for %v,%v", ccfg.Mac, err)
		return
	}

	result.ExecResult = resultSuccess
	result.FinishTime = time.Now()
	if ccfg.setup.SaveLease {
		mylease.Lease = lease
		mylease.VLANList = ccfg.VLANs
		saveleasechan <- mylease
	}
	return
}

type execResult int

const (
	resultSuccess execResult = iota
	resultFailure
)

func (er execResult) String() string {
	switch er {
	case resultSuccess:
		return "success"
	case resultFailure:
		return "failed"
	default:
		return "unknow result"
	}
}

type testResult struct {
	ExecResult execResult
	L2EP       etherconn.L2EndpointKey
	StartTime  time.Time
	FinishTime time.Time
}

func collectResults(rchan chan *testResult, output chan []*testResult) {
	finalr := []*testResult{}
	suc := 0
	fail := 0
	for r := range rchan {
		finalr = append(finalr, r)
		if r.ExecResult == resultSuccess {
			suc++
		} else {
			fail++
		}
		fmt.Printf("\rsucced: %7d\t  failed %7d", suc, fail)
	}
	fmt.Println("\r                                                                                 ")
	output <- finalr
}

type resultSummary struct {
	Total            int
	Success          int
	Failed           int
	LessThanSecond   int
	Shortest         time.Duration
	Longest          time.Duration
	SuccessTotalTime time.Duration
	TotalTime        time.Duration
	AvgSuccessTime   time.Duration
	setup            *testSetup
}

func (rs resultSummary) String() string {
	r := "Result Summary\n"
	r += fmt.Sprintf("total: %d\n", rs.Total)
	r += fmt.Sprintf("Success:%d\n", rs.Success)
	r += fmt.Sprintf("Failed:%d\n", rs.Failed)
	r += fmt.Sprintf("Duration:%v\n", rs.TotalTime)
	r += fmt.Sprintf("Interval:%v\n", rs.setup.Interval)
	totalSuccessSeconds := (float64(rs.SuccessTotalTime) / float64(time.Second))
	if totalSuccessSeconds == 0 {
		r += fmt.Sprintln(`Setup rate: n\a`)
	} else {
		r += fmt.Sprintf("Setup rate:%v\n", float64(rs.Success)/totalSuccessSeconds)
	}

	r += fmt.Sprintf("Fastest success:%v\n", rs.Shortest)
	r += fmt.Sprintf("Success within a second:%v\n", rs.LessThanSecond)
	r += fmt.Sprintf("Slowest success:%v\n", rs.Longest)
	r += fmt.Sprintf("Avg success time:%v\n", rs.AvgSuccessTime)
	return r
}

const maxDuration = time.Duration(int64(^uint64(0) >> 1))

func analyzeResults(results []*testResult, setup *testSetup) *resultSummary {

	summary := new(resultSummary)
	summary.setup = setup
	totalSuccessTime := time.Duration(0)
	summary.Shortest = maxDuration
	summary.Longest = time.Duration(0)
	var beginTime, endTime time.Time
	beginTime = time.Now()
	for _, r := range results {
		completeTime := r.FinishTime.Sub(r.StartTime)
		if completeTime < 0 {
			completeTime = 0
		}
		switch r.ExecResult {
		case resultSuccess:
			summary.Success++
			if completeTime < time.Second {
				summary.LessThanSecond++
			}
			if completeTime > summary.Longest {
				summary.Longest = completeTime
			}
			if completeTime < summary.Shortest {
				summary.Shortest = completeTime
			}
			totalSuccessTime += completeTime
		case resultFailure:
			summary.Failed++
		}
		if r.StartTime.Before(beginTime) {
			beginTime = r.StartTime
		}
		if r.ExecResult == resultSuccess {
			if r.FinishTime.After(endTime) {
				endTime = r.FinishTime
			}
		}

		summary.Total++
	}
	if summary.Success != 0 {
		summary.AvgSuccessTime = totalSuccessTime / time.Duration(summary.Success)
	} else {
		summary.AvgSuccessTime = 0
	}
	summary.SuccessTotalTime = endTime.Sub(beginTime)
	if summary.Shortest == maxDuration {
		summary.Shortest = 0
	}
	return summary
}

//DORA excute DHCPv4 DORA exchange according to setup
func DORA(setup *testSetup) *resultSummary {
	ccfgList, err := genClientConfigurations(setup)
	if err != nil {
		log.Fatalf("failed to generate per client config,%v", err)
	}
	//doing DORA
	relay, err := etherconn.NewRawSocketRelay(context.Background(), setup.Ifname, etherconn.WithDebug(setup.Debug))
	if err != nil {
		log.Fatalf("failed to create raw socket for if %v", setup.Ifname)
	}
	defer relay.Stop()
	savewg := new(sync.WaitGroup)
	saveLeaseChan := make(chan *v4Lease, 16)
	savewg.Add(1)
	go saveLease(saveLeaseChan, setup.Ifname, savewg)
	defer savewg.Wait()
	defer close(saveLeaseChan)
	wg := new(sync.WaitGroup)
	var i uint
	resultCh := make(chan *testResult, 16)
	resultOutput := make(chan []*testResult)
	go collectResults(resultCh, resultOutput)
	testStart := time.Now()
	for i = 0; i < setup.NumOfClients; i++ {
		wg.Add(1)
		go doDORA(ccfgList[i], relay, wg, saveLeaseChan, setup.Debug, resultCh)
		time.Sleep(setup.Interval)
	}
	wg.Wait()
	testDuration := time.Now().Sub(testStart)
	close(resultCh)
	allresults := <-resultOutput
	summary := analyzeResults(allresults, setup)
	summary.TotalTime = testDuration
	return summary

}

const (
	actDORA    = "dora"
	actRelease = "release"
)

func actHelpStr() string {
	return fmt.Sprintf("DHCP action,%v|%v", actDORA, actRelease)
}

var logger *log.Logger

func myLog(format string, a ...interface{}) {
	if logger == nil {
		return
	}
	msg := fmt.Sprintf(format, a...)
	_, fname, linenum, _ := runtime.Caller(1)
	logger.Print(fmt.Sprintf("%v:%v:%v", filepath.Base(fname), linenum, msg))
}

var VERSION string

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	intf := flag.String("i", "", "interface name")
	debug := flag.Bool("d", false, "enable debug output")
	mac := flag.String("mac", "", "mac address")
	clntnum := flag.Uint("n", 1, "number of clients")
	macstep := flag.Uint("macstep", 1, "mac address step")
	vlanstep := flag.Uint("vlanstep", 0, "VLAN Id step")
	excludevlanid := flag.String("excludedvlans", "", "excluded vlan IDs")
	cid := flag.String("cid", "", "option82 circuit-id")
	rid := flag.String("rid", "", "option82 remote-id")
	clientid := flag.String("clntid", "", "Client Identifier")
	vclass := flag.String("vc", "", "vendor class")
	action := flag.String("action", actDORA, actHelpStr())
	retry := flag.Uint("retry", 3, "number of DHCP request retry")
	timeout := flag.Duration("timeout", 5*time.Second, "DHCP request timeout")
	interval := flag.Duration("interval", time.Millisecond, "interval between launching client")
	vlanid := flag.Int("vlan", -1, "vlan tag")
	vlantype := flag.Uint("vlanetype", 0x8100, "vlan tag EtherType")
	svlanid := flag.Int("svlan", -1, "svlan tag")
	svlantype := flag.Uint("svlanetype", 0x8100, "svlan tag EtherType")
	profiling := flag.Bool("p", false, "enable profiling, only for dev use")
	save := flag.Bool("savelease", false, "save leases")
	customoption := flag.String("customoption", "", "add a custom option, id:value")
	ver := flag.Bool("v", false, "show version")
	flag.Parse()
	if *ver {
		if VERSION == "" {
			VERSION = "non-release build"
		}
		fmt.Printf("dhcplt, a DHCP load tester, %v, by Hu Jun\n", VERSION)
		return
	}
	if *profiling {
		runtime.SetBlockProfileRate(1000000000)
		go func() {
			log.Println(http.ListenAndServe("0.0.0.0:6060", nil))
		}()

	}
	var err error

	setup, err := newSetupviaFlags(
		*intf,
		*clntnum,
		*retry,
		*timeout,
		*mac,
		*macstep,
		*vlanid,
		*vlantype,
		*svlanid,
		*svlantype,
		*vlanstep,
		*excludevlanid,
		*interval,
		*debug,
		*rid, *cid, *clientid, *vclass, *customoption,
		*save,
	)
	if err != nil {
		log.Fatalf("invalid parameter,%v", err)
	}
	if setup.Debug {
		logger = log.New(os.Stderr, "", log.Ldate|log.Ltime)
	}
	switch *action {
	case actDORA:
		summary := DORA(setup)
		fmt.Println(summary)
	case actRelease:
		release(setup)
	default:
		fmt.Printf("unknown action %v\n", *action)
		return
	}

}
