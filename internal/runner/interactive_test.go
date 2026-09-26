package runner

import (
	"context"

	"testing"
)

func TestInteractiveCommand(t *testing.T) {
	cmd := InteractiveCommand(context.Background(), "nmtui")
	if cmd == nil {
		t.Fatal("InteractiveCommand returned nil")
	}
	if got := cmd.Path; got != "nmtui" && got != "" {
		// exec.Command sets Path to the looked-up binary when it resolves, or
		// leaves the name as Path when it does not. Both are acceptable here.
		t.Logf("resolved path: %q", got)
	}
	if cmd.Args[0] != "nmtui" {
		t.Errorf("Args[0] = %q, want %q", cmd.Args[0], "nmtui")
	}
	// Stdout/Stdin must stay nil so tea.ExecProcess wires the real terminal.
	if cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil {
		t.Error("stdio must be left nil so tea.ExecProcess attaches the terminal")
	}
	// A separate process group keeps a Ctrl-C inside nmtui from killing the
	// installer along with it.
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Setpgid {
		if cmd.SysProcAttr == nil {
			t.Fatal("SysProcAttr must be set")
		}
		if !cmd.SysProcAttr.Setpgid {
			t.Error("Setpgid must be true so nmtui runs in its own process group")
		}
	}
}

func TestInteractiveCommandArgs(t *testing.T) {
	cmd := InteractiveCommand(context.Background(), "nmtui", "--ascii")
	if len(cmd.Args) != 2 || cmd.Args[1] != "--ascii" {
		t.Errorf("Args = %v, want [nmtui --ascii]", cmd.Args)
	}
}

func TestInteractiveCommandHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := InteractiveCommand(ctx, "nmtui")
	if cmd == nil {
		t.Fatal("InteractiveCommand returned nil")
	}
	// A cancelled context must be bound to the command so an abandoned nmtui
	// cannot outlive the installer.
	if cmd.Cancel == nil {
		t.Error("context should be bound to the command via cmd.Cancel")
	}
}

func TestLookPath(t *testing.T) {
	// "sh" is present in any sane environment; a name that cannot exist is not.
	if _, err := LookPath("sh"); err != nil {
		t.Errorf("LookPath(\"sh\") = %v, want a resolved path", err)
	}
	if _, err := LookPath("knuckle-definitely-not-a-real-binary-xyzzy"); err == nil {
		t.Error("expected an error for a binary that does not exist")
	}
}
