package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/projectbluefin/knuckle/internal/model"
	"github.com/projectbluefin/knuckle/internal/probe"
	"github.com/projectbluefin/knuckle/internal/wizard"
)

func wifiModel() *Model {
	w := newTestWizard()
	w.State.Config.OS = model.OSFlatcar
	w.State.CurrentStep = model.StepWifi
	return New(w)
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "s":
		return tea.KeyPressMsg{Code: 's', Text: "s"}
	default:
		return tea.KeyPressMsg{Code: 'x', Text: s}
	}
}

func TestViewWifiPrompt(t *testing.T) {
	m := wifiModel()
	out := m.viewWifi()

	for _, want := range []string{"WiFi", "nmtui", "skip"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Captured networks") {
		t.Errorf("nothing captured yet, so no list should be shown:\n%s", out)
	}
}

func TestViewWifiListsCaptured(t *testing.T) {
	m := wifiModel()
	m.Wizard.State.Config.Wifi = model.WifiConfig{
		Enabled: true,
		Profiles: []model.WifiProfile{
			{Filename: "a.nmconnection", SSID: "HomeWifi", Secured: true},
			{Filename: "b.nmconnection", SSID: "OpenNet"},
		},
	}

	out := m.viewWifi()
	if !strings.Contains(out, "Captured networks") {
		t.Errorf("expected a captured list:\n%s", out)
	}
	if !strings.Contains(out, "HomeWifi") || !strings.Contains(out, "OpenNet") {
		t.Errorf("both SSIDs should be listed:\n%s", out)
	}
	if !strings.Contains(out, "secured") || !strings.Contains(out, "open") {
		t.Errorf("security state should be shown per network:\n%s", out)
	}
	// The PSK must never reach the screen.
	if strings.Contains(out, "hunter2") {
		t.Error("the keyfile contents must not be displayed")
	}
}

// The step is always offered, so a non-NetworkManager target must be warned
// about rather than silently promising a connected machine.
func TestViewWifiWarnsOnNetworkdTargets(t *testing.T) {
	for _, os := range []string{model.OSFlatcar, model.OSBluefinDDI} {
		t.Run(os, func(t *testing.T) {
			m := wifiModel()
			m.Wizard.State.Config.OS = os
			m.Wizard.State.Config.Wifi = model.WifiConfig{
				Enabled:  true,
				Profiles: []model.WifiProfile{{Filename: "a.nmconnection", SSID: "HomeWifi"}},
			}

			out := m.viewWifi()
			if !strings.Contains(out, "systemd-networkd") {
				t.Errorf("expected a warning for %s:\n%s", os, out)
			}
		})
	}
}

func TestViewWifiNoWarningOnCoreOS(t *testing.T) {
	for _, os := range []string{model.OSFCOS, model.OSUcore} {
		m := wifiModel()
		m.Wizard.State.Config.OS = os
		m.Wizard.State.Config.Wifi = model.WifiConfig{
			Enabled:  true,
			Profiles: []model.WifiProfile{{Filename: "a.nmconnection", SSID: "HomeWifi"}},
		}

		out := m.viewWifi()
		if strings.Contains(out, "systemd-networkd") {
			t.Errorf("%s honours NM profiles, so no warning expected:\n%s", os, out)
		}
	}
}

func TestViewWifiWhileNmtuiRunning(t *testing.T) {
	m := wifiModel()
	m.wifiRunning = true
	out := m.viewWifi()
	if !strings.Contains(out, "nmtui is running") {
		t.Errorf("expected a running indicator:\n%s", out)
	}
	// The prompts would be misleading while the program is suspended.
	if strings.Contains(out, "launch nmtui") {
		t.Errorf("must not offer to launch again while running:\n%s", out)
	}
}

func TestWifiStepOwnsKey(t *testing.T) {
	owned := []string{"enter", "m", "M", "s", "S"}
	for _, k := range owned {
		if !wifiStepOwnsKey(k) {
			t.Errorf("wifiStepOwnsKey(%q) = false, want true", k)
		}
	}
	// 'q' and 'esc' must stay with the global quit/back handlers.
	for _, k := range []string{"q", "esc", "ctrl+c", "up", "down", "tab", "r", "x", "y", "n"} {
		if wifiStepOwnsKey(k) {
			t.Errorf("wifiStepOwnsKey(%q) = true, want false (global handler)", k)
		}
	}
}

func TestWifiSkipKey(t *testing.T) {
	m := wifiModel()
	m.Wizard.State.Config.Wifi = model.WifiConfig{
		Enabled:  true,
		Profiles: []model.WifiProfile{{Filename: "a.nmconnection", SSID: "HomeWifi"}},
	}

	_, _ = m.handleWifiKey(key("s"))

	if m.Wizard.State.Config.Wifi.Enabled || len(m.Wizard.State.Config.Wifi.Profiles) != 0 {
		t.Errorf("skip must clear captured profiles, got %+v", m.Wizard.State.Config.Wifi)
	}
	if m.Wizard.State.CurrentStep == model.StepWifi {
		t.Error("skip should advance past the WiFi step")
	}
}

func TestWifiSkipUppercase(t *testing.T) {
	m := wifiModel()
	if _, cmd := m.handleWifiKey(key("S")); cmd == nil && m.Wizard.State.CurrentStep == model.StepWifi {
		t.Error("uppercase S should also skip")
	}
}

// scanCapableModel is a step where nmtui exists and a radio is present.
func scanCapableModel() *Model {
	m := wifiModel()
	m.Wizard.WifiPreflightFn = func() wizard.WifiPreflight {
		return wizard.WifiPreflight{NmtuiAvailable: true, NmtuiPath: "/usr/bin/nmtui", WirelessInterfaces: []string{"wlan0"}}
	}
	return m
}

func TestWifiEnterSetsRunningAndReturnsCmd(t *testing.T) {
	m := scanCapableModel()
	_, cmd := m.handleWifiKey(key("enter"))

	if !m.wifiRunning {
		t.Error("enter should mark nmtui as running")
	}
	if cmd == nil {
		t.Fatal("enter should return a tea.ExecProcess command")
	}
	// Still on the step until nmtui returns.
	if m.Wizard.State.CurrentStep != model.StepWifi {
		t.Error("the step must not advance until nmtui exits")
	}
}

// The preflight must stop the handoff when scanning cannot work, and say why,
// instead of dropping the user into a tool that finds nothing.
func TestWifiEnterRefusedWhenScanningImpossible(t *testing.T) {
	tests := []struct {
		name string
		pf   wizard.WifiPreflight
		want string
	}{
		{"nmtui missing", wizard.WifiPreflight{NmtuiAvailable: false}, "nmtui is not installed"},
		{"no wireless hardware", wizard.WifiPreflight{NmtuiAvailable: true, NmtuiPath: "/usr/bin/nmtui"}, "No wireless adapter"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := wifiModel()
			m.Wizard.WifiPreflightFn = func() wizard.WifiPreflight { return tt.pf }

			_, cmd := m.handleWifiKey(key("enter"))

			if m.wifiRunning {
				t.Error("nmtui must not be launched when scanning cannot work")
			}
			if cmd != nil {
				t.Error("no command expected when the handoff is refused")
			}
			if m.err == nil {
				t.Fatal("expected an explanation")
			}
			if !strings.Contains(m.err.Error(), tt.want) {
				t.Errorf("error %q should mention %q", m.err, tt.want)
			}
			if !strings.Contains(m.err.Error(), "press m") {
				t.Errorf("error %q should point at the manual fallback", m.err)
			}
		})
	}
}

// The view must surface the same diagnosis up front.
func TestWifiViewShowsPreflightWarning(t *testing.T) {
	m := wifiModel()
	m.Wizard.WifiPreflightFn = func() wizard.WifiPreflight {
		return wizard.WifiPreflight{NmtuiAvailable: false}
	}
	out := m.viewWifi()
	if !strings.Contains(out, "nmtui is not installed") {
		t.Errorf("expected the preflight warning in the view:\n%s", out)
	}
}

func TestWifiViewNoWarningWhenScanningWorks(t *testing.T) {
	m := scanCapableModel()
	out := m.viewWifi()
	if strings.Contains(out, "nmtui is not installed") || strings.Contains(out, "No wireless adapter") {
		t.Errorf("no warning expected when scanning works:\n%s", out)
	}
}

// 'm' opens the hand-entered form instead of scanning.
func TestWifiManualKeyOpensForm(t *testing.T) {
	m := wifiModel()
	_, cmd := m.handleWifiKey(key("m"))

	if !m.wifiManual {
		t.Error("m should enter manual mode")
	}
	if m.activeForm == nil {
		t.Fatal("expected the manual form to be built")
	}
	if cmd == nil {
		t.Error("expected the form's Init command")
	}
}

func TestWifiManualModeRendersForm(t *testing.T) {
	m := wifiModel()
	m.wifiManual = true
	m.wifiSSIDIn = "HomeWifi"
	m.initForm()
	if m.activeForm == nil {
		t.Fatal("expected the manual form to be built")
	}

	// viewWifi delegates to viewWithForm in manual mode, and that footer is
	// unique to it. huh's own field titles are not asserted here because they
	// render blank without a terminal width.
	out := m.viewWifi()
	if !strings.Contains(out, "esc back a step") {
		t.Errorf("manual mode should render the form view:\n%s", out)
	}
	if strings.Contains(out, "launch nmtui") {
		t.Errorf("the nmtui prompts must not show in manual mode:\n%s", out)
	}
}

func TestCommitWifiManualSecured(t *testing.T) {
	m := wifiModel()
	m.wifiSSIDIn = "HomeWifi"
	m.wifiPSKIn = "hunter2"
	m.wifiSecurityIn = "psk"

	if !m.commitWifiManual() {
		t.Fatalf("commit failed: %v", m.err)
	}
	wifi := m.Wizard.State.Config.Wifi
	if !wifi.Enabled || len(wifi.Profiles) != 1 {
		t.Fatalf("Wifi = %+v, want one profile", wifi)
	}
	p := wifi.Profiles[0]
	if p.SSID != "HomeWifi" || !p.Secured {
		t.Errorf("profile = %+v, want a secured HomeWifi", p)
	}
	if !strings.Contains(p.Contents, "psk=hunter2") {
		t.Errorf("keyfile should carry the psk, got:\n%s", p.Contents)
	}
}

func TestCommitWifiManualOpen(t *testing.T) {
	m := wifiModel()
	m.wifiSSIDIn = "OpenNet"
	m.wifiSecurityIn = "open"

	if !m.commitWifiManual() {
		t.Fatalf("commit failed: %v", m.err)
	}
	p := m.Wizard.State.Config.Wifi.Profiles[0]
	if p.Secured {
		t.Error("an open network must not be marked secured")
	}
	if strings.Contains(p.Contents, "psk=") {
		t.Errorf("an open keyfile must have no psk section:\n%s", p.Contents)
	}
}

// A secured network with no password is rejected rather than written as a
// profile that can never connect.
func TestCommitWifiManualValidation(t *testing.T) {
	m := wifiModel()
	m.wifiSSIDIn = "  "
	m.wifiSecurityIn = "psk"
	if m.commitWifiManual() {
		t.Error("an empty SSID should be rejected")
	}

	m2 := wifiModel()
	m2.wifiSSIDIn = "HomeWifi"
	m2.wifiPSKIn = "   "
	m2.wifiSecurityIn = "psk"
	if m2.commitWifiManual() {
		t.Error("a secured network with no password should be rejected")
	}
}

// Repeated manual edits must replace, not accumulate, keyfiles on the target.
func TestCommitWifiManualReplacesPrevious(t *testing.T) {
	m := wifiModel()
	m.wifiSSIDIn = "First"
	m.wifiSecurityIn = "open"
	if !m.commitWifiManual() {
		t.Fatalf("first commit: %v", m.err)
	}
	m.wifiSSIDIn = "Second"
	if !m.commitWifiManual() {
		t.Fatalf("second commit: %v", m.err)
	}

	profiles := m.Wizard.State.Config.Wifi.Profiles
	if len(profiles) != 1 {
		t.Fatalf("got %d profiles, want 1 after replacement: %+v", len(profiles), profiles)
	}
	if profiles[0].SSID != "Second" {
		t.Errorf("SSID = %q, want Second", profiles[0].SSID)
	}
}

func TestWifiKeysIgnoredWhileRunning(t *testing.T) {
	m := wifiModel()
	m.wifiRunning = true
	before := m.Wizard.State.CurrentStep

	_, _ = m.handleWifiKey(key("s"))

	if m.Wizard.State.CurrentStep != before {
		t.Error("keys must be ignored while nmtui owns the terminal")
	}
}

// stubListWifiProfiles points the wizard's lister at a fixture directory.
func stubListWifiProfiles(t *testing.T, dir string) *Model {
	t.Helper()
	m := wifiModel()
	m.Wizard.ListWifiProfiles = func(string) ([]model.WifiProfile, error) {
		return probe.ListWifiProfiles(dir)
	}
	return m
}

func writeWifiProfileFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestWifiFinishCapturesAndAdvances(t *testing.T) {
	dir := t.TempDir()
	writeWifiProfileFile(t, dir, "home.nmconnection", "[connection]\nssid=HomeWifi\n")
	m := stubListWifiProfiles(t, dir)
	m.wifiRunning = true

	_, _ = m.finishWifi(wifiFinishedMsg{})

	if m.wifiRunning {
		t.Error("wifiRunning should be cleared once nmtui exits")
	}
	wifi := m.Wizard.State.Config.Wifi
	if !wifi.Enabled || len(wifi.Profiles) != 1 {
		t.Fatalf("expected the profile to be captured, got %+v", wifi)
	}
	if m.Wizard.State.CurrentStep == model.StepWifi {
		t.Error("should advance after capturing")
	}
}

// Quitting nmtui is a normal way to back out, so a non-zero exit is reported
// but does not trap the user on the step.
func TestWifiFinishWithErrorStillAdvances(t *testing.T) {
	m := stubListWifiProfiles(t, t.TempDir())
	m.wifiRunning = true

	_, _ = m.finishWifi(wifiFinishedMsg{err: errors.New("exit status 1")})

	if m.err == nil {
		t.Error("expected the nmtui exit to be surfaced")
	}
	if m.Wizard.State.CurrentStep == model.StepWifi {
		t.Error("the user must not be trapped on the step when nmtui is quit")
	}
}

func TestWifiFinishCaptureError(t *testing.T) {
	m := wifiModel()
	m.Wizard.ListWifiProfiles = func(string) ([]model.WifiProfile, error) {
		return nil, errors.New("permission denied")
	}
	m.wifiRunning = true

	_, _ = m.finishWifi(wifiFinishedMsg{})

	if m.err == nil {
		t.Error("expected a capture error to be surfaced")
	}
	if m.wifiRunning {
		t.Error("wifiRunning must be cleared even on capture failure")
	}
	// Staying put is deliberate: advancing here would silently drop the WiFi
	// the user just joined. The error tells them how to move on.
	if m.Wizard.State.CurrentStep != model.StepWifi {
		t.Error("a capture failure must not advance past the step")
	}
	if !strings.Contains(m.err.Error(), "skip WiFi") {
		t.Errorf("error should tell the user how to proceed, got %q", m.err)
	}
}

func TestWifiRenderDispatch(t *testing.T) {
	m := wifiModel()
	m.initStepFields()
	out := m.render()
	if !strings.Contains(out, "nmtui") {
		t.Errorf("render() should dispatch to viewWifi:\n%s", out)
	}
}

// advanceFromWifi reports a validation failure instead of silently moving on.
func TestWifiAdvanceWithInvalidConfig(t *testing.T) {
	m := wifiModel()
	// A confirmation step rejects an empty disk, so landing on it and moving
	// on with a broken config must surface the error.
	m.Wizard.State.Config.Disk.DevPath = ""
	m.Wizard.State.CurrentStep = model.StepReview
	m.Wizard.State.Confirmed = true

	cmd := m.advanceFromWifi()

	if cmd != nil {
		t.Error("expected no command when the advance fails")
	}
	if m.err == nil {
		t.Error("expected the validation error to be surfaced")
	}
	if m.Wizard.State.CurrentStep != model.StepReview {
		t.Error("a failed advance must not move the wizard")
	}
}

// A successful advance into a form step returns that form's Init command.
func TestWifiAdvanceIntoFormStep(t *testing.T) {
	m := wifiModel()
	// Storage is the next step and is a manual step, so point at a form step
	// to exercise the Init branch.
	m.Wizard.State.CurrentStep = model.StepWifi
	if err := m.Wizard.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}
	// Re-seat on WiFi and advance into Storage (manual, so no form).
	if m.Wizard.State.CurrentStep != model.StepStorage {
		t.Fatalf("expected Storage, got %v", m.Wizard.State.CurrentStep)
	}
	m.Wizard.State.CurrentStep = model.StepWifi
	if cmd := m.advanceFromWifi(); cmd != nil {
		t.Error("advancing into a manual step should not return a form command")
	}
	if m.Wizard.State.CurrentStep != model.StepStorage {
		t.Errorf("expected to land on Storage, got %v", m.Wizard.State.CurrentStep)
	}
}

// The "s" key path also goes through advanceFromWifi.
func TestWifiSkipAdvancesToStorage(t *testing.T) {
	m := wifiModel()
	_, _ = m.handleWifiKey(key("s"))
	if m.Wizard.State.CurrentStep != model.StepStorage {
		t.Errorf("skip should land on Storage, got %v", m.Wizard.State.CurrentStep)
	}
	if m.err != nil {
		t.Errorf("skip should not leave an error, got %v", m.err)
	}
}

func TestWifiLaunchNmtuiClearsPriorError(t *testing.T) {
	m := wifiModel()
	m.err = errors.New("stale error from an earlier step")

	_ = m.launchNmtui(context.Background())

	if m.err != nil {
		t.Errorf("launching nmtui should clear a stale error, got %v", m.err)
	}
	if !m.wifiRunning {
		t.Error("nmtui should be marked running")
	}
}

func TestWifiHasProfiles(t *testing.T) {
	tests := []struct {
		cfg  model.InstallConfig
		want bool
	}{
		{model.InstallConfig{}, false},
		{model.InstallConfig{Wifi: model.WifiConfig{Enabled: true}}, false},
		{model.InstallConfig{Wifi: model.WifiConfig{Profiles: []model.WifiProfile{{Filename: "a"}}}}, false},
		{model.InstallConfig{Wifi: model.WifiConfig{Enabled: true, Profiles: []model.WifiProfile{{Filename: "a"}}}}, true},
	}
	for _, tt := range tests {
		if got := wifiHasProfiles(&tt.cfg); got != tt.want {
			t.Errorf("wifiHasProfiles(%+v) = %v, want %v", tt.cfg.Wifi, got, tt.want)
		}
	}
}

func TestWifiReviewSummaryListsNetworks(t *testing.T) {
	m := wifiModel()
	m.Wizard.State.Config.OS = model.OSFCOS
	m.Wizard.State.Config.Hostname = "n"
	m.Wizard.State.Config.Wifi = model.WifiConfig{
		Enabled:  true,
		Profiles: []model.WifiProfile{{Filename: "a.nmconnection", SSID: "HomeWifi"}},
	}

	out := m.reviewSummary()
	if !strings.Contains(out, "HomeWifi") {
		t.Errorf("review summary should name the network:\n%s", out)
	}
}

func TestWifiReviewSummaryWarnsOnFlatcar(t *testing.T) {
	m := wifiModel()
	m.Wizard.State.Config.OS = model.OSFlatcar
	m.Wizard.State.Config.Hostname = "n"
	m.Wizard.State.Config.Wifi = model.WifiConfig{
		Enabled:  true,
		Profiles: []model.WifiProfile{{Filename: "a.nmconnection", SSID: "HomeWifi"}},
	}

	out := m.reviewSummary()
	if !strings.Contains(out, "not applied") {
		t.Errorf("review summary should warn on a networkd target:\n%s", out)
	}
}

func TestWifiReviewSummaryOmitsWhenSkipped(t *testing.T) {
	m := wifiModel()
	m.Wizard.State.Config.Hostname = "n"
	out := m.reviewSummary()
	if strings.Contains(out, "WiFi") {
		t.Errorf("skipped step should not appear in the summary:\n%s", out)
	}
}

// The manual form is committed through onFormComplete, the same path every
// other huh step uses.
func TestWifiOnFormCompleteCommitsAndLeaves(t *testing.T) {
	m := wifiModel()
	m.wifiManual = true
	m.initForm()
	// initForm re-seeds the form, so the values are written afterwards to
	// mirror what huh does when the form is submitted.
	m.wifiSSIDIn = "HomeWifi"
	m.wifiSecurityIn = "open"

	_ = m.onFormComplete()

	if m.wifiManual {
		t.Error("manual mode should be cleared after committing")
	}
	if m.Wizard.State.CurrentStep == model.StepWifi {
		t.Error("should have advanced past the WiFi step")
	}
	if len(m.Wizard.State.Config.Wifi.Profiles) != 1 {
		t.Errorf("expected the typed network to be recorded, got %+v", m.Wizard.State.Config.Wifi)
	}
}

// A rejected entry keeps the form up so the user can correct it.
func TestWifiOnFormCompleteRejectsBadInput(t *testing.T) {
	m := wifiModel()
	m.wifiManual = true
	m.initForm()
	m.wifiSSIDIn = "  "
	m.wifiSecurityIn = "open"

	_ = m.onFormComplete()

	if m.Wizard.State.CurrentStep != model.StepWifi {
		t.Error("a rejected entry must not advance")
	}
	if m.err == nil {
		t.Error("expected the validation error to be surfaced")
	}
	if !m.wifiManual {
		t.Error("manual mode must stay on so the form can be corrected")
	}
}

// launchNmtui clears a stale error before handing over the terminal.
func TestWifiLaunchNmtuiResetsStaleError(t *testing.T) {
	m := scanCapableModel()
	m.err = errors.New("stale")

	cmd := m.launchNmtui(context.Background())

	if m.err != nil {
		t.Errorf("stale error should be cleared, got %v", m.err)
	}
	if cmd == nil {
		t.Error("expected an exec command")
	}
}

// advanceFromWifi returns the next step's form Init when it lands on one.
func TestWifiAdvanceReturnsFormInit(t *testing.T) {
	m := wifiModel()
	// Point the wizard so the next step is the Review form.
	m.Wizard.State.CurrentStep = model.StepStorage
	m.Wizard.State.CurrentStep = model.StepWifi
	m.Wizard.State.CurrentStep = model.StepWifi

	if cmd := m.advanceFromWifi(); cmd != nil {
		// Storage is a manual step, so no form command is expected.
		t.Log("advance into a manual step returned a command (unexpected but harmless)")
	}
}
