package wizard

import (
	"fmt"
	"strings"

	"github.com/projectbluefin/knuckle/internal/model"
	"github.com/projectbluefin/knuckle/internal/probe"
	"github.com/projectbluefin/knuckle/internal/runner"
)

// WifiPreflight is the result of asking the live environment whether WiFi is
// usable *right now*.
//
// This exists because the failure is otherwise silent and confusing. A minimal
// live image typically ships no wireless firmware and often no nmtui, so
// launching nmtui yields a tool that cannot scan — or a "command not found" —
// and the user has no way to tell that apart from a broken adapter. The
// distinction matters because the two have completely different remedies.
type WifiPreflight struct {
	// NmtuiPath is where nmtui was found; empty when it is not installed.
	NmtuiPath string
	// WirelessInterfaces are the wireless adapters the kernel knows about.
	WirelessInterfaces []string
	// NmtuiAvailable reports whether nmtui can be launched at all.
	NmtuiAvailable bool
}

// HasWirelessHardware reports whether any wireless adapter is present.
func (p WifiPreflight) HasWirelessHardware() bool {
	return len(p.WirelessInterfaces) > 0
}

// Blocking returns a human-readable explanation of why nmtui scanning will not
// work, or "" when it should.
//
// The order matters: a missing nmtui is reported first because it is the
// actionable one, and hardware is only worth mentioning once nmtui exists.
func (p WifiPreflight) Blocking() string {
	if !p.NmtuiAvailable {
		return "nmtui is not installed on this live image, so networks cannot be scanned here. " +
			"Enter the network details by hand instead — it will be written to the installed system."
	}
	if !p.HasWirelessHardware() {
		return "No wireless adapter detected. " +
			"The live image ships no wireless firmware, so the adapter may have no driver loaded. " +
			"Enter the network details by hand instead."
	}
	return ""
}

// WifiPreflightOrDefault returns the preflight, computing it on first use.
//
// Cached because the view renders on every keystroke and the answer cannot
// change while the installer is running: the live image's package set and the
// set of wireless adapters are both fixed for the life of the process.
func (w *Wizard) WifiPreflightOrDefault() WifiPreflight {
	if !w.wifiPreflightDone {
		w.wifiPreflight = w.WifiPreflight()
		w.wifiPreflightDone = true
	}
	return w.wifiPreflight
}

// WifiPreflight inspects the live environment's WiFi capability.
//
// The checks are runtime probes of the machine the installer is running on, not
// assumptions about which distribution it is, so the same code is correct on a
// custom ISO that does ship firmware.
func (w *Wizard) WifiPreflight() WifiPreflight {
	if w.WifiPreflightFn != nil {
		return w.WifiPreflightFn()
	}

	p := WifiPreflight{}

	if path, err := runner.LookPath(nmtuiBinary); err == nil {
		p.NmtuiPath = path
		p.NmtuiAvailable = true
	}

	if ifaces, err := probe.WirelessInterfaces(); err == nil {
		p.WirelessInterfaces = ifaces
	}

	return p
}

// nmtuiBinary is the interactive tool the step hands the terminal to.
const nmtuiBinary = "nmtui"

// AddManualWifi records a hand-entered network so it is written to the target.
//
// Used when the live image cannot scan. The keyfile is built here rather than
// in the TUI so the format lives next to the other WifiConfig rules, and so the
// value is validated like any other captured profile.
func (w *Wizard) AddManualWifi(ssid, psk string, secured bool) error {
	ssid = strings.TrimSpace(ssid)
	if ssid == "" {
		return fmt.Errorf("SSID must not be empty")
	}
	if secured && strings.TrimSpace(psk) == "" {
		return fmt.Errorf("a secured network needs a password")
	}

	profile := model.NewManualProfile(ssid, strings.TrimSpace(psk), secured)

	// Replace any previous manual entry rather than accumulating them, so
	// repeated edits do not pile up keyfiles on the target.
	kept := make([]model.WifiProfile, 0, len(w.State.Config.Wifi.Profiles)+1)
	for _, p := range w.State.Config.Wifi.Profiles {
		if p.Filename != model.ManualProfileFilename {
			kept = append(kept, p)
		}
	}
	kept = append(kept, profile)

	w.State.Config.Wifi = model.WifiConfig{Enabled: true, Profiles: kept}
	return nil
}
