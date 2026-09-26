package validate

import (
	"strings"
	"testing"

	"github.com/projectbluefin/knuckle/internal/model"
)

func TestUcoreStream(t *testing.T) {
	for _, s := range model.UcoreStreams {
		if err := UcoreStream(s); err != nil {
			t.Errorf("UcoreStream(%q) = %v, want nil", s, err)
		}
	}
	// "next" is an FCOS stream, not a uCore one. "beta"/"alpha"/"edge" are
	// Flatcar channels. None of them are valid uCore streams.
	for _, s := range []string{"next", "beta", "alpha", "edge", "", "stable "} {
		if err := UcoreStream(s); err == nil {
			t.Errorf("UcoreStream(%q) = nil, want error", s)
		}
	}
}

func TestUcoreStreamErrorListsValidStreams(t *testing.T) {
	err := UcoreStream("nope")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, s := range model.UcoreStreams {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error %q does not mention valid stream %q", err, s)
		}
	}
}

func TestUcoreImage(t *testing.T) {
	for _, img := range model.UcoreImages {
		if err := UcoreImage(img.ID); err != nil {
			t.Errorf("UcoreImage(%q) = %v, want nil", img.ID, err)
		}
	}
	for _, s := range []string{"", "ucore-hci-minimal", "UCORE", "fedora-coreos"} {
		if err := UcoreImage(s); err == nil {
			t.Errorf("UcoreImage(%q) = nil, want error", s)
		}
	}
}

func TestUcoreImageErrorListsValidImages(t *testing.T) {
	err := UcoreImage("nope")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, img := range model.UcoreImages {
		if !strings.Contains(err.Error(), img.ID) {
			t.Errorf("error %q does not mention valid image %q", err, img.ID)
		}
	}
}

// The empty string must be accepted: it is the "no NVIDIA driver" choice and
// the default the cursor starts on.
func TestUcoreNvidiaAcceptsEmpty(t *testing.T) {
	if err := UcoreNvidia(""); err != nil {
		t.Errorf("UcoreNvidia(\"\") = %v, want nil", err)
	}
	for _, v := range []string{model.UcoreNvidiaOpen, model.UcoreNvidiaLTS} {
		if err := UcoreNvidia(v); err != nil {
			t.Errorf("UcoreNvidia(%q) = %v, want nil", v, err)
		}
	}
	for _, s := range []string{"nvidia-open", "570-open", "open", "Nvidia"} {
		if err := UcoreNvidia(s); err == nil {
			t.Errorf("UcoreNvidia(%q) = nil, want error", s)
		}
	}
}

// CheckConsistency is the gate the wizard's Review step and headless Run share,
// so the uCore rules it enforces must be pinned here.
func TestCheckConsistencyUcore(t *testing.T) {
	// A minimal config that passes every other consistency check.
	base := func() *model.InstallConfig {
		return &model.InstallConfig{
			OS:       model.OSUcore,
			Hostname: "ucore-node",
			Network:  model.NetworkConfig{Mode: model.NetworkDHCP},
			Disk:     model.DiskInfo{DevPath: "/dev/sda"},
			Users:    []model.UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAA test"}}},
			Ucore:    model.UcoreConfig{Image: model.UcoreImageFull, Stream: model.UcoreStreamStable, Nvidia: "", Verify: model.UcoreVerifySigned},
		}
	}

	t.Run("defaults are accepted", func(t *testing.T) {
		cfg := base()
		cfg.Ucore = model.UcoreConfig{} // every field omitted
		if err := CheckConsistency(cfg); err != nil {
			t.Errorf("CheckConsistency() = %v, want nil for a defaulted uCore config", err)
		}
	})

	for _, tt := range []struct {
		name string
		cfg  model.UcoreConfig
	}{
		{"bad image", model.UcoreConfig{Image: "ucore-tiny"}},
		{"bad stream", model.UcoreConfig{Stream: "next"}},
		{"bad nvidia variant", model.UcoreConfig{Nvidia: "570-open"}},
		{"bad verify mode", model.UcoreConfig{Verify: "yes"}},
	} {
		t.Run(tt.name+" is rejected", func(t *testing.T) {
			cfg := base()
			cfg.Ucore = tt.cfg
			if err := CheckConsistency(cfg); err == nil {
				t.Errorf("CheckConsistency(%+v) = nil, want an error", tt.cfg)
			}
		})
	}

	// uCore bakes its driver into the image tag, so the Flatcar-only field
	// must be refused rather than silently ignored.
	t.Run("nvidia_driver_version is rejected", func(t *testing.T) {
		cfg := base()
		cfg.NvidiaDriverVersion = "570-open"
		err := CheckConsistency(cfg)
		if err == nil {
			t.Fatal("expected nvidia_driver_version to be rejected for uCore")
		}
		if !strings.Contains(err.Error(), "uCore") {
			t.Errorf("error %q should point at the uCore image variant", err)
		}
	})

	// The uCore image rules must not leak onto other OS targets, which do use
	// the Flatcar NVIDIA field.
	t.Run("flatcar keeps nvidia_driver_version", func(t *testing.T) {
		cfg := base()
		cfg.OS = model.OSFlatcar
		cfg.Channel = "stable"
		cfg.NvidiaDriverVersion = "570-open"
		cfg.Ucore = model.UcoreConfig{Image: "nonsense"}
		if err := CheckConsistency(cfg); err != nil {
			t.Errorf("CheckConsistency() = %v, want nil — uCore rules must not apply to Flatcar", err)
		}
	})
}

func TestUcoreVerifyMode(t *testing.T) {
	for _, v := range []string{model.UcoreVerifySigned, model.UcoreVerifyUnverified} {
		if err := UcoreVerifyMode(v); err != nil {
			t.Errorf("UcoreVerifyMode(%q) = %v, want nil", v, err)
		}
	}
	// The empty string is rejected here even though WithDefaults() fills it
	// in: an unset verify mode is a caller bug, not a valid choice, and
	// silently defaulting it would hide it.
	for _, s := range []string{"", "yes", "true", "Signed"} {
		if err := UcoreVerifyMode(s); err == nil {
			t.Errorf("UcoreVerifyMode(%q) = nil, want error", s)
		}
	}
}
