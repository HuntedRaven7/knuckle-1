package install

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/projectbluefin/knuckle/internal/ignition"
	"github.com/projectbluefin/knuckle/internal/model"
	"github.com/projectbluefin/knuckle/internal/runner"
)

// UcoreInstaller installs uCore via coreos-installer.
//
// # Why this shells out to coreos-installer at all
//
// uCore ships no ISO and no installer of its own — it is an OCI image that
// extends Fedora CoreOS. The deployment is therefore laid down as stock Fedora
// CoreOS, and the generated Ignition carries a oneshot unit that rebases it
// onto the uCore OCI image on the installed system's first boot. That unit is
// the only thing that makes the result uCore rather than CoreOS.
//
// # Stream handling
//
// The Fedora CoreOS base stream is derived from the uCore stream
// (model.UcoreBaseStream) rather than from cfg.Channel, because uCore's stream
// vocabulary includes "lts", which has no FCOS equivalent.
type UcoreInstaller struct {
	Runner       runner.Runner
	Generator    *ignition.Generator
	Logger       *slog.Logger
	ignitionPath string // dynamically set temp file path
}

// NewUcoreInstaller creates an UcoreInstaller with the given runner and logger.
func NewUcoreInstaller(r runner.Runner, logger *slog.Logger) *UcoreInstaller {
	return &UcoreInstaller{
		Runner:    r,
		Generator: ignition.NewGenerator(),
		Logger:    logger,
	}
}

// Install performs the uCore installation:
//  1. Generate uCore Butane → compile Ignition (GenerateUcoreButane)
//  2. Write ignition to a secure temp file
//  3. Run: coreos-installer install --stream <derived> --ignition-file <path> <disk>
//  4. The installed system rebases onto the uCore OCI image on its first boot
func (i *UcoreInstaller) Install(ctx context.Context, cfg *model.InstallConfig, progress func(step string)) error {
	if cfg == nil {
		return fmt.Errorf("install config cannot be nil")
	}

	if cfg.Version != "" {
		// uCore images are tagged by stream, not by pinned release, and
		// coreos-installer has no equivalent of flatcar-install's -V.
		i.Logger.Warn("uCore version pinning is not supported; using the stream default",
			"requested_version", cfg.Version)
	}

	uc := cfg.Ucore.WithDefaults()

	if cfg.IgnitionURL != "" {
		// External Ignition mode means the user owns provisioning entirely, so
		// knuckle's rebase unit is not applied and the machine stays on stock
		// CoreOS unless their config performs the rebase. Say so loudly — the
		// failure mode is a machine that looks installed but is not uCore.
		i.Logger.Warn("external Ignition config supplied for a uCore install: "+
			"knuckle's uCore rebase unit is NOT applied — the installed system "+
			"will remain stock Fedora CoreOS unless the supplied config rebases "+
			"it onto the uCore image itself",
			"image_reference", uc.ImageReference())
		progress("Using external Ignition config...")
	} else {
		progress("Generating Butane config...")
		butaneYAML, err := i.Generator.GenerateUcoreButane(cfg)
		if err != nil {
			return fmt.Errorf("generating uCore butane config: %w", err)
		}

		progress("Compiling Ignition config...")
		ignitionJSON, err := compileToIgnitionFunc(butaneYAML)
		if err != nil {
			return fmt.Errorf("compiling butane: %w", err)
		}

		progress("Writing Ignition config...")
		ignPath, err := i.writeIgnitionFile(ignitionJSON)
		if err != nil {
			return fmt.Errorf("writing ignition file: %w", err)
		}
		i.ignitionPath = ignPath
		defer i.cleanupIgnitionFile()
	}

	stream := model.UcoreBaseStream(uc.Stream)
	args := buildCoreOSInstallArgs(stream, cfg.IgnitionURL, i.ignitionPath, installDiskPath(cfg))

	progress("Running coreos-installer...")
	i.Logger.Info("executing coreos-installer",
		"args", args, "ucore_image", uc.ImageReference(), "base_stream", stream)

	result, err := i.Runner.Run(ctx, "coreos-installer", args...)
	if err != nil || (result != nil && result.ExitCode != 0) {
		return formatCommandError("coreos-installer install failed", result, err)
	}

	progress("Installation complete — the target will become uCore on first boot!")
	return nil
}

// writeIgnitionFile writes ignition JSON to a secure temp file and returns
// its path. Shares the implementation with the FCOS installer so both paths
// have identical secret-handling behaviour.
func (i *UcoreInstaller) writeIgnitionFile(ignitionJSON string) (string, error) {
	return writeIgnitionTempFile(ignitionJSON, i.Logger)
}

// cleanupIgnitionFile removes the temp ignition file (it contains SSH keys).
func (i *UcoreInstaller) cleanupIgnitionFile() {
	if i.ignitionPath == "" {
		return
	}
	if err := removeIgnitionFile(i.ignitionPath); err != nil {
		i.Logger.Warn("failed to clean up uCore ignition file", "path", i.ignitionPath, "error", err)
	}
	i.ignitionPath = ""
}
