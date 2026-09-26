// Package iso generates Ignition configs and helpers for building
// self-contained installer disk images with knuckle embedded.
// Supports Flatcar Container Linux, Fedora CoreOS (FCOS), and uCore.
//
// The live installer medium is always the upstream live image of the base
// distribution — uCore publishes no ISO of its own, so its installer runs on
// the stock FCOS live image and installs uCore onto the target disk.
package iso

import (
	"encoding/json"

	"github.com/projectbluefin/knuckle/internal/model"
)

// ignitionConfig is the minimal Ignition 3.3.0 schema needed for the installer.
type ignitionConfig struct {
	Ignition ignitionMeta `json:"ignition"`
	Systemd  systemdCfg   `json:"systemd"`
	Passwd   *passwdCfg   `json:"passwd,omitempty"`
}

type ignitionMeta struct {
	Version string `json:"version"`
}

type systemdCfg struct {
	Units []unit `json:"units"`
}

type unit struct {
	Name     string `json:"name"`
	Enabled  *bool  `json:"enabled,omitempty"`
	Contents string `json:"contents,omitempty"`
}

type passwdCfg struct {
	Users []user `json:"users,omitempty"`
}

type user struct {
	Name              string   `json:"name"`
	SSHAuthorizedKeys []string `json:"sshAuthorizedKeys,omitempty"`
}

// knuckleServiceUnitFlatcar is the systemd unit that launches knuckle on
// Flatcar live images. Flatcar live does not autologin on tty1, so no
// conflict directives are needed.
const knuckleServiceUnitFlatcar = `[Unit]
Description=Knuckle Flatcar Installer
After=multi-user.target
ConditionPathExists=/opt/knuckle

[Service]
Type=idle
ExecStart=/opt/knuckle
StandardInput=tty
StandardOutput=tty
TTYPath=/dev/tty1
TTYReset=yes
TTYVHangup=yes
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target`

// knuckleServiceUnitFCOS is the systemd unit that launches knuckle on FCOS
// live images. FCOS runs getty@tty1.service with autologin for the "core"
// user by default, so we declare a conflict to prevent both services from
// fighting over tty1.
const knuckleServiceUnitFCOS = `[Unit]
Description=Knuckle FCOS Installer
After=multi-user.target
Conflicts=getty@tty1.service
Before=getty@tty1.service
ConditionPathExists=/opt/knuckle

[Service]
Type=idle
ExecStart=/opt/knuckle
StandardInput=tty
StandardOutput=tty
TTYPath=/dev/tty1
TTYReset=yes
TTYVHangup=yes
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target`

// GenerateInstallerIgnition creates Ignition JSON for the installer image.
// The knuckle binary must be placed at /opt/knuckle on the filesystem before
// booting with this config (via squashfs overlay for Flatcar, or an Ignition
// storage.files entry for FCOS).
//
// os selects the OS-specific service unit body. The Fedora CoreOS family
// (FCOS and uCore, which is a CoreOS derivative) gets the FCOS unit, which
// includes Conflicts/Before for getty@tty1.service to stop the live image's
// autologin getty from fighting the TUI for the console. Flatcar live does not
// autologin on tty1, so any other value (including model.OSFlatcar or "")
// picks the Flatcar unit.
//
// If sshPubKey is non-empty, it is added to the "core" user for debug access.
func GenerateInstallerIgnition(os, sshPubKey string) ([]byte, error) {
	enabled := true

	serviceUnit := knuckleServiceUnitFlatcar
	if model.IsCoreOSDerivative(os) {
		serviceUnit = knuckleServiceUnitFCOS
	}

	cfg := ignitionConfig{
		Ignition: ignitionMeta{Version: "3.3.0"},
		Systemd: systemdCfg{
			Units: []unit{
				{Name: "sshd.service", Enabled: &enabled},
				{
					Name:     "knuckle-installer.service",
					Enabled:  &enabled,
					Contents: serviceUnit,
				},
			},
		},
	}

	if sshPubKey != "" {
		cfg.Passwd = &passwdCfg{
			Users: []user{{Name: "core", SSHAuthorizedKeys: []string{sshPubKey}}},
		}
	}

	return json.MarshalIndent(cfg, "", "  ")
}
