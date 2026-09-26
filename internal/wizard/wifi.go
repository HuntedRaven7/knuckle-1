package wizard

import (
	"fmt"

	"github.com/projectbluefin/knuckle/internal/model"
	"github.com/projectbluefin/knuckle/internal/probe"
	"github.com/projectbluefin/knuckle/internal/validate"
)

// nmConnectionDir is where nmtui writes its profiles.
const nmConnectionDir = model.NMConnectionDir

// WifiProfileLister reads NetworkManager keyfiles from a directory. It is a
// field on Wizard rather than a package-level hook so that packages driving the
// wizard — the TUI in particular — can substitute a fixture directory in tests
// without reaching into this package's unexported state.
type WifiProfileLister func(dir string) ([]model.WifiProfile, error)

// CaptureWifiProfiles reads the NetworkManager profiles present in the live
// environment and records them on the config for the installer to copy onto
// the target.
//
// The keyfiles contain pre-shared keys, so the result is secret: callers must
// not log it. It is only ever handed to the Ignition generator, which writes it
// to a 0600 temp file.
//
// Profiles whose filename could escape the profile directory are refused rather
// than carried forward, since each becomes a write path on the target disk.
// The prober already filters these; re-checking here keeps the guarantee local
// to the code that turns a name into a config field.
func (w *Wizard) CaptureWifiProfiles() error {
	lister := w.ListWifiProfiles
	if lister == nil {
		lister = probe.ListWifiProfiles
	}
	profiles, err := lister(nmConnectionDir)
	if err != nil {
		return fmt.Errorf("reading NetworkManager profiles: %w", err)
	}

	kept := make([]model.WifiProfile, 0, len(profiles))
	for _, p := range profiles {
		if err := validate.WifiProfileFilename(p.Filename); err != nil {
			continue
		}
		kept = append(kept, p)
	}

	w.State.Config.Wifi = model.WifiConfig{
		Enabled:  len(kept) > 0,
		Profiles: kept,
	}
	return nil
}

// SkipWifi clears any captured profiles, so the step can be backed out of
// without leaving a stale network behind on the target.
func (w *Wizard) SkipWifi() {
	w.State.Config.Wifi = model.WifiConfig{}
}

// WifiWarnings returns human-readable caveats about whether the captured
// profiles will actually take effect on the chosen OS.
//
// The step is always offered, so on a Flatcar or Bluefin target the user can
// legitimately configure WiFi and be told plainly that it will not be carried
// over, rather than discovering it after the reboot.
func (w *Wizard) WifiWarnings() []string {
	cfg := w.State.Config.Wifi
	if !cfg.Enabled || len(cfg.Profiles) == 0 {
		return nil
	}
	if cfg.ApplyAppliesToTarget(w.State.Config.OS) {
		return nil
	}
	return []string{
		"WiFi profiles apply to Fedora CoreOS and uCore only.",
		"This target uses systemd-networkd, so the profiles are written but ignored.",
	}
}
