package model

import "strings"

// WiFi support in knuckle is a two-phase affair:
//
//  1. In the installer, the user joins a network with nmtui. That writes a
//     NetworkManager keyfile to /etc/NetworkManager/system-connections/.
//  2. Those keyfiles are copied onto the *target* system by the generated
//     Ignition config, so the installed machine comes up already joined.
//
// Phase 2 only works where NetworkManager is the network stack. Fedora CoreOS
// and uCore use it, so this is where WiFi persistence takes effect. Flatcar
// uses systemd-networkd and Bluefin Server is a DDI image with no Ignition at
// all, so on those targets the profiles are written but inert — the TUI says so
// explicitly rather than implying the machine will come up on WiFi.

// NMConnectionDir is where NetworkManager keeps connection profiles.
// Ignition writes the captured profiles here on the target system.
const NMConnectionDir = "/etc/NetworkManager/system-connections"

// WifiProfile is a single NetworkManager keyfile captured from the installer
// environment.
//
// Contents holds the keyfile verbatim, including the PSK for a secured
// network. That makes it a secret: it must never be logged, and it must never
// be echoed into a dry-run or debug dump. It is written to the target with
// mode 0600, which is what NetworkManager requires — it ignores world-readable
// keyfiles.
type WifiProfile struct {
	// Filename is the keyfile's basename, e.g. "knuckle-wifi.nmconnection".
	// Only the basename is ever used; it is validated before being joined onto
	// a directory so a hostile name cannot escape NMConnectionDir.
	Filename string
	// SSID is the network name, for display only.
	SSID string
	// Secured reports whether the profile carries a pre-shared key.
	Secured bool
	// Contents is the verbatim keyfile body. Secret.
	Contents string
}

// WifiConfig carries the WiFi choices from the installer step to the installed
// system.
type WifiConfig struct {
	// Enabled is true when the user ran nmtui and at least one profile was
	// captured. False means the step was skipped and nothing is written.
	Enabled bool
	// Profiles are the captured keyfiles, in capture order.
	Profiles []WifiProfile
}

// ApplyAppliesToTarget reports whether the captured profiles will actually be
// honoured on the given OS.
//
// They are honoured only by the NetworkManager-based CoreOS derivatives. On
// Flatcar (systemd-networkd) and Bluefin Server (no Ignition) the keyfiles are
// still written — preserving the user's work costs nothing — but nothing reads
// them, so the UI warns rather than promising a connected machine.
func (w WifiConfig) ApplyAppliesToTarget(os string) bool {
	return IsCoreOSDerivative(os)
}

// SSIDs returns the captured network names, for display.
func (w WifiConfig) SSIDs() []string {
	out := make([]string, 0, len(w.Profiles))
	for _, p := range w.Profiles {
		out = append(out, p.SSID)
	}
	return out
}

// ManualProfileFilename is the keyfile name used for a network the user typed
// in rather than joined with nmtui. The "manual-" prefix keeps it
// distinguishable from a keyfile nmtui generated itself.
const ManualProfileFilename = "manual-wifi.nmconnection"

// BuildWifiKeyfile renders a NetworkManager keyfile for a network the user
// supplied by hand.
//
// This exists because nmtui cannot scan on a minimal live image: Fedora's
// CoreOS live environment ships without wireless firmware and commonly without
// nmtui itself, so there may be no radio to scan with. The point of the WiFi step
// is to get the *installed* system onto a network, and the full uCore image
// ships the firmware, so a hand-built keyfile still lands the machine on WiFi
// even when the installer could never see a network itself.
//
// The output is a standard PSK keyfile. The password is written verbatim into
// the psk field, which NetworkManager accepts for WPA-PSK.
func BuildWifiKeyfile(ssid, psk string, secured bool) string {
	var b strings.Builder
	b.WriteString("[connection]\n")
	b.WriteString("id=" + ssid + "\n")
	b.WriteString("type=wifi\n")
	b.WriteString("autoconnect=true\n")
	b.WriteString("\n")
	b.WriteString("[wifi]\n")
	b.WriteString("ssid=" + ssid + "\n")
	b.WriteString("mode=infrastructure\n")
	if secured {
		b.WriteString("\n")
		b.WriteString("[wifi-security]\n")
		b.WriteString("key-mgmt=wpa-psk\n")
		b.WriteString("psk=" + psk + "\n")
	}
	return b.String()
}

// NewManualProfile builds a WifiProfile for a hand-entered network.
func NewManualProfile(ssid, psk string, secured bool) WifiProfile {
	return WifiProfile{
		Filename: ManualProfileFilename,
		SSID:     ssid,
		Secured:  secured,
		Contents: BuildWifiKeyfile(ssid, psk, secured),
	}
}
