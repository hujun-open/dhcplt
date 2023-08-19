// dhcplt
package main

import (
	"context"
	"os/signal"
	"sync"
	"syscall"

	// "encoding/json"

	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"

	// "runtime/debug"

	"time"

	"github.com/hujun-open/dhcplt/common"
	"github.com/hujun-open/shouchan"

	mv "github.com/RobinUS2/golang-moving-average"
	"github.com/hujun-open/etherconn"
)

const (
	BBFEnterpriseNumber        = 3561
	EthernetTypeIPv4    uint16 = 0x0800
	EthernetTypeIPv6    uint16 = 0x86DD
)

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
	switch setup.Driver {
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
	return nil, fmt.Errorf("unknown eng type %v", setup.Driver)
}

const (
	//NOTE: without (xxx and vlan), then double vlan case won't work
	// bpfFilter = "(udp or (udp and vlan)) or (icmp6 or (icmp6 and vlan))"

	//bpfFilter = "(ip6 or (ip6 and vlan)) or (ip or (ip and vlan))"
	// bpfFilter = "ip or ip6"
	bpfFilter = ""
	ENG_AFPKT = etherconn.RelayTypeAFP
	ENG_XDP   = etherconn.RelayTypeXDP
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
	cnf, err := shouchan.NewSConf(newDefaultConf(), "dhcplt",
		"a DHCP load tester", shouchan.WithDefaultConfigFilePath[*testSetup]("dhcplt.conf"))
	if err != nil {
		panic(err)
	}
	cnf.ReadwithCMDLine()
	setup := cnf.GetConf()
	// fmt.Printf("%+v\n", setup)
	err = setup.init()
	if err != nil {
		log.Fatalf("invalid setup, %v", err)
	}
	if setup.Profiling {
		runtime.SetBlockProfileRate(1000000000)
		go func() {
			log.Println(http.ListenAndServe("0.0.0.0:6060", nil))
		}()

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
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go handleCtrlC(c, cancelf)
	wg.Wait()
	if setup.Profiling {
		ch := make(chan bool)
		<-ch
	}
	fmt.Println("done.")
}
