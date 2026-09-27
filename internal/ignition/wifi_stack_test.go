package ignition

import (
	"strings"
	"testing"

	"github.com/projectbluefin/knuckle/internal/model"
)

// generateWifiStack runs the FCOS generator over a config whose WiFi step
// captured one profile, and returns the Butane document.
func generateWifiStack(t *testing.T, os string) string {
	t.Helper()
	g := NewGenerator()
	var (
		out string
		err error
	)
	switch os {
	case model.OSUcore:
		out, err = g.GenerateUcoreButane(wifiConfig(os))
	default:
		out, err = g.GenerateFCOSButane(wifiConfig(os))
	}
	if err != nil {
		t.Fatalf("generate error = %v", err)
	}
	return out
}

// A keyfile alone is inert: coreos-installer lays down a *stock* upstream FCOS
// deployment, so the installed machine has no NetworkManager WiFi plugin and no
// adapter firmware for the profile to be read by. The layering unit is what
// makes the installed machine actually come up on WiFi.
func TestWifiStackLayersNetworkManagerWifi(t *testing.T) {
	out := generateWifiStack(t, model.OSFCOS)

	if !strings.Contains(out, "knuckle-wifi-stack.service") {
		t.Errorf("expected the wifi stack unit:\n%s", out)
	}
	for _, want := range []string{
		"NetworkManager-wifi",
		"wpa_supplicant",
		"wireless-regdb",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the layering unit:\n%s", want, out)
		}
	}
}

// Installing "linux-firmware" resolves to no wireless firmware on a CoreOS base
// image: Fedora made those blobs Recommends and CoreOS composes with
// recommends: false. Asking for it would look right and do nothing.
func TestWifiStackDoesNotRequestUselessLinuxFirmware(t *testing.T) {
	out := generateWifiStack(t, model.OSFCOS)

	if strings.Contains(out, "ExecStart=/usr/bin/rpm-ostree install -y --allow-inactive linux-firmware\n") {
		t.Errorf("linux-firmware alone pulls no wireless firmware on CoreOS:\n%s", out)
	}
	// The real per-vendor blobs must be requested by name instead.
	for _, want := range []string{
		"iwlwifi-mvm-firmware",
		"brcmfmac-firmware",
		"atheros-firmware",
		"realtek-firmware",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected vendor firmware %q:\n%s", want, out)
		}
	}
}

// rpm-ostree resolves every package or installs nothing, so a single unresolvable
// blob name would cost the plugin too. The firmware pass is therefore
// systemd-ignore-failure, and the failure must be on *that* line only.
func TestWifiStackFirmwareInstallIsFailureTolerant(t *testing.T) {
	out := generateWifiStack(t, model.OSFCOS)

	guarded := "ExecStart=-/usr/bin/rpm-ostree install -y --allow-inactive atheros-firmware"
	if !strings.Contains(out, guarded) {
		t.Errorf("firmware install must tolerate an unresolvable blob:\n%s", out)
	}
	// The stack install itself stays unguarded: losing it must abort the unit so
	// the stamp is not written and the next boot retries. Matched without an
	// indent prefix because the builder and Butane both re-indent the fragment.
	if !strings.Contains(out, "ExecStart=/usr/bin/rpm-ostree install -y --allow-inactive NetworkManager-wifi") {
		t.Errorf("stack install must remain unguarded so a failed attempt retries:\n%s", out)
	}
	if strings.Contains(out, "ExecStart=-/usr/bin/rpm-ostree install -y --allow-inactive NetworkManager-wifi") {
		t.Errorf("stack install must not be failure-tolerant:\n%s", out)
	}
}

// The stamp is what makes the unit idempotent, and its absence on the failure
// path is what makes a network-less first boot self-heal on the next one.
func TestWifiStackStampGuardsReruns(t *testing.T) {
	out := generateWifiStack(t, model.OSFCOS)

	if !strings.Contains(out, "ConditionPathExists=!/var/lib/knuckle/.wifi-layered") {
		t.Errorf("expected a stamp condition:\n%s", out)
	}
	// Stamp after both installs, reboot last: layering stages a deployment, and
	// the packages are not live until it has booted.
	installAt := strings.Index(out, "ExecStart=/usr/bin/rpm-ostree install")
	stampAt := strings.Index(out, "install -D -m 0644 /dev/null /var/lib/knuckle/.wifi-layered")
	rebootAt := strings.Index(out, "systemctl --no-block reboot")
	if installAt < 0 || stampAt < 0 || rebootAt < 0 {
		t.Fatalf("missing expected ExecStart lines:\n%s", out)
	}
	if installAt > stampAt || stampAt > rebootAt {
		t.Errorf("want install -> stamp -> reboot, got offsets %d/%d/%d:\n%s",
			installAt, stampAt, rebootAt, out)
	}
}

// The uCore autorebase unit replaces the whole deployment with the uCore image.
// Anything layered before that completes is discarded, so the stack must wait
// for a rebase that already happened on an earlier boot.
func TestUcoreWifiStackWaitsForRebase(t *testing.T) {
	out := generateWifiStack(t, model.OSUcore)

	gate := "ConditionPathExists=" + UcoreRebaseStateDir + "/rebased"
	if !strings.Contains(out, gate) {
		t.Errorf("uCore must gate layering on a completed rebase (%s):\n%s", gate, out)
	}

	fcos := generateWifiStack(t, model.OSFCOS)
	if strings.Contains(fcos, gate) {
		t.Errorf("FCOS never rebases, so the gate must not appear:\n%s", fcos)
	}
}

// An enabled step that captured nothing has no network to join. Layering would
// pull in firmware and reboot the machine for no reason.
func TestWifiStackOmittedWithoutProfiles(t *testing.T) {
	g := NewGenerator()
	cfg := wifiConfig(model.OSFCOS)
	cfg.Wifi = model.WifiConfig{Enabled: true}

	out, err := g.GenerateFCOSButane(cfg)
	if err != nil {
		t.Fatalf("generate error = %v", err)
	}
	if strings.Contains(out, "rpm-ostree install") {
		t.Errorf("no profiles should mean no layering:\n%s", out)
	}
}

// Flatcar is systemd-networkd and ships its own driver/firmware story, so the
// NetworkManager layering must not leak into that target.
func TestFlatcarHasNoWifiStackUnit(t *testing.T) {
	g := NewGenerator()
	out, err := g.GenerateButane(wifiConfig(model.OSFlatcar))
	if err != nil {
		t.Fatalf("generate error = %v", err)
	}
	if strings.Contains(out, "knuckle-wifi-stack.service") {
		t.Errorf("Flatcar must not get the NetworkManager layering unit:\n%s", out)
	}
}

// A malformed unit template must fail the whole generation rather than emit a
// truncated unit that looks provisioned. Matches the injection pattern used for
// the other section templates.
func TestWifiStackTemplateError(t *testing.T) {
	orig := wifiStackTemplate
	wifiStackTemplate = `{{ .StateDir `
	t.Cleanup(func() { wifiStackTemplate = orig })

	if _, err := NewGenerator().GenerateFCOSButane(wifiConfig(model.OSFCOS)); err == nil {
		t.Error("expected an error when the wifi stack unit template is malformed")
	}
	if _, err := NewGenerator().GenerateUcoreButane(wifiConfig(model.OSUcore)); err == nil {
		t.Error("expected an error from the uCore generator too")
	}
}

// The unit is embedded in a YAML block scalar, so a mis-indented line would
// silently truncate the unit body. This is the real compiler, not a string check.
func TestWifiStackUnitSurvivesButaneCompile(t *testing.T) {
	for _, os := range []string{model.OSFCOS, model.OSUcore} {
		t.Run(os, func(t *testing.T) {
			out := generateWifiStack(t, os)
			ign, err := CompileToIgnition(out)
			if err != nil {
				t.Fatalf("Butane compile failed: %v\n%s", err, out)
			}
			for _, want := range []string{
				"knuckle-wifi-stack.service",
				"/var/lib/knuckle/.wifi-layered",
				"--no-block reboot",
			} {
				if !strings.Contains(string(ign), want) {
					t.Errorf("expected %q in compiled Ignition:\n%s", want, ign)
				}
			}
		})
	}
}
