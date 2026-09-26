package wizard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/projectbluefin/knuckle/internal/model"
	"github.com/projectbluefin/knuckle/internal/probe"
)

// stubProfiles points the wizard's lister at a fixture directory.
func stubProfiles(t *testing.T, dir string) *Wizard {
	t.Helper()
	w := New(nil, nil, nil)
	w.ListWifiProfiles = func(string) ([]model.WifiProfile, error) {
		return probe.ListWifiProfiles(dir)
	}
	return w
}

func writeProfile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestCaptureWifiProfiles(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "home.nmconnection", "[connection]\nssid=Home\n\n[wifi-security]\npsk=s3cret\n")
	w := stubProfiles(t, dir)

	if err := w.CaptureWifiProfiles(); err != nil {
		t.Fatalf("CaptureWifiProfiles() error = %v", err)
	}

	wifi := w.State.Config.Wifi
	if !wifi.Enabled {
		t.Error("Wifi.Enabled = false, want true after capturing a profile")
	}
	if len(wifi.Profiles) != 1 || wifi.Profiles[0].Filename != "home.nmconnection" {
		t.Fatalf("profiles = %+v, want home.nmconnection", wifi.Profiles)
	}
	if wifi.Profiles[0].SSID != "Home" {
		t.Errorf("SSID = %q, want Home", wifi.Profiles[0].SSID)
	}
	if !wifi.Profiles[0].Secured {
		t.Error("a profile with a psk must report Secured=true")
	}
}

func TestCaptureWifiProfilesNone(t *testing.T) {
	w := stubProfiles(t, t.TempDir())
	if err := w.CaptureWifiProfiles(); err != nil {
		t.Fatalf("CaptureWifiProfiles() error = %v", err)
	}
	if w.State.Config.Wifi.Enabled {
		t.Error("Wifi.Enabled = true with no profiles, want false")
	}
}

func TestCaptureWifiProfilesError(t *testing.T) {
	w := New(nil, nil, nil)
	w.ListWifiProfiles = func(string) ([]model.WifiProfile, error) {
		return nil, os.ErrPermission
	}
	if err := w.CaptureWifiProfiles(); err == nil {
		t.Error("expected an error when the profile directory cannot be read")
	}
}

// A nil lister must fall back to the real prober rather than panicking, so a
// Wizard built as a bare struct still works.
func TestCaptureWifiProfilesNilLister(t *testing.T) {
	w := New(nil, nil, nil)
	w.ListWifiProfiles = nil
	// The real lister reads /etc/NetworkManager/system-connections, which does
	// not exist here, so this returns an empty set rather than an error.
	if err := w.CaptureWifiProfiles(); err != nil {
		t.Fatalf("nil lister should fall back, got %v", err)
	}
}

// The filename becomes a write path on the target, so unsafe names never make
// it into the config.
func TestCaptureWifiProfilesDropsUnsafeNames(t *testing.T) {
	w := New(nil, nil, nil)
	w.ListWifiProfiles = func(string) ([]model.WifiProfile, error) {
		return []model.WifiProfile{
			{Filename: "../escape.nmconnection"},
			{Filename: "good.nmconnection"},
		}, nil
	}

	if err := w.CaptureWifiProfiles(); err != nil {
		t.Fatalf("CaptureWifiProfiles() error = %v", err)
	}
	if len(w.State.Config.Wifi.Profiles) != 1 {
		t.Fatalf("profiles = %+v, want only the safe one", w.State.Config.Wifi.Profiles)
	}
	if w.State.Config.Wifi.Profiles[0].Filename != "good.nmconnection" {
		t.Errorf("kept %q, want good.nmconnection", w.State.Config.Wifi.Profiles[0].Filename)
	}
}

func TestSkipWifiClearsProfiles(t *testing.T) {
	w := New(nil, nil, nil)
	w.State.Config.Wifi = model.WifiConfig{
		Enabled:  true,
		Profiles: []model.WifiProfile{{Filename: "a.nmconnection"}},
	}

	w.SkipWifi()

	if w.State.Config.Wifi.Enabled || len(w.State.Config.Wifi.Profiles) != 0 {
		t.Errorf("SkipWifi left %+v, want an empty config", w.State.Config.Wifi)
	}
}

func TestWifiWarnings(t *testing.T) {
	profiles := []model.WifiProfile{{Filename: "a.nmconnection", SSID: "Home"}}

	tests := []struct {
		name      string
		os        string
		wifi      model.WifiConfig
		wantWarns int
	}{
		{"fcos is honoured", model.OSFCOS, model.WifiConfig{Enabled: true, Profiles: profiles}, 0},
		{"ucore is honoured", model.OSUcore, model.WifiConfig{Enabled: true, Profiles: profiles}, 0},
		{"flatcar uses networkd", model.OSFlatcar, model.WifiConfig{Enabled: true, Profiles: profiles}, 2},
		{"bluefin has no ignition", model.OSBluefinDDI, model.WifiConfig{Enabled: true, Profiles: profiles}, 2},
		{"nothing captured", model.OSFlatcar, model.WifiConfig{}, 0},
		{"enabled but empty", model.OSFlatcar, model.WifiConfig{Enabled: true}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := New(nil, nil, nil)
			w.State.Config.OS = tt.os
			w.State.Config.Wifi = tt.wifi

			got := w.WifiWarnings()
			if len(got) != tt.wantWarns {
				t.Errorf("WifiWarnings() = %v, want %d warnings", got, tt.wantWarns)
			}
		})
	}
}

// The step is always offered, so it must appear in the path for every OS.
func TestWifiStepIsAlwaysVisited(t *testing.T) {
	for _, os := range []string{model.OSFlatcar, model.OSFCOS, model.OSUcore, model.OSBluefinDDI} {
		w := New(nil, nil, nil)
		w.State.Config.OS = os
		w.State.CurrentStep = model.StepNetwork

		if err := w.Next(); err != nil {
			t.Fatalf("%s: Next() error = %v", os, err)
		}
		if w.State.CurrentStep != model.StepWifi {
			t.Errorf("%s: after Network the step is %s, want WiFi", os, w.State.CurrentStep)
		}
	}
}

func TestWifiStepValidateCurrentStep(t *testing.T) {
	w := New(nil, nil, nil)
	w.State.CurrentStep = model.StepWifi
	// Skipping is valid, so validation never fails here.
	if err := w.ValidateCurrentStep(); err != nil {
		t.Errorf("ValidateCurrentStep() on the WiFi step = %v, want nil", err)
	}
}
