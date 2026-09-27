package ignition

import (
	"fmt"
	"strings"

	"github.com/projectbluefin/knuckle/internal/model"
	"github.com/projectbluefin/knuckle/internal/validate"
)

// WifiTargetDir is where captured NetworkManager profiles are written on the
// installed system. It matches NMConnectionDir in the model.
const WifiTargetDir = model.NMConnectionDir

// renderWifiProfiles turns the captured keyfiles into validated, pre-indented
// entries ready to be embedded.
//
// Every filename is validated here, at the point where it becomes part of a
// path written to a disk, rather than trusted from the prober that read it: a
// name containing a separator or ".." would otherwise write outside the profile
// directory on the target.
//
// Returns nil when the step was skipped, so both callers can pass the result
// straight through without their own emptiness check.
func renderWifiProfiles(cfg *model.InstallConfig) ([]renderedWifiProfile, error) {
	if !cfg.Wifi.Enabled || len(cfg.Wifi.Profiles) == 0 {
		return nil, nil
	}

	out := make([]renderedWifiProfile, 0, len(cfg.Wifi.Profiles))
	for _, p := range cfg.Wifi.Profiles {
		if err := validate.WifiProfileFilename(p.Filename); err != nil {
			return nil, fmt.Errorf("wifi profile: %w", err)
		}
		body := strings.TrimRight(p.Contents, "\n")
		if strings.TrimSpace(body) == "" {
			return nil, fmt.Errorf("wifi profile %q is empty", p.Filename)
		}
		out = append(out, renderedWifiProfile{Filename: p.Filename, Body: body})
	}
	return out, nil
}

// addWifiProfiles adds the captured keyfiles to a Builder-based config (the FCOS
// and uCore generators).
//
// mode 0600 is not cosmetic: NetworkManager refuses to use a keyfile that is
// group- or world-readable, so a 0644 file would be silently ignored and the
// machine would boot with no network while looking correctly provisioned.
func addWifiProfiles(b *Builder, cfg *model.InstallConfig) error {
	profiles, err := renderWifiProfiles(cfg)
	if err != nil {
		return err
	}
	for _, p := range profiles {
		b.AddStorageFile("- path: " + WifiTargetDir + "/" + p.Filename + "\n" +
			"  mode: 0600\n" +
			"  overwrite: true\n" +
			"  contents:\n" +
			"    inline: |\n" +
			indentBlock(10, p.Body) + "\n")
	}
	return nil
}

// wifiLayerStateDir is where the layering stamp is written. It matches the
// swapfile unit's convention (/var/lib/knuckle) so both "knuckle ran this at
// first boot" sentinels live in one place.
const wifiLayerStateDir = "/var/lib/knuckle"

// WifiLayerStateStamp is the sentinel recording that the WiFi stack has been
// layered. It exists so the unit does not re-stage a deployment on every boot —
// and, more importantly, so a failure that never reached the stamp is retried.
const WifiLayerStateStamp = wifiLayerStateDir + "/.wifi-layered"

// wifiStackPackages are the userspace pieces without which NetworkManager has
// no WiFi at all. NetworkManager-wifi is a single plugin library
// (libnm-device-plugin-wifi.so) and pulls in wpa_supplicant and
// wireless-regdb, but they are named explicitly because the unit's success
// depends on the plugin actually being present.
func wifiStackPackages() []string {
	return []string{
		"NetworkManager-wifi",
		"wpa_supplicant",
		"wireless-regdb",
	}
}

// wifiFirmwarePackages are the adapter firmware blobs for the common vendors.
//
// Note what is *not* here. "linux-firmware" is deliberately absent: Fedora
// split the wireless blobs out into per-vendor subpackages and made them
// Recommends rather than Requires, and Fedora CoreOS composes with
// "recommends: false" — so installing linux-firmware resolves to no WiFi
// firmware at all on a CoreOS base image. The split also means there is no
// "linux-firmware-free" or "linux-firmware-nonfree" to ask for instead.
//
// "iwlwifi-mld-firmware" and "iwlbluetooth-firmware" are omitted for the
// opposite reason: they were split out very recently, so they are absent from
// older Fedora releases this installer still supports. Current
// iwlwifi-mvm-firmware Requires them, so dnf pulls them in where they exist.
//
// b43-fwcutter, b43-openfwwf, atmel-firmware and zd1211-firmware cover
// pre-802.11n hardware and are left out; a missing package would fail the
// whole install, and the stamp means a failure is not retried.
func wifiFirmwarePackages() []string {
	return []string{
		"atheros-firmware",
		"brcmfmac-firmware",
		"iwlegacy-firmware",
		"iwlwifi-dvm-firmware",
		"iwlwifi-mvm-firmware",
		"libertas-firmware",
		"mt7xxx-firmware",
		"nxpwireless-firmware",
		"realtek-firmware",
		"tiwilink-firmware",
	}
}

// addWifiStackUnit layers the WiFi stack onto the installed system.
//
// Writing the keyfiles (addWifiProfiles) is only half the job, and the half
// that is silently useless on its own. knuckle installs a *stock* upstream
// Fedora CoreOS deployment — coreos-installer is invoked with --stream and
// pulls the image from the registry, it does not derive the target from the
// live ISO's package set. So a machine installed from even a custom live ISO
// has no NetworkManager WiFi plugin and no adapter firmware, and the keyfile
// is inert: NetworkManager ignores a profile for a device type it has no
// support for. The machine boots with no network and nothing in the logs
// explains why.
//
// This unit closes that gap the way the upstream Fedora CoreOS documentation
// prescribes: layer the packages on first boot with rpm-ostree, then reboot so
// the staged deployment takes effect. See
// https://docs.fedoraproject.org/en-US/fedora-coreos/sysconfig-enabling-wifi/
//
// afterRebase must be true for uCore. Its autorebase unit replaces the whole
// deployment with the uCore image, which would discard anything layered
// beforehand, so the stack is held back until a rebase has already happened on
// an earlier boot.
func addWifiStackUnit(b *Builder, cfg *model.InstallConfig, afterRebase bool) error {
	// Gated on profiles, not merely on Enabled. An enabled step with nothing
	// captured has no network to join, so layering would pull in the whole
	// vendor firmware set and reboot the machine for nothing.
	if !cfg.Wifi.Enabled || len(cfg.Wifi.Profiles) == 0 {
		return nil
	}

	// Appended to the stamp condition on the same template line: an empty gate
	// then leaves no blank line behind, and both conditions stay adjacent.
	gate := ""
	if afterRebase {
		gate = "\n    ConditionPathExists=" + UcoreRebaseStateDir + "/rebased"
	}

	unit, err := renderTemplate("wifi-stack", wifiStackTemplate, struct {
		StateDir   string
		Stack      string
		Firmware   string
		RebaseGate string
	}{
		StateDir:   wifiLayerStateDir,
		Stack:      strings.Join(wifiStackPackages(), " "),
		Firmware:   strings.Join(wifiFirmwarePackages(), " "),
		RebaseGate: gate,
	})
	if err != nil {
		return err
	}
	b.AddSystemdUnit(unit)
	return nil
}

// wifiStackTemplate is the first-boot oneshot that layers the WiFi stack.
//
// Three properties of it are load-bearing, and each maps to a specific failure
// mode:
//
//   - The stack install is the only *unguarded* ExecStart. rpm-ostree resolves
//     every package or installs nothing, so a network-less first boot must
//     abort the unit before the stamp — that is what makes the next boot retry.
//   - The firmware install carries systemd's "-" prefix, so an unresolvable
//     blob name degrades to "NetworkManager works, this adapter's firmware is
//     missing" instead of losing the plugin too. It cannot be merged into the
//     line above for exactly that reason.
//   - The stamp is written only after both, and the reboot is last. Layering
//     stages a deployment; the packages are not live until it is booted.
var wifiStackTemplate = `- name: knuckle-wifi-stack.service
  enabled: true
  contents: |
    [Unit]
    Description=Layer the NetworkManager WiFi plugin and adapter firmware
    Documentation=https://docs.fedoraproject.org/en-US/fedora-coreos/sysconfig-enabling-wifi/
    Wants=network-online.target
    After=network-online.target
    ConditionPathExists=!{{.StateDir}}/.wifi-layered{{.RebaseGate}}

    [Service]
    Type=oneshot
    RemainAfterExit=yes
    StandardOutput=journal+console
    ExecStart=/usr/bin/rpm-ostree install -y --allow-inactive {{.Stack}}
    ExecStart=-/usr/bin/rpm-ostree install -y --allow-inactive {{.Firmware}}
    ExecStart=/usr/bin/install -D -m 0644 /dev/null {{.StateDir}}/.wifi-layered
    ExecStart=/usr/bin/systemctl --no-block reboot

    [Install]
    WantedBy=multi-user.target`

// indentBlock prefixes every non-empty line with n spaces. Multi-line content
// inside a YAML block scalar must be indented past the block marker or YAML
// terminates the scalar early and the document fails to parse.
func indentBlock(n int, s string) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}
