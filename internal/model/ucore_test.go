package model

import "testing"

// uCore has no ISO, so the image reference knuckle rebases onto is the single
// source of truth for "which uCore is this" — these tests pin the derivation.

func TestUcoreWithDefaults(t *testing.T) {
	tests := []struct {
		name string
		in   UcoreConfig
		want UcoreConfig
	}{
		{
			name: "zero value defaults to full stable signed",
			in:   UcoreConfig{},
			want: UcoreConfig{Image: UcoreImageFull, Stream: UcoreStreamStable, Nvidia: "", Verify: UcoreVerifySigned},
		},
		{
			name: "explicit fields are preserved",
			in:   UcoreConfig{Image: UcoreImageHCI, Stream: UcoreStreamLTS, Nvidia: UcoreNvidiaLTS, Verify: UcoreVerifyUnverified},
			want: UcoreConfig{Image: UcoreImageHCI, Stream: UcoreStreamLTS, Nvidia: UcoreNvidiaLTS, Verify: UcoreVerifyUnverified},
		},
		{
			name: "nvidia without other fields keeps the nvidia choice",
			in:   UcoreConfig{Nvidia: UcoreNvidiaOpen},
			want: UcoreConfig{Image: UcoreImageFull, Stream: UcoreStreamStable, Nvidia: UcoreNvidiaOpen, Verify: UcoreVerifySigned},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.WithDefaults(); got != tt.want {
				t.Errorf("WithDefaults() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestUcoreTag(t *testing.T) {
	tests := []struct {
		name string
		in   UcoreConfig
		want string
	}{
		{"bare", UcoreConfig{}, UcoreStreamStable},
		{"stream only", UcoreConfig{Stream: UcoreStreamTesting}, UcoreStreamTesting},
		{"stream plus nvidia", UcoreConfig{Stream: UcoreStreamLTS, Nvidia: UcoreNvidiaOpen}, "lts-nvidia"},
		{"stream plus nvidia lts", UcoreConfig{Stream: UcoreStreamStable, Nvidia: UcoreNvidiaLTS}, "stable-nvidia-lts"},
		{"nvidia only", UcoreConfig{Nvidia: UcoreNvidiaOpen}, "stable-nvidia"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.Tag(); got != tt.want {
				t.Errorf("Tag() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUcoreImageReference(t *testing.T) {
	tests := []struct {
		name string
		in   UcoreConfig
		want string
	}{
		{
			name: "default is the signature-verified transport",
			in:   UcoreConfig{},
			want: "ostree-image-signed:docker://ghcr.io/ublue-os/ucore:stable",
		},
		{
			name: "unverified drops signature enforcement",
			in:   UcoreConfig{Verify: UcoreVerifyUnverified},
			want: "ostree-unverified-registry:ghcr.io/ublue-os/ucore:stable",
		},
		{
			name: "image and variant compose into one tag",
			in:   UcoreConfig{Image: UcoreImageMinimal, Stream: UcoreStreamTesting, Nvidia: UcoreNvidiaLTS},
			want: "ostree-image-signed:docker://ghcr.io/ublue-os/ucore-minimal:testing-nvidia-lts",
		},
		{
			name: "hci family",
			in:   UcoreConfig{Image: UcoreImageHCI, Stream: UcoreStreamLTS},
			want: "ostree-image-signed:docker://ghcr.io/ublue-os/ucore-hci:lts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.ImageReference(); got != tt.want {
				t.Errorf("ImageReference() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestUcoreZeroValueIsVerified guards the security-relevant default: an unset
// Verify field must never silently produce the unverified transport.
func TestUcoreZeroValueIsVerified(t *testing.T) {
	if got := (UcoreConfig{}).ImageReference(); got[:len("ostree-image-signed:")] != "ostree-image-signed:" {
		t.Errorf("zero-value UcoreConfig produced unverified transport: %q", got)
	}
}

func TestUcoreBaseStream(t *testing.T) {
	tests := []struct {
		stream string
		want   string
	}{
		{UcoreStreamStable, "stable"},
		{UcoreStreamTesting, "testing"},
		// uCore's lts stream is FCOS stable with a longterm kernel, so the
		// base must be stable — there is no FCOS "lts" stream.
		{UcoreStreamLTS, "stable"},
		{"", "stable"},
		{"nonsense", "stable"},
	}

	for _, tt := range tests {
		if got := UcoreBaseStream(tt.stream); got != tt.want {
			t.Errorf("UcoreBaseStream(%q) = %q, want %q", tt.stream, got, tt.want)
		}
	}
}

func TestIsCoreOSDerivative(t *testing.T) {
	tests := []struct {
		os   string
		want bool
	}{
		{OSFCOS, true},
		{OSUcore, true},
		{OSFlatcar, false},
		{OSBluefinDDI, false},
		{"", false},
		{"fcos ", false},
	}

	for _, tt := range tests {
		if got := IsCoreOSDerivative(tt.os); got != tt.want {
			t.Errorf("IsCoreOSDerivative(%q) = %v, want %v", tt.os, got, tt.want)
		}
	}
}

func TestReleaseLabel(t *testing.T) {
	tests := []struct {
		name string
		cfg  *InstallConfig
		want string
	}{
		{
			name: "flatcar labels from channel",
			cfg:  &InstallConfig{OS: OSFlatcar, Channel: "beta"},
			want: "beta",
		},
		{
			name: "ucore labels from its own stream, not channel",
			cfg:  &InstallConfig{OS: OSUcore, Channel: "beta", Ucore: UcoreConfig{Stream: UcoreStreamLTS}},
			want: "lts",
		},
		{
			name: "ucore with unset stream falls back to the default",
			cfg:  &InstallConfig{OS: OSUcore, Channel: "beta"},
			want: "stable",
		},
		{
			name: "bluefin labels from channel",
			cfg:  &InstallConfig{OS: OSBluefinDDI, Channel: "stable"},
			want: "stable",
		},
		{
			name: "nil config is empty",
			cfg:  nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.ReleaseLabel(); got != tt.want {
				t.Errorf("ReleaseLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The pickers in internal/tui index UcoreImages/UcoreStreams/UcoreNvidiaVariants
// with the same cursor they render, so the rosters must be complete and their
// order must be what the UI shows.
// OSDisplayName is used in status and completion sentences, so the empty and
// unknown values must resolve to Flatcar rather than rendering as "".
func TestOSDisplayName(t *testing.T) {
	tests := []struct {
		os   string
		want string
	}{
		{OSFlatcar, "Flatcar Container Linux"},
		{OSFCOS, "Fedora CoreOS"},
		{OSUcore, "uCore"},
		{OSBluefinDDI, "Bluefin Server"},
		{"", "Flatcar Container Linux"},
		{"nixos", "Flatcar Container Linux"},
	}

	for _, tt := range tests {
		if got := OSDisplayName(tt.os); got != tt.want {
			t.Errorf("OSDisplayName(%q) = %q, want %q", tt.os, got, tt.want)
		}
	}
}

// Every roster entry must have a distinct display name, or the install and done
// screens would name two different targets identically.
func TestOSDisplayNamesAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, id := range OSTargetIDs() {
		name := OSDisplayName(id)
		if name == "" {
			t.Errorf("OS %q has an empty display name", id)
			continue
		}
		if prev, dup := seen[name]; dup {
			t.Errorf("OS %q and %q share the display name %q", prev, id, name)
		}
		seen[name] = id
	}
}

// The breadcrumb renders every step's name, so a new step with no case would
// render as "Unknown".
func TestStepUcoreString(t *testing.T) {
	if got := StepUcore.String(); got != "uCore Image" {
		t.Errorf("StepUcore.String() = %q, want %q", got, "uCore Image")
	}
}

func TestUcoreRostersAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for i, img := range UcoreImages {
		if img.ID == "" || img.Label == "" || img.Description == "" {
			t.Errorf("UcoreImages entry %d is incomplete: %+v", i, img)
		}
		if seen[img.ID] {
			t.Errorf("UcoreImages entry %d repeats ID %q", i, img.ID)
		}
		seen[img.ID] = true
	}

	if len(UcoreStreams) == 0 {
		t.Error("UcoreStreams is empty")
	}
	for i, s := range UcoreStreams {
		if s == "" {
			t.Errorf("UcoreStreams entry %d is empty", i)
		}
	}
	// The no-NVIDIA option must be first: it is the default and the cursor
	// starts at 0.
	if len(UcoreNvidiaVariants) == 0 || UcoreNvidiaVariants[0] != "" {
		t.Errorf("UcoreNvidiaVariants must lead with the no-NVIDIA option, got %v", UcoreNvidiaVariants)
	}
}
