package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/projectbluefin/knuckle/internal/model"
	"github.com/projectbluefin/knuckle/internal/runner"
)

// nmtuiBinary is the interactive tool this step hands the terminal to.
const nmtuiBinary = "nmtui"

// wifiFinishedMsg is returned when nmtui exits, either cleanly or with an
// error. A non-zero exit is not treated as fatal: the user may have quit nmtui
// to back out, in which case there is simply nothing to capture.
type wifiFinishedMsg struct{ err error }

// buildWifiManualForm creates the form used to type a network in by hand.
//
// This is the fallback for live images that cannot scan: minimal CoreOS media
// ships no wireless firmware and often no nmtui, so there may be nothing to
// scan with. The keyfile it produces is still written to the target, and the
// full uCore image ships the firmware needed to use it.
func (m *Model) buildWifiManualForm() *huh.Form {
	return huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Enter WiFi details").
				Description("These are written to the installed system as a NetworkManager profile.\nThe network must be in range and visible from that machine at first boot."),
			huh.NewInput().
				Title("SSID (network name)").
				Placeholder("HomeWifi").
				Value(&m.wifiSSIDIn),
			huh.NewSelect[string]().
				Title("Security").
				Options(
					huh.NewOption("Secured — WPA/WPA2 with a password (PSK)", "psk"),
					huh.NewOption("Open — no password", "open"),
				).
				Value(&m.wifiSecurityIn),
			huh.NewInput().
				Title("Password (PSK)").
				Description("Leave blank for an open network").
				Placeholder("network password").
				EchoMode(huh.EchoModePassword).
				Value(&m.wifiPSKIn),
		),
	).WithTheme(huh.ThemeFunc(huh.ThemeDracula)).WithShowHelp(true).WithWidth(80)
}

// commitWifiManual validates the hand-entered network and records it.
func (m *Model) commitWifiManual() bool {
	secured := m.wifiSecurityIn != "open"
	if err := m.Wizard.AddManualWifi(m.wifiSSIDIn, m.wifiPSKIn, secured); err != nil {
		m.err = err
		m.initForm()
		return false
	}
	return true
}

// viewWifi renders the WiFi step.
//
// The step is always offered, because a machine with no radio is common and the
// cost of a dead step is much lower than hiding the option from the machines
// that need it. Captured networks are listed as soon as they exist, because
// after this step the user cannot see what will be written to the target.
func (m *Model) viewWifi() string {
	var b strings.Builder

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("51"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	warn := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	ok := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))

	b.WriteString("WiFi\n\n")
	b.WriteString("  Join a wireless network for the installed system.\n")
	b.WriteString(dim.Render("  The profile is copied onto the target disk so it comes up connected.") + "\n\n")

	// Say up front when scanning cannot work, rather than letting the user
	// launch a tool that silently finds nothing.
	if msg := m.Wizard.WifiPreflightOrDefault().Blocking(); msg != "" {
		b.WriteString(warn.Render("  ⚠ "+msg) + "\n\n")
	}

	wifi := m.Wizard.State.Config.Wifi
	if m.wifiRunning {
		b.WriteString("  " + warn.Render("nmtui is running — complete it or press q there to return.") + "\n\n")
		return b.String()
	}

	// In manual mode the form owns the screen.
	if m.wifiManual {
		return m.viewWithForm()
	}

	if wifi.Enabled && len(wifi.Profiles) > 0 {
		b.WriteString(ok.Render("  Captured networks:") + "\n")
		for _, p := range wifi.Profiles {
			sec := "open"
			if p.Secured {
				sec = "secured"
			}
			b.WriteString("    • " + p.SSID + " " + dim.Render("("+sec+")") + "\n")
		}
		for _, w := range m.Wizard.WifiWarnings() {
			b.WriteString("    " + warn.Render("! "+w) + "\n")
		}
		b.WriteString("\n")
	} else {
		b.WriteString(dim.Render("  No networks captured yet.") + "\n\n")
	}

	b.WriteString("  " + title.Render("enter") + dim.Render("  launch nmtui to join a network") + "\n")
	b.WriteString("  " + title.Render("m") + dim.Render("      enter the network details by hand") + "\n")
	b.WriteString("  " + title.Render("s") + dim.Render("      skip — the target will be configured over Ethernet") + "\n")
	return b.String()
}

// wifiStepOwnsKey reports whether a key belongs to the WiFi step rather than to
// the global quit/navigation handlers.
//
// 'q' is deliberately excluded so the user can still quit, and 'esc' so they
// can go back. While nmtui is running nothing is owned here, because the TUI is
// suspended and nmtui has the terminal.
func wifiStepOwnsKey(k string) bool {
	switch k {
	case "enter", "m", "M", "s", "S":
		return true
	default:
		return false
	}
}

// handleWifiKey processes keys on the WiFi step.
//
// Enter hands the terminal to nmtui through tea.ExecProcess, which suspends the
// Bubble Tea program and restores it afterwards. The alternative — shelling out
// through the runner — would give nmtui no TTY and it would exit immediately.
func (m *Model) handleWifiKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.wifiRunning {
		// nmtui owns the terminal; the program is suspended, so nothing here
		// should act on input.
		return m, nil
	}
	switch msg.String() {
	case "s", "S":
		m.Wizard.SkipWifi()
		m.err = nil
		return m, m.advanceFromWifi()
	case "m", "M":
		m.wifiManual = true
		m.err = nil
		m.initForm()
		return m, m.activeForm.Init()
	case "enter":
		// Refuse to hand over the terminal when scanning is known not to
		// work, and point at the fallback instead. Without this the user gets
		// a tool that finds nothing, or a bare "command not found".
		if msg := m.Wizard.WifiPreflight().Blocking(); msg != "" {
			m.err = fmt.Errorf("%s — press m to enter the details by hand", msg)
			return m, nil
		}
		return m, m.launchNmtui(context.Background())
	}
	return m, nil
}

// launchNmtui suspends the TUI and runs nmtui against the real terminal.
func (m *Model) launchNmtui(ctx context.Context) tea.Cmd {
	m.wifiRunning = true
	m.err = nil
	return tea.ExecProcess(
		runner.InteractiveCommand(ctx, nmtuiBinary),
		func(err error) tea.Msg { return wifiFinishedMsg{err: err} },
	)
}

// finishWifi captures whatever nmtui left behind and advances.
//
// A failed capture keeps the user on the step rather than advancing: silently
// skipping would install a machine with no WiFi after they had just joined one.
func (m *Model) finishWifi(msg wifiFinishedMsg) (tea.Model, tea.Cmd) {
	m.wifiRunning = false

	// Quitting nmtui is a normal way to back out, so a non-zero exit is
	// reported but does not block the step.
	var nmtuiErr error
	if msg.err != nil {
		nmtuiErr = fmt.Errorf("nmtui exited: %w", msg.err)
	}

	if err := m.Wizard.CaptureWifiProfiles(); err != nil {
		m.err = fmt.Errorf("%w — press enter to run nmtui again, or s to skip WiFi", err)
		return m, nil
	}

	cmd := m.advanceFromWifi()
	// advanceFromWifi clears m.err when it succeeds, so a nmtui exit that was
	// worth reporting has to be restored afterwards.
	if nmtuiErr != nil {
		m.err = nmtuiErr
	}
	return m, cmd
}

// advanceFromWifi leaves the step for the next one, keeping the breadcrumb and
// form state consistent.
func (m *Model) advanceFromWifi() tea.Cmd {
	if err := m.Wizard.Next(); err != nil {
		m.err = err
		return nil
	}
	m.err = nil
	m.cursor = 0
	m.initStepFields()
	m.initForm()
	if m.activeForm != nil {
		return m.activeForm.Init()
	}
	return nil
}

// wifiHasProfiles reports whether anything will be written to the target, used
// by the review summary.
func wifiHasProfiles(cfg *model.InstallConfig) bool {
	return cfg.Wifi.Enabled && len(cfg.Wifi.Profiles) > 0
}
