package main

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/insomniacslk/dhcp/iana"
)

func TestActionTypeRoundTrip(t *testing.T) {
	for _, act := range []actionType{actionDORA, actionRelease, actionRenew, actionRebind} {
		txt, err := act.MarshalText()
		if err != nil {
			t.Fatalf("MarshalText(%d) error: %v", act, err)
		}
		var got actionType
		if err := got.UnmarshalText(txt); err != nil {
			t.Fatalf("UnmarshalText(%q) error: %v", txt, err)
		}
		if got != act {
			t.Errorf("round trip = %d, want %d", got, act)
		}
	}
}

func TestActionTypeUnknown(t *testing.T) {
	var act actionType
	if err := act.UnmarshalText([]byte("not-an-action")); err == nil {
		t.Error("UnmarshalText(unknown) = nil error, want error")
	}
	if _, err := actionType(42).MarshalText(); err == nil {
		t.Error("MarshalText(unknown) = nil error, want error")
	}
}

func TestActionTypeString(t *testing.T) {
	cases := map[actionType]string{
		actionDORA:    "dora",
		actionRelease: "release",
		actionRenew:   "renew",
		actionRebind:  "rebind",
	}
	for act, want := range cases {
		if got := act.String(); got != want {
			t.Errorf("actionType(%d).String() = %q, want %q", act, got, want)
		}
	}
}

func TestGetIAIDviaInt(t *testing.T) {
	if got, want := getIAIDviaInt(0x01020304), [4]byte{1, 2, 3, 4}; got != want {
		t.Errorf("getIAIDviaInt = %v, want %v", got, want)
	}
}

func TestCollectResults(t *testing.T) {
	setup := newDefaultConf()
	sch := &Sched{
		summary:      newResultSummary(setup),
		dialResultCh: make(chan *dialResult, 8),
	}
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go sch.collectResults(wg)

	base := time.Now()
	sch.dialResultCh <- &dialResult{
		action:     actionDORA,
		ExecResult: resultSuccess,
		StartTime:  base,
		FinishTime: base.Add(200 * time.Millisecond),
	}
	sch.dialResultCh <- &dialResult{
		action:     actionDORA,
		ExecResult: resultFailure,
		StartTime:  base,
		FinishTime: base.Add(2 * time.Second),
	}
	sch.dialResultCh <- &dialResult{
		action:     actionRenew,
		ExecResult: resultSuccess,
		StartTime:  base,
		FinishTime: base.Add(10 * time.Millisecond),
	}
	close(sch.dialResultCh)
	wg.Wait()

	if sch.summary.Total != 3 {
		t.Errorf("Total = %d, want 3", sch.summary.Total)
	}
	if sch.summary.Success != 1 {
		t.Errorf("Success = %d, want 1", sch.summary.Success)
	}
	if sch.summary.Failed != 1 {
		t.Errorf("Failed = %d, want 1", sch.summary.Failed)
	}
	if sch.summary.Renewed != 1 {
		t.Errorf("Renewed = %d, want 1", sch.summary.Renewed)
	}
	if sch.summary.Released != 0 {
		t.Errorf("Released = %d, want 0", sch.summary.Released)
	}
	if sch.summary.LessThanSecond != 1 {
		t.Errorf("LessThanSecond = %d, want 1", sch.summary.LessThanSecond)
	}
	if sch.summary.Shortest != 200*time.Millisecond {
		t.Errorf("Shortest = %v, want 200ms", sch.summary.Shortest)
	}
	if sch.summary.Longest != 200*time.Millisecond {
		t.Errorf("Longest = %v, want 200ms", sch.summary.Longest)
	}
	// TotalTime spans the earliest StartTime to the latest FinishTime.
	if sch.summary.TotalTime != 2*time.Second {
		t.Errorf("TotalTime = %v, want 2s", sch.summary.TotalTime)
	}
	if sch.summary.AvgSuccessTime.Avg() <= 0 {
		t.Errorf("AvgSuccessTime = %v, want > 0", sch.summary.AvgSuccessTime.Avg())
	}
}

func TestBuildSolicit(t *testing.T) {
	mac := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0x11, 0x22, 0x33}
	setup := newDefaultConf()
	setup.DORA.NeedNA = true
	setup.DORA.NeedPD = true
	ccfg := clientConfig{Mac: mac, setup: setup}

	msg, err := buildSolicit(ccfg)
	if err != nil {
		t.Fatalf("buildSolicit error: %v", err)
	}
	if msg.MessageType != dhcpv6.MessageTypeSolicit {
		t.Errorf("MessageType = %v, want SOLICIT", msg.MessageType)
	}
	if msg.GetOneOption(dhcpv6.OptionClientID) == nil {
		t.Error("solicit message is missing client-id")
	}
	if msg.GetOneOption(dhcpv6.OptionIANA) == nil {
		t.Error("solicit message is missing IA_NA")
	}
	if msg.GetOneOption(dhcpv6.OptionIAPD) == nil {
		t.Error("solicit message is missing IA_PD")
	}

	setup.DORA.NeedNA = false
	setup.DORA.NeedPD = false
	msg, err = buildSolicit(ccfg)
	if err != nil {
		t.Fatalf("buildSolicit error: %v", err)
	}
	if msg.GetOneOption(dhcpv6.OptionIANA) != nil {
		t.Error("solicit message has IA_NA but NeedNA is false")
	}
	if msg.GetOneOption(dhcpv6.OptionIAPD) != nil {
		t.Error("solicit message has IA_PD but NeedPD is false")
	}
}

func TestNewRequestFromAdv(t *testing.T) {
	if _, err := NewRequestFromAdv(nil); err == nil {
		t.Error("NewRequestFromAdv(nil) = nil error, want error")
	}

	wrongType, err := dhcpv6.NewMessage()
	if err != nil {
		t.Fatalf("dhcpv6.NewMessage error: %v", err)
	}
	if _, err := NewRequestFromAdv(wrongType); err == nil {
		t.Error("NewRequestFromAdv(non-advertise) = nil error, want error")
	}

	// Advertise without client-id should fail.
	noCID, err := dhcpv6.NewMessage()
	if err != nil {
		t.Fatalf("dhcpv6.NewMessage error: %v", err)
	}
	noCID.MessageType = dhcpv6.MessageTypeAdvertise
	if _, err := NewRequestFromAdv(noCID); err == nil {
		t.Error("NewRequestFromAdv(advertise without client-id) = nil error, want error")
	}

	mac := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0x11, 0x22, 0x33}
	adv, err := dhcpv6.NewMessage()
	if err != nil {
		t.Fatalf("dhcpv6.NewMessage error: %v", err)
	}
	adv.MessageType = dhcpv6.MessageTypeAdvertise
	adv.AddOption(dhcpv6.OptClientID(&dhcpv6.DUIDLLT{HWType: iana.HWTypeEthernet, LinkLayerAddr: mac}))
	adv.AddOption(dhcpv6.OptServerID(&dhcpv6.DUIDLLT{HWType: iana.HWTypeEthernet, LinkLayerAddr: mac}))

	req, err := NewRequestFromAdv(adv)
	if err != nil {
		t.Fatalf("NewRequestFromAdv error: %v", err)
	}
	if req.MessageType != dhcpv6.MessageTypeRequest {
		t.Errorf("MessageType = %v, want REQUEST", req.MessageType)
	}
	if req.GetOneOption(dhcpv6.OptionClientID) == nil {
		t.Error("request is missing client-id")
	}
	if req.GetOneOption(dhcpv6.OptionServerID) == nil {
		t.Error("request is missing server-id")
	}
}
