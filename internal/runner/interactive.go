package runner

import (
	"context"
	"os/exec"
	"syscall"
)

// LookPath reports whether an executable is available on PATH, returning its
// resolved path.
//
// It lives here so that PATH resolution stays inside the runner package along
// with the rest of the exec surface. It runs nothing — it only stats the PATH
// entries — so callers can use it for preflight checks without side effects.
func LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

// InteractiveCommand returns an *exec.Cmd for a program that must own the
// terminal directly — an interactive TUI such as nmtui.
//
// The Runner interface cannot serve this: Run captures output into a Result and
// gives the child no controlling terminal, so a full-screen program would draw
// into a buffer and exit immediately. A caller instead hands this command to
// tea.ExecProcess, which suspends the Bubble Tea program, releases the terminal,
// runs the child against the real TTY, and restores afterwards.
//
// It lives here so that exec.Command stays confined to this package, per the
// "all system commands route through internal/runner" rule. The child is placed
// in its own process group for the same reason RealRunner does it: a
// Ctrl-C-driven exit inside nmtui must not take the installer down with it.
func InteractiveCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}
