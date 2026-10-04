package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hujun-open/extyaml"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv6"
)

var registerExtYAMLOnce sync.Once

// registerExtYAML registers the extra YAML converters the config format relies
// on. It is idempotent so both the unit test and the e2e test can call it.
func registerExtYAML() {
	registerExtYAMLOnce.Do(func() {
		extyaml.RegisterExt[dhcpv4.Option](d4OptionToStr, d4OptionFromStr)
		extyaml.RegisterExt[dhcpv6.OptionGeneric](d6OptionToStr, d6OptionFromStr)
		extyaml.RegisterExt[dhcpv6.MessageType](d6MsgTypeToStr, d6MsgTypeFromStr)
	})
}

// loadTestSetupFromFile loads a dhcplt YAML config into a testSetup seeded with
// defaults.
func loadTestSetupFromFile(t *testing.T, path string) *testSetup {
	t.Helper()
	registerExtYAML()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	s := newDefaultConf()
	if err := extyaml.UnmarshalExt(data, s); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return s
}

func TestTestdataE2EConfigs(t *testing.T) {
	cases := []struct {
		path       string
		v4, v6     bool
		needPD     bool
		checkV6Msg bool
	}{
		{path: "testdata/e2e/dora_v4.yaml", v4: true, v6: false},
		{path: "testdata/e2e/dora_v6.yaml", v4: false, v6: true, needPD: true, checkV6Msg: true},
	}
	for _, tc := range cases {
		t.Run(filepath.Base(tc.path), func(t *testing.T) {
			s := loadTestSetupFromFile(t, tc.path)

			if s.Ifname != "C" {
				t.Errorf("ifname = %q, want C", s.Ifname)
			}
			if s.EnableV4 != tc.v4 || s.EnableV6 != tc.v6 {
				t.Errorf("EnableV4/EnableV6 = %v/%v, want %v/%v", s.EnableV4, s.EnableV6, tc.v4, tc.v6)
			}
			if s.DORA.NumOfClients != 4 {
				t.Errorf("NumOfClients = %d, want 4", s.DORA.NumOfClients)
			}
			if got := s.DORA.StartMAC.String(); got != "aa:bb:cc:11:22:33" {
				t.Errorf("StartMAC = %q, want aa:bb:cc:11:22:33", got)
			}
			if s.DORA.MacStep != 1 {
				t.Errorf("MacStep = %d, want 1", s.DORA.MacStep)
			}
			if len(s.DORA.StartVLANs) != 1 || s.DORA.StartVLANs[0].ID != 100 {
				t.Errorf("StartVLANs = %v, want [100]", s.DORA.StartVLANs)
			}
			if s.DORA.VLANStep != 0 {
				t.Errorf("VLANStep = %d, want 0", s.DORA.VLANStep)
			}
			if s.DORA.NeedPD != tc.needPD {
				t.Errorf("NeedPD = %v, want %v", s.DORA.NeedPD, tc.needPD)
			}
			if tc.checkV6Msg && s.V6MsgType != dhcpv6.MessageTypeSolicit {
				t.Errorf("V6MsgType = %v, want SOLICIT", s.V6MsgType)
			}
		})
	}
}
