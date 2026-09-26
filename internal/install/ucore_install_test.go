package install

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/projectbluefin/knuckle/internal/model"
	"github.com/projectbluefin/knuckle/internal/runner"
)

func ucoreConfig() *model.InstallConfig {
	return &model.InstallConfig{
		OS:       model.OSUcore,
		Channel:  "stable",
		Hostname: "ucore-node",
		Disk:     model.DiskInfo{DevPath: "/dev/sda"},
		Network:  model.NetworkConfig{Mode: model.NetworkDHCP},
		Users:    []model.UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAA test"}}},
	}
}

func firstCall(t *testing.T, spy *runner.SpyRunner, name string) *runner.SpyCall {
	t.Helper()
	for i := range spy.Calls {
		if spy.Calls[i].Name == name {
			return &spy.Calls[i]
		}
	}
	t.Fatalf("%s was not called; got: %v", name, spy.Calls)
	return nil
}

func TestUcoreInstall_RunsCoreOSInstaller(t *testing.T) {
	spy := runner.NewSpyRunner()
	installer := NewUcoreInstaller(spy, testLogger())

	if err := installer.Install(context.Background(), ucoreConfig(), func(string) {}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call := firstCall(t, spy, "coreos-installer")
	joined := strings.Join(call.Args, " ")

	if call.Args[0] != "install" {
		t.Errorf("first arg = %q, want %q", call.Args[0], "install")
	}
	if !strings.Contains(joined, "--stream stable") {
		t.Errorf("args = %v, want --stream stable", call.Args)
	}
	// The disk is positional and must be last.
	if call.Args[len(call.Args)-1] != "/dev/sda" {
		t.Errorf("last arg = %q, want the disk path", call.Args[len(call.Args)-1])
	}
	if !strings.Contains(joined, "--ignition-file") {
		t.Errorf("args = %v, want a generated --ignition-file", call.Args)
	}
}

// The base stream must be derived from the uCore stream, not from Channel.
// "lts" is the case that matters: it is a uCore stream with no FCOS equivalent,
// so a base of "lts" would make coreos-installer fail.
func TestUcoreInstall_DerivesBaseStreamFromUcoreStream(t *testing.T) {
	tests := []struct {
		name       string
		ucoreStrem string
		wantStream string
	}{
		{"lts maps onto the FCOS stable base", model.UcoreStreamLTS, "stable"},
		{"testing maps onto testing", model.UcoreStreamTesting, "testing"},
		{"stable maps onto stable", model.UcoreStreamStable, "stable"},
		{"unset maps onto stable", "", "stable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spy := runner.NewSpyRunner()
			cfg := ucoreConfig()
			// A Channel that disagrees with the uCore stream must be ignored.
			cfg.Channel = "beta"
			cfg.Ucore.Stream = tt.ucoreStrem

			if err := NewUcoreInstaller(spy, testLogger()).Install(context.Background(), cfg, func(string) {}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			call := firstCall(t, spy, "coreos-installer")
			if !strings.Contains(strings.Join(call.Args, " "), "--stream "+tt.wantStream) {
				t.Errorf("args = %v, want --stream %s", call.Args, tt.wantStream)
			}
		})
	}
}

// capturingRunner reads the --ignition-file while the command is "running",
// which is the only window in which the temp file exists.
type capturingRunner struct {
	runner.SpyRunner
	ignition string
}

func (c *capturingRunner) Run(ctx context.Context, name string, args ...string) (*runner.Result, error) {
	for i, a := range args {
		if a == "--ignition-file" && i+1 < len(args) {
			if b, err := os.ReadFile(args[i+1]); err == nil {
				c.ignition = string(b)
			}
		}
	}
	return c.SpyRunner.Run(ctx, name, args...)
}

// The generated Ignition is what actually makes the target uCore, so it must
// carry the rebase reference rather than a plain CoreOS config.
func TestUcoreInstall_IgnitionCarriesRebaseReference(t *testing.T) {
	cap := &capturingRunner{}
	cap.Responses = map[string]*runner.Result{}
	cap.Errors = map[string]error{}

	cfg := ucoreConfig()
	cfg.Ucore = model.UcoreConfig{
		Image:  model.UcoreImageMinimal,
		Stream: model.UcoreStreamLTS,
		Nvidia: model.UcoreNvidiaOpen,
	}

	if err := NewUcoreInstaller(cap, testLogger()).Install(context.Background(), cfg, func(string) {}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cap.ignition == "" {
		t.Fatal("no ignition file was passed to coreos-installer")
	}
	want := "ghcr.io/ublue-os/ucore-minimal:lts-nvidia"
	if !strings.Contains(cap.ignition, want) {
		t.Errorf("ignition does not reference %q; got:\n%s", want, cap.ignition)
	}
	// It must be the verifying transport by default.
	if !strings.Contains(cap.ignition, "ostree-image-signed:docker://") {
		t.Errorf("ignition must default to the signature-verified transport; got:\n%s", cap.ignition)
	}
	// And it must actually rebase, not merely mention the image.
	if !strings.Contains(cap.ignition, "rpm-ostree rebase") {
		t.Errorf("ignition has no rebase command; got:\n%s", cap.ignition)
	}
}

func TestUcoreInstall_NilConfig(t *testing.T) {
	spy := runner.NewSpyRunner()
	err := NewUcoreInstaller(spy, testLogger()).Install(context.Background(), nil, func(string) {})
	if err == nil {
		t.Fatal("expected an error for a nil config, got nil")
	}
	if len(spy.Calls) != 0 {
		t.Errorf("no command should run for a nil config, got %v", spy.Calls)
	}
}

func TestUcoreInstall_ButaneError(t *testing.T) {
	spy := runner.NewSpyRunner()
	cfg := ucoreConfig()
	cfg.Ucore.Image = "ucore-nope" // rejected by GenerateUcoreButane

	err := NewUcoreInstaller(spy, testLogger()).Install(context.Background(), cfg, func(string) {})
	if err == nil {
		t.Fatal("expected an error for an invalid uCore image, got nil")
	}
	if len(spy.Calls) != 0 {
		t.Errorf("coreos-installer must not run when config generation fails, got %v", spy.Calls)
	}
}

func TestUcoreInstall_CompileError(t *testing.T) {
	orig := compileToIgnitionFunc
	compileToIgnitionFunc = func(string) (string, error) { return "", errors.New("boom") }
	t.Cleanup(func() { compileToIgnitionFunc = orig })

	spy := runner.NewSpyRunner()
	err := NewUcoreInstaller(spy, testLogger()).Install(context.Background(), ucoreConfig(), func(string) {})
	if err == nil {
		t.Fatal("expected a compile error")
	}
	if len(spy.Calls) != 0 {
		t.Errorf("coreos-installer must not run when compilation fails, got %v", spy.Calls)
	}
}

func TestUcoreInstall_CoreosInstallerFailure(t *testing.T) {
	spy := runner.NewSpyRunner()
	spy.AllError = errors.New("boom")

	err := NewUcoreInstaller(spy, testLogger()).Install(context.Background(), ucoreConfig(), func(string) {})
	if err == nil {
		t.Fatal("expected an error when coreos-installer fails")
	}
}

// An external Ignition config replaces knuckle's provisioning wholesale, so
// the rebase unit is not applied and the target stays on stock CoreOS. The
// installer must pass the URL through rather than silently ignoring it.
func TestUcoreInstall_ExternalIgnitionURL(t *testing.T) {
	spy := runner.NewSpyRunner()
	cfg := ucoreConfig()
	cfg.IgnitionURL = "https://example.com/custom.ign"

	if err := NewUcoreInstaller(spy, testLogger()).Install(context.Background(), cfg, func(string) {}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call := firstCall(t, spy, "coreos-installer")
	joined := strings.Join(call.Args, " ")
	if !strings.Contains(joined, "--ignition-url https://example.com/custom.ign") {
		t.Errorf("args = %v, want the external ignition URL", call.Args)
	}
	if strings.Contains(joined, "--ignition-file") {
		t.Errorf("args = %v, must not also pass a generated --ignition-file", call.Args)
	}
}

func TestUcoreInstall_IgnitionTempFileIsRemoved(t *testing.T) {
	spy := runner.NewSpyRunner()
	installer := NewUcoreInstaller(spy, testLogger())

	if err := installer.Install(context.Background(), ucoreConfig(), func(string) {}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// A leftover path would be re-used by the next run and could leak the
	// previous run's SSH keys.
	if installer.ignitionPath != "" {
		t.Errorf("ignitionPath = %q, want it cleared after install", installer.ignitionPath)
	}
}

func TestUcoreInstall_WriteIgnitionFileError(t *testing.T) {
	orig := newIgnitionTempFile
	newIgnitionTempFile = func() (ignitionTempFile, error) { return nil, errors.New("no temp") }
	t.Cleanup(func() { newIgnitionTempFile = orig })

	spy := runner.NewSpyRunner()
	err := NewUcoreInstaller(spy, testLogger()).Install(context.Background(), ucoreConfig(), func(string) {})
	if err == nil {
		t.Fatal("expected an error when the ignition temp file cannot be created")
	}
}

// cleanupIgnitionFile is a no-op when no path was recorded, and must tolerate
// a path that has already gone.
// A pinned version is silently unsatisfiable for uCore (stream-tagged images,
// no -V equivalent), so the installer must warn rather than pretend to honour it.
func TestUcoreInstall_VersionPinningWarnsAndProceeds(t *testing.T) {
	spy := runner.NewSpyRunner()
	cfg := ucoreConfig()
	cfg.Version = "44.20260101.0.0"

	if err := NewUcoreInstaller(spy, testLogger()).Install(context.Background(), cfg, func(string) {}); err != nil {
		t.Fatalf("a pinned version must not fail the install, got: %v", err)
	}
	if firstCall(t, spy, "coreos-installer") == nil {
		t.Error("coreos-installer should still run")
	}
}

// The temp Ignition file carries SSH keys, so a failed write must not leave
// it behind.
func TestUcoreWriteIgnitionFile_RemovesTempFileOnWriteError(t *testing.T) {
	backing, err := os.CreateTemp(t.TempDir(), "ucore-ignition-*.json")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	writeErr := errors.New("simulated write failure")
	stub := &stubIgnitionTempFile{file: backing, writeErr: writeErr}
	overrideIgnitionFileOps(t, stub, nil)

	installer := NewUcoreInstaller(runner.NewSpyRunner(), testLogger())
	path, err := installer.writeIgnitionFile(`{"passwd":{"users":[{"sshAuthorizedKeys":["ssh-ed25519 AAAA"]}]}}`)

	if path != "" {
		t.Fatalf("path = %q, want empty", path)
	}
	if !errors.Is(err, writeErr) {
		t.Fatalf("error = %v, want the wrapped write error", err)
	}
	if !stub.closeCalled {
		t.Error("expected the file to be closed before cleanup")
	}
	if _, statErr := os.Stat(backing.Name()); !os.IsNotExist(statErr) {
		t.Errorf("temp file should be removed, stat err = %v", statErr)
	}
}

func TestUcoreWriteIgnitionFile_RemovesTempFileOnCloseError(t *testing.T) {
	backing, err := os.CreateTemp(t.TempDir(), "ucore-ignition-*.json")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	closeErr := errors.New("simulated close failure")
	stub := &stubIgnitionTempFile{file: backing, closeErr: closeErr}
	overrideIgnitionFileOps(t, stub, nil)

	installer := NewUcoreInstaller(runner.NewSpyRunner(), testLogger())
	path, err := installer.writeIgnitionFile(`{"passwd":{"users":[{"sshAuthorizedKeys":["ssh-ed25519 AAAA"]}]}}`)

	if path != "" {
		t.Fatalf("path = %q, want empty", path)
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("error = %v, want the wrapped close error", err)
	}
	if _, statErr := os.Stat(backing.Name()); !os.IsNotExist(statErr) {
		t.Errorf("temp file should be removed, stat err = %v", statErr)
	}
}

func TestUcoreCleanupIgnitionFileNoPath(t *testing.T) {
	installer := NewUcoreInstaller(runner.NewSpyRunner(), testLogger())
	installer.cleanupIgnitionFile() // must not panic
}

func TestUcoreCleanupIgnitionFileMissingFile(t *testing.T) {
	installer := NewUcoreInstaller(runner.NewSpyRunner(), testLogger())
	installer.ignitionPath = "/nonexistent/knuckle-ucore-test.ign"

	installer.cleanupIgnitionFile()

	if installer.ignitionPath != "" {
		t.Errorf("ignitionPath = %q, want it cleared even when removal fails", installer.ignitionPath)
	}
}
