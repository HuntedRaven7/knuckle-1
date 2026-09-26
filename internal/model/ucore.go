package model

// uCore (https://github.com/ublue-os/ucore) is an OCI image of Fedora CoreOS
// with extra tooling pre-installed (cockpit, tailscale, ZFS, Samba, mergerfs…).
//
// # Why uCore has no ISO
//
// uCore publishes no installer ISO. Its GitHub releases are changelog notes
// only — the "assets" array is empty on every release — and the binaries are OCI
// images on ghcr.io. The project states plainly that it "does not provide its
// own custom or GUI installer".
//
// So knuckle installs uCore the way uCore's own documentation recommends:
// coreos-installer lays down a stock Fedora CoreOS deployment, and an Ignition
// oneshot unit rebases that deployment onto the uCore OCI image on first boot.
// The live installer ISO is therefore the stock FCOS live ISO that uCore
// already extends; only the *installed* system becomes uCore.

// UcoreRegistry is the OCI registry namespace uCore publishes to.
const UcoreRegistry = "ghcr.io/ublue-os"

// uCore image families. These are the distinct repositories under the
// ublue-os namespace; every one of them ships the same stream/variant tag
// matrix, so they combine as "<image>:<stream>[-<nvidia>]".
const (
	UcoreImageFull    = "ucore"         // cockpit, ZFS, Samba, mergerfs, snapraid — the full server image
	UcoreImageMinimal = "ucore-minimal" // slim container host
	UcoreImageHCI     = "ucore-hci"     // adds libvirt / cockpit-machines
)

// uCore release streams.
const (
	UcoreStreamStable  = "stable"
	UcoreStreamTesting = "testing"
	UcoreStreamLTS     = "lts" // stock FCOS stable base, longterm kernel
)

// uCore NVIDIA variants. These are tag suffixes, not separate repositories:
// ucore:stable-nvidia and ucore:stable-nvidia-lts are the same image with the
// open driver and the LTS driver respectively baked in.
const (
	UcoreNvidiaOpen = "nvidia"
	UcoreNvidiaLTS  = "nvidia-lts"
)

// uCore image signature modes, selecting the rpm-ostree transport.
//
// The transport is the security boundary: ostree-image-signed enforces the
// image's sigstore signature, ostree-unverified-registry does not. The default
// is the verifying transport, and the zero value of the field normalises to it,
// so an unset field is never accidentally unverified.
const (
	UcoreVerifySigned     = "signed"     // ostree-image-signed:docker:// — sigstore signature enforced
	UcoreVerifyUnverified = "unverified" // ostree-unverified-registry: — signature not checked
)

// UcoreImage describes one entry in the uCore image-family roster.
type UcoreImage struct {
	ID          string
	Label       string
	Description string
}

// UcoreImages is the ordered roster of uCore image families. The order is the
// presentation order of the picker, so the cursor into the rendered list and
// the cursor into this slice must index the same position.
var UcoreImages = []UcoreImage{
	{
		ID:          UcoreImageFull,
		Label:       "uCore",
		Description: "Full server image: cockpit, ZFS, Samba, mergerfs, snapraid, rclone. Recommended for home servers and NAS.",
	},
	{
		ID:          UcoreImageMinimal,
		Label:       "uCore minimal",
		Description: "Slim container host: cockpit, tailscale, ZFS, tmux. Fewest packages, smallest footprint.",
	},
	{
		ID:          UcoreImageHCI,
		Label:       "uCore HCI",
		Description: "Full image plus libvirt and cockpit-machines. For hosts that run virtual machines.",
	},
}

// UcoreStreams is the ordered roster of uCore release streams.
var UcoreStreams = []string{UcoreStreamStable, UcoreStreamTesting, UcoreStreamLTS}

// UcoreNvidiaVariants is the ordered roster of uCore NVIDIA tag suffixes. The
// empty string is first and means "no NVIDIA driver".
var UcoreNvidiaVariants = []string{"", UcoreNvidiaOpen, UcoreNvidiaLTS}

// UcoreConfig selects which uCore OCI image the installed system is rebased
// onto at first boot.
//
// It is only consulted when InstallConfig.OS is OSUcore. For that OS, Channel
// is unused: the Fedora CoreOS base stream is derived from Stream so there is
// exactly one stream knob rather than two that can disagree.
type UcoreConfig struct {
	// Image is the image family: UcoreImageFull, UcoreImageMinimal or UcoreImageHCI.
	Image string
	// Stream is the release stream: UcoreStreamStable, UcoreStreamTesting or UcoreStreamLTS.
	Stream string
	// Nvidia is the NVIDIA tag suffix: "" (none), UcoreNvidiaOpen or UcoreNvidiaLTS.
	Nvidia string
	// Verify selects the rpm-ostree transport: UcoreVerifySigned (default) or
	// UcoreVerifyUnverified.
	Verify string
}

// WithDefaults returns a copy of u with every empty field replaced by its
// default, so downstream code can read the fields without re-deriving defaults
// at each use site.
func (u UcoreConfig) WithDefaults() UcoreConfig {
	if u.Image == "" {
		u.Image = UcoreImageFull
	}
	if u.Stream == "" {
		u.Stream = UcoreStreamStable
	}
	if u.Verify == "" {
		u.Verify = UcoreVerifySigned
	}
	return u
}

// Tag returns the uCore container tag, i.e. the "<stream>" or
// "<stream>-<nvidia>" half of the image reference.
func (u UcoreConfig) Tag() string {
	u = u.WithDefaults()
	if u.Nvidia == "" {
		return u.Stream
	}
	return u.Stream + "-" + u.Nvidia
}

// ImageReference returns the full rpm-ostree image reference to rebase onto,
// e.g. "ostree-image-signed:docker://ghcr.io/ublue-os/ucore:stable-nvidia-lts".
//
// The transport prefix carries the security meaning: the signed transport
// verifies the image signature, the unverified one does not.
func (u UcoreConfig) ImageReference() string {
	u = u.WithDefaults()
	transport := "ostree-unverified-registry:"
	if u.Verify == UcoreVerifySigned {
		transport = "ostree-image-signed:docker://"
	}
	return transport + UcoreRegistry + "/" + u.Image + ":" + u.Tag()
}

// UcoreBaseStream returns the Fedora CoreOS stream that coreos-installer should
// lay down before the rebase, for the given uCore stream.
//
// The two vocabularies overlap but are not identical: uCore publishes an "lts"
// stream that has no FCOS equivalent, because it is FCOS *stable* with a
// longterm kernel. Mapping it to "stable" is what makes the base compatible
// with the image the rebase targets. An unknown or empty stream falls back to
// stable.
func UcoreBaseStream(stream string) string {
	switch stream {
	case UcoreStreamTesting:
		return "testing"
	case UcoreStreamLTS:
		return "stable"
	case UcoreStreamStable:
		return "stable"
	default:
		return "stable"
	}
}

// IsCoreOSDerivative reports whether os is installed by coreos-installer on
// top of a Fedora CoreOS base: FCOS itself, or uCore (which is installed as
// FCOS and then rebased onto the uCore OCI image).
//
// Bluefin DDI is deliberately excluded — it writes a systemd DDI image and
// never invokes coreos-installer, so sharing this predicate would give it
// coreos-installer's flags.
func IsCoreOSDerivative(os string) bool {
	return os == OSFCOS || os == OSUcore
}

// ReleaseLabel returns the release identifier to display for this config.
//
// Flatcar and Bluefin DDI label a build by Channel. uCore publishes its own
// stream vocabulary (including "lts", which Flatcar also has but with a
// different meaning) and ignores Channel entirely, so it must be labelled from
// its own field or the installer would advertise the wrong release.
func (c *InstallConfig) ReleaseLabel() string {
	if c == nil {
		return ""
	}
	if c.OS == OSUcore {
		return c.Ucore.WithDefaults().Stream
	}
	return c.Channel
}
