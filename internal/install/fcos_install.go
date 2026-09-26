package install

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/projectbluefin/knuckle/internal/ignition"
	"github.com/projectbluefin/knuckle/internal/model"
	"github.com/projectbluefin/knuckle/internal/runner"
)

// FCOSInstaller runs coreos-installer via the runner.
//
// coreos-installer differs from flatcar-install in three key ways:
//   - Disk path is a positional argument after the "install" subcommand.
//   - Stream is passed as --stream rather than -C.
//   - Disk preparation (wipefs, GPT repair) is handled internally — callers
//     must NOT invoke wipefs or sfdisk before or after coreos-installer.
type FCOSInstaller struct {
	Runner       runner.Runner
	Generator    *ignition.Generator
	Logger       *slog.Logger
	ignitionPath string // dynamically set temp file path
}

// NewFCOSInstaller creates an FCOSInstaller with the given runner and logger.
func NewFCOSInstaller(r runner.Runner, logger *slog.Logger) *FCOSInstaller {
	return &FCOSInstaller{
		Runner:    r,
		Generator: ignition.NewGenerator(),
		Logger:    logger,
	}
}

// Install performs the FCOS installation:
//  1. Generate FCOS Butane → compile Ignition (uses GenerateFCOSButane)
//  2. Write ignition to secure temp file (reuses WriteIgnitionFile)
//  3. Run: coreos-installer install --stream <stream> [--ignition-file <path>] <disk>
//  4. Done — no wipefs or sfdisk steps.
func (i *FCOSInstaller) Install(ctx context.Context, cfg *model.InstallConfig, progress func(step string)) error {
	if cfg == nil {
		return fmt.Errorf("install config cannot be nil")
	}

	if cfg.Version != "" {
		// coreos-installer does not support a -V equivalent for stream-based
		// version pinning; pinning requires constructing an explicit --image-url.
		// For v1 we log a warning and proceed with the stream default.
		// See docs/HEADLESS-CONFIG.md §FCOS version pinning.
		i.Logger.Warn("FCOS version pinning is not supported in v1; using stream default",
			"requested_version", cfg.Version)
	}

	if cfg.IgnitionURL != "" {
		progress("Using external Ignition config...")
	} else {
		progress("Generating Butane config...")
		butaneYAML, err := i.Generator.GenerateFCOSButane(cfg)
		if err != nil {
			return fmt.Errorf("generating fcos butane config: %w", err)
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
		defer i.cleanupFCOSIgnitionFile()
	}

	args := buildFCOSInstallArgs(cfg, i.ignitionPath)

	progress("Running coreos-installer...")
	i.Logger.Info("executing coreos-installer", "args", args)

	result, err := i.Runner.Run(ctx, "coreos-installer", args...)
	if err != nil || (result != nil && result.ExitCode != 0) {
		return formatCommandError("coreos-installer install failed", result, err)
	}

	progress("Installation complete!")
	return nil
}

// buildFCOSInstallArgs constructs the argument list for coreos-installer,
// taking the stream from the config's channel.
// The disk path is a positional argument that must come last.
func buildFCOSInstallArgs(cfg *model.InstallConfig, ignitionPath string) []string {
	return buildCoreOSInstallArgs(cfg.Channel, cfg.IgnitionURL, ignitionPath, installDiskPath(cfg))
}

// buildCoreOSInstallArgs constructs the coreos-installer argument list.
//
// stream is passed explicitly rather than read from the config because the
// stream a target needs is not always the config's Channel: uCore publishes its
// own stream vocabulary and derives its Fedora CoreOS base stream from it, so
// that base may differ from whatever Channel happens to hold.
func buildCoreOSInstallArgs(stream, ignitionURL, ignitionPath, diskPath string) []string {
	args := []string{
		"install",
		"--stream", stream,
	}

	if ignitionURL != "" {
		args = append(args, "--ignition-url", ignitionURL)
	} else if ignitionPath != "" {
		args = append(args, "--ignition-file", ignitionPath)
	}

	// Disk path is positional — must be the final argument.
	args = append(args, diskPath)
	return args
}

// writeIgnitionFile writes ignition JSON to a secure temp file and returns
// its path. Delegates to the package-level newIgnitionTempFile seam so tests
// can inject failures.
func (i *FCOSInstaller) writeIgnitionFile(ignitionJSON string) (string, error) {
	return writeIgnitionTempFile(ignitionJSON, i.Logger)
}

// writeIgnitionTempFile writes an Ignition config to a 0600 temp file and
// returns its path.
//
// Ignition configs carry SSH keys and password hashes, so a partial write must
// never be left on disk: both the write and close failure paths remove the
// file before returning. The file is created via newIgnitionTempFile (O_EXCL)
// so a predictable name cannot be pre-created by another user.
func writeIgnitionTempFile(ignitionJSON string, logger *slog.Logger) (string, error) {
	f, err := newIgnitionTempFile()
	if err != nil {
		return "", fmt.Errorf("creating temp ignition file: %w", err)
	}
	path := f.Name()

	if _, err := f.WriteString(ignitionJSON); err != nil {
		_ = f.Close()
		_ = removeIgnitionFile(path)
		return "", fmt.Errorf("writing ignition content: %w", err)
	}

	if err := f.Close(); err != nil {
		_ = removeIgnitionFile(path)
		return "", fmt.Errorf("closing ignition file: %w", err)
	}

	logger.Info("ignition file written", "path", path)
	return path, nil
}

// cleanupFCOSIgnitionFile removes the temp ignition file (contains SSH keys).
func (i *FCOSInstaller) cleanupFCOSIgnitionFile() {
	if i.ignitionPath == "" {
		return
	}
	if err := removeIgnitionFile(i.ignitionPath); err != nil {
		i.Logger.Warn("failed to clean up fcos ignition file", "path", i.ignitionPath, "error", err)
	}
	i.ignitionPath = ""
}
