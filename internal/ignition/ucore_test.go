package ignition

import (
	"strings"
	"testing"

	"github.com/projectbluefin/knuckle/internal/model"
)

func baseUcoreConfig() *model.InstallConfig {
	return &model.InstallConfig{
		OS:       model.OSUcore,
		Hostname: "ucore-node",
		Timezone: "UTC",
		Network:  model.NetworkConfig{Mode: model.NetworkDHCP},
		Users: []model.UserConfig{{
			Username: "core",
			SSHKeys:  []string{"ssh-ed25519 AAAA test"},
		}},
	}
}

func TestGenerateUcoreButane_RebaseUnitAndSentinel(t *testing.T) {
	g := NewGenerator()
	out, err := g.GenerateUcoreButane(baseUcoreConfig())
	if err != nil {
		t.Fatalf("GenerateUcoreButane() error = %v", err)
	}

	// The rebase target is the whole point of the config.
	if !strings.Contains(out, "ostree-image-signed:docker://ghcr.io/ublue-os/ucore:stable") {
		t.Errorf("expected signed default rebase reference, got:\n%s", out)
	}
	if !strings.Contains(out, "rpm-ostree rebase --bypass-driver") {
		t.Error("expected rpm-ostree rebase ExecStart")
	}
	// The sentinel is what stops the unit re-running on every later boot.
	if !strings.Contains(out, "ConditionPathExists=!"+UcoreRebaseStateDir+"/rebased") {
		t.Error("expected ConditionPathExists guarding the sentinel")
	}
	if !strings.Contains(out, "ExecStart=/usr/bin/touch "+UcoreRebaseStateDir+"/rebased") {
		t.Error("expected the unit to record the sentinel after a successful rebase")
	}
	// Ordering matters: rebase, then record, then reboot.
	rebase := strings.Index(out, "rpm-ostree rebase")
	touch := strings.Index(out, "/usr/bin/touch")
	reboot := strings.Index(out, "ExecStart=/usr/bin/systemctl reboot")
	if rebase > touch || touch > reboot {
		t.Errorf("ExecStart order is wrong: rebase=%d touch=%d reboot=%d", rebase, touch, reboot)
	}
	// The directory the sentinel lives in must be created.
	if !strings.Contains(out, "directories:") || !strings.Contains(out, UcoreRebaseStateDir) {
		t.Errorf("expected the sentinel directory to be created, got:\n%s", out)
	}
}

func TestGenerateUcoreButane_HonoursImageVariant(t *testing.T) {
	tests := []struct {
		name string
		cfg  model.UcoreConfig
		want string
	}{
		{
			name: "minimal with nvidia lts on testing",
			cfg:  model.UcoreConfig{Image: model.UcoreImageMinimal, Stream: model.UcoreStreamTesting, Nvidia: model.UcoreNvidiaLTS},
			want: "ostree-image-signed:docker://ghcr.io/ublue-os/ucore-minimal:testing-nvidia-lts",
		},
		{
			name: "hci on lts",
			cfg:  model.UcoreConfig{Image: model.UcoreImageHCI, Stream: model.UcoreStreamLTS},
			want: "ostree-image-signed:docker://ghcr.io/ublue-os/ucore-hci:lts",
		},
		{
			name: "explicit unverified",
			cfg:  model.UcoreConfig{Verify: model.UcoreVerifyUnverified},
			want: "ostree-unverified-registry:ghcr.io/ublue-os/ucore:stable",
		},
	}

	g := NewGenerator()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseUcoreConfig()
			cfg.Ucore = tt.cfg
			out, err := g.GenerateUcoreButane(cfg)
			if err != nil {
				t.Fatalf("GenerateUcoreButane() error = %v", err)
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("expected reference %q in:\n%s", tt.want, out)
			}
		})
	}
}

// uCore disables zincati and updates through rpm-ostreed/bootc, and Flatcar
// bakery sysexts cannot load on a Fedora kernel. Neither belongs in the config.
func TestGenerateUcoreButane_OmitsZincatiAndSysexts(t *testing.T) {
	g := NewGenerator()
	cfg := baseUcoreConfig()
	cfg.Sysexts = []model.SysextEntry{{
		Name:     "docker",
		URL:      "https://example.invalid/docker.raw",
		Selected: true,
	}}
	cfg.UpdateStrategy = model.UpdateStrategy{FCOSUpdateStrategy: model.FCOSStrategyImmediate}

	out, err := g.GenerateUcoreButane(cfg)
	if err != nil {
		t.Fatalf("GenerateUcoreButane() error = %v", err)
	}
	if strings.Contains(out, "zincati") {
		t.Errorf("uCore must not configure zincati, got:\n%s", out)
	}
	if strings.Contains(out, "systemd-sysext.service") {
		t.Errorf("uCore must not install Flatcar sysexts, got:\n%s", out)
	}
	if strings.Contains(out, "/etc/extensions/docker.raw") {
		t.Errorf("uCore must not write sysext images, got:\n%s", out)
	}
}

// The uCore config reuses the shared FCOS-base provisioning: hostname, users,
// SSH hardening, and the fcos Butane variant.
func TestGenerateUcoreButane_SharesFCOSProvisioning(t *testing.T) {
	g := NewGenerator()
	out, err := g.GenerateUcoreButane(baseUcoreConfig())
	if err != nil {
		t.Fatalf("GenerateUcoreButane() error = %v", err)
	}
	if !strings.Contains(out, "variant: fcos") {
		t.Error("expected the fcos variant header")
	}
	for _, want := range []string{"/etc/hostname", "ucore-node", "name: \"core\"", "99-knuckle-hardening.conf"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in:\n%s", want, out)
		}
	}
}

func TestGenerateUcoreButane_NilConfig(t *testing.T) {
	if _, err := NewGenerator().GenerateUcoreButane(nil); err == nil {
		t.Error("expected an error for a nil config")
	}
}

// A bad image selection must fail generation, not be baked into a unit that
// runs unattended at first boot.
func TestGenerateUcoreButane_RejectsInvalidSelection(t *testing.T) {
	tests := []struct {
		name string
		cfg  model.UcoreConfig
	}{
		{"unknown image", model.UcoreConfig{Image: "ucore-tiny"}},
		{"unknown stream", model.UcoreConfig{Stream: "next"}},
		{"unknown nvidia variant", model.UcoreConfig{Nvidia: "nvidia-open"}},
		{"unknown verify mode", model.UcoreConfig{Verify: "maybe"}},
	}

	g := NewGenerator()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseUcoreConfig()
			cfg.Ucore = tt.cfg
			if _, err := g.GenerateUcoreButane(cfg); err == nil {
				t.Errorf("GenerateUcoreButane(%+v) = nil error, want rejection", tt.cfg)
			}
		})
	}
}

// The config is handed to a real Butane compiler before it reaches a disk, so a
// malformed fragment must be caught here rather than at install time.
func TestGenerateUcoreButane_CompilesToIgnition(t *testing.T) {
	g := NewGenerator()
	out, err := g.GenerateUcoreButane(baseUcoreConfig())
	if err != nil {
		t.Fatalf("GenerateUcoreButane() error = %v", err)
	}

	ignJSON, err := CompileToIgnition(out)
	if err != nil {
		t.Fatalf("CompileToIgnition() error = %v\nbutane:\n%s", err, out)
	}
	if len(ignJSON) == 0 {
		t.Fatal("CompileToIgnition returned empty output")
	}
}

// The generator is the last point where a malformed fragment can be caught
// before it is compiled and written to the target disk, so both template
// error paths must surface rather than emitting a broken config.
func TestGenerateUcoreButane_RebaseTemplateError(t *testing.T) {
	orig := ucoreAutorebaseTemplate
	ucoreAutorebaseTemplate = `{{ .ImageReference `
	t.Cleanup(func() { ucoreAutorebaseTemplate = orig })

	if _, err := NewGenerator().GenerateUcoreButane(baseUcoreConfig()); err == nil {
		t.Error("expected an error when the rebase unit template is malformed")
	}
}

func TestGenerateUcoreButane_SharedFragmentError(t *testing.T) {
	orig := hostnameTemplate
	hostnameTemplate = `{{ .Hostname `
	t.Cleanup(func() { hostnameTemplate = orig })

	if _, err := NewGenerator().GenerateUcoreButane(baseUcoreConfig()); err == nil {
		t.Error("expected an error when a shared fragment template is malformed")
	}
}

func TestBuilderAddStorageDirectory(t *testing.T) {
	b := NewBuilder(&model.InstallConfig{})
	b.AddStorageDirectory("- path: /var/lib/thing\n  mode: 0755")
	got := b.BuildFCOS()

	if !strings.Contains(got, "directories:") {
		t.Errorf("expected a directories key, got:\n%s", got)
	}
	// Directories are emitted before files so a file can land in a directory
	// created by the same Ignition run.
	dir := strings.Index(got, "directories:")
	_ = dir
	b.AddStorageFile("- path: /var/lib/thing/file\n  mode: 0644")
	got = b.BuildFCOS()
	if strings.Index(got, "directories:") > strings.Index(got, "files:") {
		t.Errorf("directories must be emitted before files, got:\n%s", got)
	}
}
