package headless

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/projectbluefin/knuckle/internal/model"
)

func ucoreHeadlessConfig() *Config {
	return &Config{
		OS:       model.OSUcore,
		Hostname: "ucore-node",
		Timezone: "UTC",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAA test"}}},
		Disk:     "/dev/vdb",
	}
}

func TestUcoreConfigToModel(t *testing.T) {
	tests := []struct {
		name string
		in   *UcoreConfig
		want model.UcoreConfig
	}{
		{
			name: "nil takes every default",
			in:   nil,
			want: model.UcoreConfig{Image: model.UcoreImageFull, Stream: model.UcoreStreamStable, Nvidia: "", Verify: model.UcoreVerifySigned},
		},
		{
			name: "empty struct takes every default",
			in:   &UcoreConfig{},
			want: model.UcoreConfig{Image: model.UcoreImageFull, Stream: model.UcoreStreamStable, Nvidia: "", Verify: model.UcoreVerifySigned},
		},
		{
			name: "explicit values pass through",
			in:   &UcoreConfig{Image: model.UcoreImageHCI, Stream: model.UcoreStreamLTS, Nvidia: model.UcoreNvidiaLTS, Verify: model.UcoreVerifyUnverified},
			want: model.UcoreConfig{Image: model.UcoreImageHCI, Stream: model.UcoreStreamLTS, Nvidia: model.UcoreNvidiaLTS, Verify: model.UcoreVerifyUnverified},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.ToModel(); got != tt.want {
				t.Errorf("ToModel() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// A headless config that omits the whole ucore block must still resolve to the
// documented defaults rather than an empty image reference.
func TestToInstallConfigUcoreDefaults(t *testing.T) {
	cfg := ucoreHeadlessConfig()
	ic := cfg.ToInstallConfig()

	if ic.OS != model.OSUcore {
		t.Fatalf("OS = %q, want %q", ic.OS, model.OSUcore)
	}
	want := model.UcoreConfig{Image: model.UcoreImageFull, Stream: model.UcoreStreamStable, Nvidia: "", Verify: model.UcoreVerifySigned}
	if ic.Ucore != want {
		t.Errorf("Ucore = %+v, want %+v", ic.Ucore, want)
	}
	if got := ic.Ucore.ImageReference(); !strings.Contains(got, "ghcr.io/ublue-os/ucore:stable") {
		t.Errorf("ImageReference() = %q, want the default full stable image", got)
	}
}

func TestToInstallConfigUcoreRoundTrip(t *testing.T) {
	cfg := ucoreHeadlessConfig()
	cfg.Ucore = &UcoreConfig{
		Image:  model.UcoreImageMinimal,
		Stream: model.UcoreStreamTesting,
		Nvidia: model.UcoreNvidiaOpen,
		Verify: model.UcoreVerifyUnverified,
	}

	ic := cfg.ToInstallConfig()
	if ic.Ucore.Image != model.UcoreImageMinimal || ic.Ucore.Stream != model.UcoreStreamTesting ||
		ic.Ucore.Nvidia != model.UcoreNvidiaOpen || ic.Ucore.Verify != model.UcoreVerifyUnverified {
		t.Fatalf("Ucore = %+v, want the values from the config", ic.Ucore)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

// The JSON field names are the public headless contract.
func TestUcoreConfigJSONRoundTrip(t *testing.T) {
	raw := []byte(`{
		"os": "ucore",
		"hostname": "ucore-node",
		"network": {"mode": "dhcp"},
		"users": [{"username": "core", "ssh_keys": ["ssh-ed25519 AAAA test"]}],
		"disk": "/dev/vdb",
		"ucore": {"image": "ucore-hci", "stream": "lts", "nvidia": "nvidia-lts", "verify": "unverified"}
	}`)

	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg.Ucore == nil {
		t.Fatal("ucore block did not decode")
	}
	if cfg.Ucore.Image != model.UcoreImageHCI || cfg.Ucore.Stream != model.UcoreStreamLTS ||
		cfg.Ucore.Nvidia != model.UcoreNvidiaLTS || cfg.Ucore.Verify != model.UcoreVerifyUnverified {
		t.Fatalf("Ucore = %+v, want the decoded values", cfg.Ucore)
	}

	out, err := json.Marshal(cfg.Ucore)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, key := range []string{`"image"`, `"stream"`, `"nvidia"`, `"verify"`} {
		if !strings.Contains(string(out), key) {
			t.Errorf("marshalled uCore config is missing %s: %s", key, out)
		}
	}
}

func TestValidateUcoreImageSelection(t *testing.T) {
	tests := []struct {
		name   string
		ucore  *UcoreConfig
		wantOK bool
	}{
		{"nil block is valid", nil, true},
		{"empty block is valid", &UcoreConfig{}, true},
		{"full selection is valid", &UcoreConfig{Image: model.UcoreImageFull, Stream: model.UcoreStreamLTS, Nvidia: model.UcoreNvidiaOpen, Verify: model.UcoreVerifySigned}, true},
		{"unknown image", &UcoreConfig{Image: "ucore-tiny"}, false},
		{"fc stream is not a ucore stream", &UcoreConfig{Stream: "next"}, false},
		{"flatcar channel is not a ucore stream", &UcoreConfig{Stream: "beta"}, false},
		{"unknown nvidia variant", &UcoreConfig{Nvidia: "570-open"}, false},
		{"unknown verify mode", &UcoreConfig{Verify: "yes"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ucoreHeadlessConfig()
			cfg.Ucore = tt.ucore
			err := cfg.Validate()
			if tt.wantOK && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
			if !tt.wantOK {
				if err == nil {
					t.Error("Validate() = nil, want an error")
				} else if !strings.HasPrefix(err.Error(), "ucore.") {
					t.Errorf("error = %q, want it to name the ucore field", err)
				}
			}
		})
	}
}

// "ucore" is a roster member, so the headless os check must accept it — and the
// acceptance list must not drift from the roster again.
func TestValidateAcceptsEveryRosterOS(t *testing.T) {
	for _, id := range model.OSTargetIDs() {
		cfg := ucoreHeadlessConfig()
		cfg.OS = id
		cfg.Channel = "stable"
		switch id {
		case model.OSFCOS:
			cfg.Channel = "stable"
		case model.OSUcore:
			cfg.Ucore = &UcoreConfig{Stream: model.UcoreStreamStable}
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() for os=%q = %v, want nil — the roster and the validator disagree", id, err)
		}
	}
}

func TestValidateRejectsUnknownOS(t *testing.T) {
	cfg := ucoreHeadlessConfig()
	cfg.OS = "nixos"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected an unknown os to be rejected")
	}
	// The message should enumerate what is accepted.
	if !strings.Contains(err.Error(), model.OSUcore) {
		t.Errorf("error %q should list the accepted os values including ucore", err)
	}
}

// uCore has no etcd-lock: it does not run zincati.
func TestValidateUcoreRejectsEtcdLock(t *testing.T) {
	cfg := ucoreHeadlessConfig()
	cfg.UpdateStrategy = "etcd-lock"
	if err := cfg.Validate(); err == nil {
		t.Error("expected etcd-lock to be rejected for uCore")
	}
}

// uCore images are stream-tagged, so a Flatcar version pin is meaningless.
func TestValidateUcoreIgnoresFlatcarVersion(t *testing.T) {
	cfg := ucoreHeadlessConfig()
	cfg.Version = "not-a-flatcar-version"
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil — FCOS-style version strings are accepted for uCore", err)
	}
}

// uCore's lts stream ships on arm64, unlike Flatcar's lts channel.
func TestValidateUcoreLTSOnArm64(t *testing.T) {
	cfg := ucoreHeadlessConfig()
	cfg.Arch = "arm64"
	cfg.Channel = "lts"
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil — uCore lts is available on arm64", err)
	}
}
