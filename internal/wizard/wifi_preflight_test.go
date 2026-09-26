package wizard

import (
	"strings"
	"testing"

	"github.com/projectbluefin/knuckle/internal/model"
)

func TestWifiPreflightBlocking(t *testing.T) {
	tests := []struct {
		name    string
		pf      WifiPreflight
		want    string
		blocked bool
	}{
		{
			name:    "nmtui present and radio present",
			pf:      WifiPreflight{NmtuiAvailable: true, NmtuiPath: "/usr/bin/nmtui", WirelessInterfaces: []string{"wlan0"}},
			blocked: false,
		},
		{
			name:    "nmtui missing is reported first",
			pf:      WifiPreflight{NmtuiAvailable: false},
			want:    "nmtui is not installed",
			blocked: true,
		},
		{
			name:    "no radio",
			pf:      WifiPreflight{NmtuiAvailable: true, NmtuiPath: "/usr/bin/nmtui"},
			want:    "No wireless adapter",
			blocked: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.pf.Blocking()
			if (got != "") != tt.blocked {
				t.Fatalf("Blocking() = %q, want blocked=%v", got, tt.blocked)
			}
			if tt.want != "" && !strings.Contains(got, tt.want) {
				t.Errorf("Blocking() = %q, want it to mention %q", got, tt.want)
			}
			// A blocked preflight must always point at the manual fallback,
			// otherwise the user has no way forward.
			if tt.blocked && !strings.Contains(got, "by hand") {
				t.Errorf("Blocking() = %q, should offer the manual fallback", got)
			}
		})
	}
}

func TestWifiPreflightHasWirelessHardware(t *testing.T) {
	if (WifiPreflight{}).HasWirelessHardware() {
		t.Error("empty preflight has no hardware")
	}
	if !(WifiPreflight{WirelessInterfaces: []string{"wlan0"}}).HasWirelessHardware() {
		t.Error("wlan0 should count as hardware")
	}
}

// The real preflight must never panic and must agree with the host it runs on.
func TestWifiPreflightRealProbe(t *testing.T) {
	w := New(nil, nil, nil)
	p := w.WifiPreflight()
	// lo is never wireless, so a positive result means it read sysfs.
	for _, iface := range p.WirelessInterfaces {
		if iface == "lo" {
			t.Error("lo must never be reported as a wireless interface")
		}
	}
}

func TestWifiPreflightOrDefaultCaches(t *testing.T) {
	w := New(nil, nil, nil)
	calls := 0
	w.WifiPreflightFn = func() WifiPreflight {
		calls++
		return WifiPreflight{NmtuiAvailable: true, WirelessInterfaces: []string{"wlan0"}}
	}

	first := w.WifiPreflightOrDefault()
	second := w.WifiPreflightOrDefault()

	if calls != 1 {
		t.Errorf("preflight probed %d times, want 1 (the answer cannot change mid-install)", calls)
	}
	if !first.NmtuiAvailable != !second.NmtuiAvailable ||
		len(first.WirelessInterfaces) != len(second.WirelessInterfaces) {
		t.Error("cached preflight should return the same result")
	}
}

func TestAddManualWifi(t *testing.T) {
	w := New(nil, nil, nil)

	if err := w.AddManualWifi("HomeWifi", "hunter2", true); err != nil {
		t.Fatalf("AddManualWifi() error = %v", err)
	}

	wifi := w.State.Config.Wifi
	if !wifi.Enabled || len(wifi.Profiles) != 1 {
		t.Fatalf("Wifi = %+v, want one profile", wifi)
	}
	p := wifi.Profiles[0]
	if p.Filename != model.ManualProfileFilename {
		t.Errorf("Filename = %q, want %q", p.Filename, model.ManualProfileFilename)
	}
	if p.SSID != "HomeWifi" || !p.Secured {
		t.Errorf("profile = %+v, want a secured HomeWifi", p)
	}
	if !strings.Contains(p.Contents, "ssid=HomeWifi") {
		t.Errorf("keyfile should carry the ssid:\n%s", p.Contents)
	}
	if !strings.Contains(p.Contents, "psk=hunter2") {
		t.Errorf("keyfile should carry the psk:\n%s", p.Contents)
	}
}

func TestAddManualWifiOpen(t *testing.T) {
	w := New(nil, nil, nil)
	if err := w.AddManualWifi("OpenNet", "", false); err != nil {
		t.Fatalf("AddManualWifi() error = %v", err)
	}
	p := w.State.Config.Wifi.Profiles[0]
	if p.Secured {
		t.Error("open network should not be secured")
	}
	if strings.Contains(p.Contents, "[wifi-security]") {
		t.Errorf("open keyfile must have no security section:\n%s", p.Contents)
	}
}

func TestAddManualWifiValidation(t *testing.T) {
	tests := []struct {
		name    string
		ssid    string
		psk     string
		secured bool
	}{
		{"empty ssid", "", "pw", true},
		{"whitespace ssid", "   ", "pw", true},
		{"secured with no password", "Home", "   ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := New(nil, nil, nil)
			if err := w.AddManualWifi(tt.ssid, tt.psk, tt.secured); err == nil {
				t.Error("expected an error")
			}
			if w.State.Config.Wifi.Enabled {
				t.Error("a rejected entry must not enable WiFi")
			}
		})
	}
}

// Repeated edits must not stack up keyfiles on the target.
func TestAddManualWifiReplacesPrevious(t *testing.T) {
	w := New(nil, nil, nil)
	if err := w.AddManualWifi("First", "", false); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := w.AddManualWifi("Second", "", false); err != nil {
		t.Fatalf("second: %v", err)
	}

	profiles := w.State.Config.Wifi.Profiles
	if len(profiles) != 1 {
		t.Fatalf("got %d profiles, want 1: %+v", len(profiles), profiles)
	}
	if profiles[0].SSID != "Second" {
		t.Errorf("SSID = %q, want Second", profiles[0].SSID)
	}
}

// A manual entry must coexist with a profile nmtui captured, not replace it.
func TestAddManualWifiKeepsCapturedProfiles(t *testing.T) {
	w := New(nil, nil, nil)
	w.State.Config.Wifi = model.WifiConfig{
		Enabled:  true,
		Profiles: []model.WifiProfile{{Filename: "home.nmconnection", SSID: "FromNmtui"}},
	}
	if err := w.AddManualWifi("Typed", "pw", true); err != nil {
		t.Fatalf("AddManualWifi() error = %v", err)
	}

	if len(w.State.Config.Wifi.Profiles) != 2 {
		t.Fatalf("got %d profiles, want both the captured and the manual one", len(w.State.Config.Wifi.Profiles))
	}
}

func TestAddManualWifiTrimsInput(t *testing.T) {
	w := New(nil, nil, nil)
	if err := w.AddManualWifi("  HomeWifi  ", "  pw  ", true); err != nil {
		t.Fatalf("AddManualWifi() error = %v", err)
	}
	p := w.State.Config.Wifi.Profiles[0]
	if p.SSID != "HomeWifi" {
		t.Errorf("SSID = %q, want trimmed HomeWifi", p.SSID)
	}
	if strings.Contains(p.Contents, "  pw") {
		t.Errorf("password should be trimmed:\n%s", p.Contents)
	}
}
