package main

import (
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/hujun-open/etherconn"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv6"
)

func TestNewDefaultConf(t *testing.T) {
	d := newDefaultConf()

	if d.DORA.NumOfClients != 1 {
		t.Errorf("DORA.NumOfClients = %d, want 1", d.DORA.NumOfClients)
	}
	if d.DORA.MacStep != 1 {
		t.Errorf("DORA.MacStep = %d, want 1", d.DORA.MacStep)
	}
	if d.DORA.VLANStep != 1 {
		t.Errorf("DORA.VLANStep = %d, want 1", d.DORA.VLANStep)
	}
	if d.DORA.VLANEType != etherconn.DefaultVLANEtype {
		t.Errorf("DORA.VLANEType = %#x, want %#x", d.DORA.VLANEType, etherconn.DefaultVLANEtype)
	}
	if !d.DORA.NeedNA {
		t.Error("DORA.NeedNA = false, want true")
	}
	if d.DORA.NeedPD {
		t.Error("DORA.NeedPD = true, want false")
	}
	if d.DORA.Flapping == nil {
		t.Fatal("DORA.Flapping = nil, want non-nil")
	}
	if !d.EnableV4 || d.EnableV6 {
		t.Errorf("EnableV4/EnableV6 = %v/%v, want true/false", d.EnableV4, d.EnableV6)
	}
	if d.Interval != time.Second {
		t.Errorf("Interval = %v, want %v", d.Interval, time.Second)
	}
	if d.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want %v", d.Timeout, 5*time.Second)
	}
	if d.Retry != 1 {
		t.Errorf("Retry = %d, want 1", d.Retry)
	}
	if d.Driver != etherconn.RelayTypeAFP {
		t.Errorf("Driver = %v, want %v", d.Driver, etherconn.RelayTypeAFP)
	}
	if d.SourceV4Port != dhcpv4.ClientPort {
		t.Errorf("SourceV4Port = %d, want %d", d.SourceV4Port, dhcpv4.ClientPort)
	}
	if d.SourceV6Port != dhcpv6.DefaultClientPort {
		t.Errorf("SourceV6Port = %d, want %d", d.SourceV6Port, dhcpv6.DefaultClientPort)
	}
	if d.LeaseFile != "dhcplt.lease" {
		t.Errorf("LeaseFile = %q, want %q", d.LeaseFile, "dhcplt.lease")
	}
	if d.GiAddr != netip.MustParseAddr("0.0.0.0") {
		t.Errorf("GiAddr = %v, want 0.0.0.0", d.GiAddr)
	}
	if d.SourceV4Addr != netip.MustParseAddr("0.0.0.0") {
		t.Errorf("SourceV4Addr = %v, want 0.0.0.0", d.SourceV4Addr)
	}
	if d.SourceV6Addr != netip.MustParseAddr("::") {
		t.Errorf("SourceV6Addr = %v, want ::", d.SourceV6Addr)
	}
	if d.V6MsgType != dhcpv6.MessageTypeNone {
		t.Errorf("V6MsgType = %v, want MessageTypeNone", d.V6MsgType)
	}
}

// anyInterfaceName returns the name of an existing interface so that tests can
// get past the interface lookup inside testSetup.init.
func anyInterfaceName(t *testing.T) string {
	t.Helper()
	ifs, err := net.Interfaces()
	if err != nil {
		t.Skipf("cannot list network interfaces: %v", err)
	}
	if len(ifs) == 0 {
		t.Skip("no network interfaces available")
	}
	return ifs[0].Name
}

func TestSetupInitValidation(t *testing.T) {
	validIf := anyInterfaceName(t)

	cases := []struct {
		name    string
		mutate  func(*testSetup)
		wantErr string
	}{
		{
			name:    "empty ifname",
			mutate:  func(s *testSetup) { s.Ifname = "" },
			wantErr: "interface name can't be empty",
		},
		{
			name: "zero clients",
			mutate: func(s *testSetup) {
				s.Ifname = validIf
				s.DORA.NumOfClients = 0
			},
			wantErr: "number of clients can't be zero",
		},
		{
			name: "unknown ifname",
			mutate: func(s *testSetup) {
				s.Ifname = "dhcplt-does-not-exist"
			},
			wantErr: "can't find interface",
		},
		{
			name: "both stacks disabled",
			mutate: func(s *testSetup) {
				s.Ifname = validIf
				s.EnableV4 = false
				s.EnableV6 = false
			},
			wantErr: "both DHCPv4 and DHCPv6 are disabled",
		},
		{
			name: "zero source v4 port",
			mutate: func(s *testSetup) {
				s.Ifname = validIf
				s.EnableV4 = true
				s.SourceV4Port = 0
			},
			wantErr: "source v4 port can't be zero",
		},
		{
			name: "zero source v6 port",
			mutate: func(s *testSetup) {
				s.Ifname = validIf
				s.EnableV4 = false
				s.EnableV6 = true
				s.SourceV6Port = 0
			},
			wantErr: "source v6 port can't be zero",
		},
		{
			name: "invalid source v4 address",
			mutate: func(s *testSetup) {
				s.Ifname = validIf
				s.EnableV4 = true
				s.SourceV4Addr = netip.MustParseAddr("127.0.0.1")
			},
			wantErr: "source v4 address must be",
		},
		{
			name: "invalid giaddr",
			mutate: func(s *testSetup) {
				s.Ifname = validIf
				s.EnableV4 = true
				s.GiAddr = netip.MustParseAddr("127.0.0.1")
			},
			wantErr: "gi address must be",
		},
		{
			name: "invalid source v6 address",
			mutate: func(s *testSetup) {
				s.Ifname = validIf
				s.EnableV4 = false
				s.EnableV6 = true
				s.SourceV6Addr = netip.MustParseAddr("fe80::1")
			},
			wantErr: "source v6 address must be",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newDefaultConf()
			tc.mutate(s)
			err := s.init(actionDORA)
			if err == nil {
				t.Fatalf("init() = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("init() error = %q, want containing %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestParseD4CustomOptionStr(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		code    uint8
		data    string
		wantErr bool
	}{
		{name: "plain", in: "60:dhcplt", code: 60, data: "dhcplt"},
		{name: "value with colon", in: "61:a:b", code: 61, data: "a:b"},
		{name: "missing colon", in: "60dhcplt", wantErr: true},
		{name: "non numeric code", in: "xx:dhcplt", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opt, err := parseD4CustomOptionStr(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseD4CustomOptionStr(%q) = nil error, want error", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseD4CustomOptionStr(%q) unexpected error: %v", tc.in, err)
			}
			if got := opt.Code.Code(); got != tc.code {
				t.Errorf("option code = %d, want %d", got, tc.code)
			}
			gen, ok := opt.Value.(dhcpv4.OptionGeneric)
			if !ok {
				t.Fatalf("option value type = %T, want dhcpv4.OptionGeneric", opt.Value)
			}
			if string(gen.Data) != tc.data {
				t.Errorf("option data = %q, want %q", gen.Data, tc.data)
			}
		})
	}
}

func TestD4OptionEmptyRoundTrip(t *testing.T) {
	v, err := d4OptionFromStr("")
	if err != nil {
		t.Fatalf("d4OptionFromStr(\"\") error: %v", err)
	}
	opt, ok := v.(dhcpv4.Option)
	if !ok {
		t.Fatalf("d4OptionFromStr(\"\") type = %T, want dhcpv4.Option", v)
	}
	if opt.Code != nil {
		t.Errorf("empty option code = %v, want nil", opt.Code)
	}
	s, err := d4OptionToStr(opt)
	if err != nil {
		t.Fatalf("d4OptionToStr error: %v", err)
	}
	if s != "" {
		t.Errorf("d4OptionToStr(empty) = %q, want empty", s)
	}
}

func TestParseD6CustomOptionStr(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		code    dhcpv6.OptionCode
		data    string
		wantErr bool
	}{
		{name: "plain", in: "23:relayagent", code: 23, data: "relayagent"},
		{name: "value with colon", in: "18:a:b", code: 18, data: "a:b"},
		{name: "missing colon", in: "23relayagent", wantErr: true},
		{name: "non numeric code", in: "xx:relayagent", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opt, err := parseD6CustomOptionStr(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseD6CustomOptionStr(%q) = nil error, want error", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseD6CustomOptionStr(%q) unexpected error: %v", tc.in, err)
			}
			gen, ok := opt.(*dhcpv6.OptionGeneric)
			if !ok {
				t.Fatalf("option type = %T, want *dhcpv6.OptionGeneric", opt)
			}
			if gen.OptionCode != tc.code {
				t.Errorf("option code = %d, want %d", gen.OptionCode, tc.code)
			}
			if string(gen.OptionData) != tc.data {
				t.Errorf("option data = %q, want %q", gen.OptionData, tc.data)
			}
			// d6OptionToStr uses string(data), so it round-trips.
			s, err := d6OptionToStr(*gen)
			if err != nil {
				t.Fatalf("d6OptionToStr error: %v", err)
			}
			if want := tc.in; s != want {
				t.Errorf("d6OptionToStr round trip = %q, want %q", s, want)
			}
		})
	}
}

func TestD6OptionEmptyRoundTrip(t *testing.T) {
	v, err := d6OptionFromStr("")
	if err != nil {
		t.Fatalf("d6OptionFromStr(\"\") error: %v", err)
	}
	gen, ok := v.(dhcpv6.OptionGeneric)
	if !ok {
		t.Fatalf("d6OptionFromStr(\"\") type = %T, want dhcpv6.OptionGeneric", v)
	}
	if gen.OptionCode != 0 {
		t.Errorf("empty option code = %d, want 0", gen.OptionCode)
	}
	s, err := d6OptionToStr(gen)
	if err != nil {
		t.Fatalf("d6OptionToStr error: %v", err)
	}
	if s != "" {
		t.Errorf("d6OptionToStr(empty) = %q, want empty", s)
	}
}

func TestD6MsgTypeFromStr(t *testing.T) {
	cases := []struct {
		in      string
		want    dhcpv6.MessageType
		wantErr bool
	}{
		{in: "solicit", want: dhcpv6.MessageTypeSolicit},
		{in: "SOLICIT", want: dhcpv6.MessageTypeSolicit},
		{in: "relay", want: dhcpv6.MessageTypeRelayForward},
		{in: "auto", want: dhcpv6.MessageTypeNone},
		{in: "bogus", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := d6MsgTypeFromStr(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("d6MsgTypeFromStr(%q) = nil error, want error", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("d6MsgTypeFromStr(%q) unexpected error: %v", tc.in, err)
			}
			mt, ok := got.(dhcpv6.MessageType)
			if !ok {
				t.Fatalf("d6MsgTypeFromStr(%q) type = %T, want dhcpv6.MessageType", tc.in, got)
			}
			if mt != tc.want {
				t.Errorf("d6MsgTypeFromStr(%q) = %v, want %v", tc.in, mt, tc.want)
			}
		})
	}
}

func TestD6MsgTypeToStr(t *testing.T) {
	cases := []struct {
		in   dhcpv6.MessageType
		want string
	}{
		{in: dhcpv6.MessageTypeNone, want: "auto"},
		{in: dhcpv6.MessageTypeSolicit, want: "solicit"},
	}
	for _, tc := range cases {
		got, err := d6MsgTypeToStr(tc.in)
		if err != nil {
			t.Fatalf("d6MsgTypeToStr(%v) error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("d6MsgTypeToStr(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
