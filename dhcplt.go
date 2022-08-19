// dhcplt
package main

import (
	"context"
	"os/signal"
	"sync"
	"syscall"

	// "encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"

	// "runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/hujun-open/dhcplt/common"

	mv "github.com/RobinUS2/golang-moving-average"
	"github.com/hujun-open/etherconn"
	"github.com/insomniacslk/dhcp/dhcpv4"
)

const (
	BBFEnterpriseNumber        = 3561
	EthernetTypeIPv4    uint16 = 0x0800
	EthernetTypeIPv6    uint16 = 0x86DD
)

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

type resultSummary struct {
	Total          int
	Success        int
	Failed         int
	Released       int
	LessThanSecond int
	Shortest       time.Duration
	Longest        time.Duration
	TotalTime      time.Duration
	AvgSuccessTime *mv.MovingAverage
	setup          *testSetup
}

func newResultSummary(s *testSetup) *resultSummary {
	return &resultSummary{
		AvgSuccessTime: mv.New(5),
		setup:          s,
		Shortest:       maxDuration,
		Longest:        time.Duration(0),
	}
}

func (rs resultSummary) String() string {
	r := "Result Summary\n"
	r += fmt.Sprintf("total trans: %d\n", rs.Total)
	r += fmt.Sprintf("Success dial:%d\n", rs.Success)
	r += fmt.Sprintf("Success release:%d\n", rs.Released)
	r += fmt.Sprintf("Failed trans:%d\n", rs.Failed)
	r += fmt.Sprintf("Duration:%v\n", rs.TotalTime)
	r += fmt.Sprintf("Interval:%v\n", rs.setup.Interval)
	avgSuccess := time.Duration(rs.AvgSuccessTime.Avg())
	r += fmt.Sprintf("Setup rate:%v\n", float64(rs.Success)/(float64(rs.TotalTime)/float64(time.Second)))
	r += fmt.Sprintf("Fastest dial success:%v\n", rs.Shortest)
	r += fmt.Sprintf("dial Success within a second:%v\n", rs.LessThanSecond)
	r += fmt.Sprintf("Slowest dial success:%v\n", rs.Longest)
	r += fmt.Sprintf("Avg dial success time:%v\n", avgSuccess)
	return r
}

const maxDuration = time.Duration(int64(^uint64(0) >> 1))

func createPktRelay(setup *testSetup) (etherconn.PacketRelay, error) {
	switch setup.ENG {
	case ENG_AFPKT:
		relay, err := etherconn.NewRawSocketRelay(context.Background(),
			setup.Ifname, etherconn.WithBPFFilter(bpfFilter),
			etherconn.WithDebug(setup.Debug),
			etherconn.WithDefaultReceival(false),
			etherconn.WithSendChanDepth(10240),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create afpkt relay for if %v, %v", setup.Ifname, err)
		}
		return relay, nil
	case ENG_XDP:
		relay, err := etherconn.NewXDPRelay(context.Background(),
			setup.Ifname, etherconn.WithXDPDebug(setup.Debug),
			etherconn.WithXDPDefaultReceival(false),
			etherconn.WithXDPSendChanDepth(10240),
			etherconn.WithXDPUMEMNumOfTrunk(65536),
			etherconn.WithXDPEtherTypes([]uint16{EthernetTypeIPv4, EthernetTypeIPv6}),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create xdp relay for if %v, %v", setup.Ifname, err)
		}
		return relay, nil
	}
	return nil, fmt.Errorf("unknown eng type %v", setup.ENG)
}

const (
	//NOTE: without (xxx and vlan), then double vlan case won't work
	// bpfFilter = "(udp or (udp and vlan)) or (icmp6 or (icmp6 and vlan))"

	//bpfFilter = "(ip6 or (ip6 and vlan)) or (ip or (ip and vlan))"
	// bpfFilter = "ip or ip6"
	bpfFilter = ""
	ENG_AFPKT = "afpkt"
	ENG_XDP   = "xdp"
)

var VERSION string

func handleCtrlC(c chan os.Signal, cf context.CancelFunc) {
	<-c
	fmt.Println("\n\rstopping...")
	cf()
}

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
	cid := flag.String("cid", "", "circuit-id")
	rid := flag.String("rid", "", "remote-id")
	clientid := flag.String("clntid", "", "Client Identifier")
	vclass := flag.String("vc", "", "vendor class")
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
	isV4 := flag.Bool("v4", true, "enable/disable DHCPv4 client")
	isV6 := flag.Bool("v6", false, "enable/disable DHCPv6 client")
	v6Mtype := flag.String("v6m", "auto", "v6 message type, auto|relay|solicit")
	sendRS := flag.Bool("sendrs", false, "send RS and expect RA before dhcpv6")
	needNA := flag.Bool("iana", true, "request IANA")
	needPD := flag.Bool("iapd", false, "request IAPD")
	engine := flag.String("eng", ENG_AFPKT, fmt.Sprintf("packet forward engine, %v|%v", ENG_AFPKT, ENG_XDP))
	flapNum := flag.Int("flap", -1, "number of client flapping")
	minFlapInterval := flag.Duration("minflapint", defaultMinFlapInt, "minimal flapping interval")
	maxFlapInterval := flag.Duration("maxflapint", defualtMaxFlapInt, "max flapping interval")
	flapStaydown := flag.Duration("flapstaydown", defaultFlapStayDown, "duration of flapping client stay down before reconnect")
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
	if !*isV4 && !*isV6 {
		fmt.Println("both DHCPv4 and DHCPv6 are disabled, nothing to do, quit.")
		return
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
		*isV4,
		*isV6,
		*v6Mtype,
		*needNA,
		*needPD,
		*sendRS,
		*save,
		false,
		*engine,
		*flapNum,
		*minFlapInterval,
		*maxFlapInterval,
		*flapStaydown,
	)
	if err != nil {
		log.Fatalf("invalid parameter,%v", err)
	}
	if setup.Debug {
		common.Logger = log.New(os.Stderr, "", log.Ldate|log.Ltime)
	}
	sch, err := NewSched(setup)
	if err != nil {
		common.MyLog("failed to create sched, %v", err)
		return
	}
	ctx, cancelf := context.WithCancel(context.Background())
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go sch.run(ctx, wg)
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go handleCtrlC(c, cancelf)
	wg.Wait()
	if *profiling {
		ch := make(chan bool)
		<-ch
	}
	fmt.Println("done.")
}
