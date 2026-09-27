package ignition

import (
	"fmt"

	"github.com/projectbluefin/knuckle/internal/model"
	"github.com/projectbluefin/knuckle/internal/validate"
)

// UcoreRebaseStateDir holds the sentinel that records that the rebase onto the
// uCore OCI image already ran, so the oneshot unit is a no-op on every boot
// after the first.
const UcoreRebaseStateDir = "/etc/ucore-knuckle"

// GenerateUcoreButane produces a Butane YAML config string for uCore.
//
// # How uCore is installed
//
// uCore publishes no ISO and no installer of its own. It is an OCI image that
// extends Fedora CoreOS, and its documented install path is: install Fedora
// CoreOS, then rebase that deployment onto the uCore image. This generator
// implements that path — the deployment is installed by coreos-installer (see
// internal/install) and this config rebases it on first boot.
//
// # Deliberate omissions vs GenerateFCOSButane
//
//   - No zincati. uCore disables zincati and updates via rpm-ostreed/bootc, so
//     writing a zincati strategy would configure a service that never runs.
//   - No sysexts. The bakery serves Flatcar sysexts built against Flatcar
//     kernels; they will not load on a Fedora kernel.
//   - No NVIDIA unit. uCore ships its driver in the image tag, selected by
//     UcoreConfig.Nvidia.
func (g *Generator) GenerateUcoreButane(cfg *model.InstallConfig) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("config cannot be nil")
	}

	uc := cfg.Ucore.WithDefaults()

	// Validate the image selection before it becomes a reference baked into a
	// unit that runs unattended at first boot. A typo here would leave the
	// machine sitting on stock CoreOS with a failing unit and no explanation.
	if err := validate.UcoreImage(uc.Image); err != nil {
		return "", fmt.Errorf("uCore image: %w", err)
	}
	if err := validate.UcoreStream(uc.Stream); err != nil {
		return "", fmt.Errorf("uCore stream: %w", err)
	}
	if err := validate.UcoreNvidia(uc.Nvidia); err != nil {
		return "", fmt.Errorf("uCore NVIDIA variant: %w", err)
	}
	if err := validate.UcoreVerifyMode(uc.Verify); err != nil {
		return "", fmt.Errorf("uCore verify mode: %w", err)
	}

	// The wizard never offers sysexts for uCore, but a headless config can set
	// them. Strip them rather than trust the caller: Flatcar bakery sysexts are
	// built against Flatcar kernels and silently fail to load on a Fedora one,
	// so writing them would be a no-op that looks provisioned.
	scoped := *cfg
	scoped.Sysexts = nil

	b := NewBuilder(&scoped)
	if err := addSharedFragments(b, &scoped); err != nil {
		return "", err
	}

	// The sentinel directory must exist before the unit writes into it.
	// Created with a restrictive mode because it lives under /etc.
	b.AddStorageDirectory(`- path: ` + UcoreRebaseStateDir + `
  mode: 0755`)

	rebaseUnit, err := renderTemplate("ucore-autorebase", ucoreAutorebaseTemplate, struct {
		ImageReference string
		StateDir       string
	}{ImageReference: uc.ImageReference(), StateDir: UcoreRebaseStateDir})
	if err != nil {
		return "", fmt.Errorf("rendering uCore autorebase unit: %w", err)
	}
	b.AddSystemdUnit(rebaseUnit)

	addSwapUnits(b, cfg)
	if err := addWifiProfiles(b, cfg); err != nil {
		return "", err
	}
	// Gated on the rebase sentinel: the autorebase unit above swaps the whole
	// deployment for the uCore image, so anything layered before it completes is
	// thrown away. Waiting for an earlier boot's rebase is what makes the stack
	// survive.
	if err := addWifiStackUnit(b, cfg, true); err != nil {
		return "", err
	}

	return b.BuildFCOS(), nil
}

// ucoreAutorebaseTemplate is the oneshot unit that performs the rebase.
//
// It follows the shape of uCore's own documented ucore-autorebase.service: a
// Type=oneshot with sequential ExecStart lines, so a failed rebase neither
// records the sentinel nor reboots. Keeping the sentinel is what makes the
// unit idempotent — without it every subsequent boot would re-run the rebase.
//
// ExecStart lines run in order and a failure aborts the remainder, which is
// what gives the three-line sequence its guard semantics without a shell.
var ucoreAutorebaseTemplate = `- name: ucore-knuckle-autorebase.service
  enabled: true
  contents: |
    [Unit]
    Description=Rebase onto the uCore OCI image and reboot
    Documentation=https://github.com/ublue-os/ucore
    ConditionPathExists=!{{.StateDir}}/rebased
    After=network-online.target
    Wants=network-online.target

    [Service]
    Type=oneshot
    StandardOutput=journal+console
    ExecStart=/usr/bin/rpm-ostree rebase --bypass-driver "{{.ImageReference | yamlEscape}}"
    ExecStart=/usr/bin/touch {{.StateDir}}/rebased
    ExecStart=/usr/bin/systemctl reboot

    [Install]
    WantedBy=multi-user.target`
