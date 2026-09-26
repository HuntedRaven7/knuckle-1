package model

import (
	"strings"
	"testing"
)

func TestWifiApplyAppliesToTarget(t *testing.T) {
	// NetworkManager is the stack only on the CoreOS derivatives. Flatcar uses
	// systemd-networkd and Bluefin Server has no Ignition at all, so profiles
	// are inert there and the UI warns instead of promising a connection.
	tests := []struct {
		os   string
		want bool
	}{
		{OSFCOS, true},
		{OSUcore, true},
		{OSFlatcar, false},
		{OSBluefinDDI, false},
		{"", false},
	}

	for _, tt := range tests {
		if got := (WifiConfig{Enabled: true}).ApplyAppliesToTarget(tt.os); got != tt.want {
			t.Errorf("ApplyAppliesToTarget(%q) = %v, want %v", tt.os, got, tt.want)
		}
	}
}

func TestWifiSSIDs(t *testing.T) {
	cfg := WifiConfig{Profiles: []WifiProfile{
		{SSID: "HomeWifi"},
		{SSID: "Guest"},
	}}
	got := cfg.SSIDs()
	if len(got) != 2 || got[0] != "HomeWifi" || got[1] != "Guest" {
		t.Errorf("SSIDs() = %v, want [HomeWifi Guest]", got)
	}

	// An empty config must yield an empty, non-nil slice so callers can range
	// over it unconditionally.
	if got := (WifiConfig{}).SSIDs(); len(got) != 0 {
		t.Errorf("SSIDs() on empty config = %v, want empty", got)
	}
}

func TestStepWifiString(t *testing.T) {
	if got := StepWifi.String(); got != "WiFi" {
		t.Errorf("StepWifi.String() = %q, want %q", got, "WiFi")
	}
}

func TestBuildWifiKeyfile(t *testing.T) {
	secured := BuildWifiKeyfile("HomeWifi", "hunter2", true)
	for _, want := range []string{
		"[connection]", "id=HomeWifi", "type=wifi", "autoconnect=true",
		"[wifi]", "ssid=HomeWifi", "mode=infrastructure",
		"[wifi-security]", "key-mgmt=wpa-psk", "psk=hunter2",
	} {
		if !strings.Contains(secured, want) {
			t.Errorf("secured keyfile missing %q:\n%s", want, secured)
		}
	}

	open := BuildWifiKeyfile("OpenNet", "", false)
	if !strings.Contains(open, "ssid=OpenNet") {
		t.Errorf("open keyfile missing the ssid:\n%s", open)
	}
	if strings.Contains(open, "psk=") || strings.Contains(open, "[wifi-security]") {
		t.Errorf("open keyfile must carry no credentials:\n%s", open)
	}
}

func TestNewManualProfile(t *testing.T) {
	p := NewManualProfile("HomeWifi", "hunter2", true)
	if p.Filename != ManualProfileFilename {
		t.Errorf("Filename = %q, want %q", p.Filename, ManualProfileFilename)
	}
	if p.SSID != "HomeWifi" || !p.Secured {
		t.Errorf("profile = %+v, want a secured HomeWifi", p)
	}
	if p.Contents != BuildWifiKeyfile("HomeWifi", "hunter2", true) {
		t.Error("Contents must match BuildWifiKeyfile")
	}
}
