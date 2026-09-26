package iso

import (
	"strings"
	"testing"

	"github.com/projectbluefin/knuckle/internal/model"
)

// uCore has no live ISO of its own, so its installer runs on the FCOS live
// image it extends. That means it must get the FCOS service unit — the one
// that stops getty@tty1.service from fighting knuckle for the console — not
// the Flatcar unit, whose live image does not autologin.
func TestGenerateInstallerIgnition_UcoreUsesFCOSServiceUnit(t *testing.T) {
	out, err := GenerateInstallerIgnition(model.OSUcore, "")
	if err != nil {
		t.Fatalf("GenerateInstallerIgnition() error = %v", err)
	}
	got := string(out)

	if !strings.Contains(got, "Conflicts=getty@tty1.service") {
		t.Errorf("uCore installer ignition must claim tty1 from the autologin getty; got:\n%s", got)
	}
	if !strings.Contains(got, "Before=getty@tty1.service") {
		t.Errorf("uCore installer ignition must order itself before getty@tty1.service; got:\n%s", got)
	}
	if strings.Contains(got, "Knuckle Flatcar Installer") {
		t.Errorf("uCore must not use the Flatcar service unit; got:\n%s", got)
	}
}

// Every CoreOS-derivative live image autologins the core user on tty1 and so
// needs the getty conflict; the predicate must not drift per-OS.
func TestGenerateInstallerIgnition_CoreOSFamilySharesUnit(t *testing.T) {
	for _, os := range []string{model.OSFCOS, model.OSUcore} {
		out, err := GenerateInstallerIgnition(os, "")
		if err != nil {
			t.Fatalf("GenerateInstallerIgnition(%q) error = %v", os, err)
		}
		if !strings.Contains(string(out), "Conflicts=getty@tty1.service") {
			t.Errorf("os=%q should get the getty conflict", os)
		}
	}

	// Flatcar live does not autologin on tty1, so it must not carry it.
	out, err := GenerateInstallerIgnition(model.OSFlatcar, "")
	if err != nil {
		t.Fatalf("GenerateInstallerIgnition(flatcar) error = %v", err)
	}
	if strings.Contains(string(out), "Conflicts=getty@tty1.service") {
		t.Errorf("Flatcar live does not autologin on tty1; got:\n%s", out)
	}
}

func TestGenerateInstallerIgnition_UcoreWithSSHKey(t *testing.T) {
	out, err := GenerateInstallerIgnition(model.OSUcore, "ssh-ed25519 AAAA test")
	if err != nil {
		t.Fatalf("GenerateInstallerIgnition() error = %v", err)
	}
	got := string(out)

	if !strings.Contains(got, "ssh-ed25519 AAAA test") {
		t.Errorf("expected the SSH key on the core user; got:\n%s", got)
	}
	if !strings.Contains(got, "knuckle-installer.service") {
		t.Errorf("expected the knuckle installer unit; got:\n%s", got)
	}
}
