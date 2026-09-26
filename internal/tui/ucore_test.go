package tui

import (
	"strings"
	"testing"

	"github.com/projectbluefin/knuckle/internal/model"
)

func ucoreModel() *Model {
	w := newTestWizard()
	w.State.Config.OS = model.OSUcore
	return New(w)
}

// The uCore image step is a four-select huh form. Building it must not panic and
// must be reachable for a uCore target.
func TestUcoreFormBuilds(t *testing.T) {
	m := ucoreModel()
	m.Wizard.State.CurrentStep = model.StepUcore
	m.initForm()

	if m.activeForm == nil {
		t.Fatal("expected a huh form on the uCore step")
	}
	if m.activeForm.View() == "" {
		t.Error("expected the uCore form to render")
	}
}

func TestUcoreFormSeedsFromConfig(t *testing.T) {
	m := ucoreModel()
	m.Wizard.State.Config.Ucore = model.UcoreConfig{
		Image:  model.UcoreImageHCI,
		Stream: model.UcoreStreamLTS,
		Nvidia: model.UcoreNvidiaLTS,
		Verify: model.UcoreVerifyUnverified,
	}
	m.Wizard.State.CurrentStep = model.StepUcore
	m.initForm()

	if m.ucoreImageIn != model.UcoreImageHCI {
		t.Errorf("ucoreImageIn = %q, want %q", m.ucoreImageIn, model.UcoreImageHCI)
	}
	if m.ucoreStreamIn != model.UcoreStreamLTS {
		t.Errorf("ucoreStreamIn = %q, want %q", m.ucoreStreamIn, model.UcoreStreamLTS)
	}
	if m.ucoreNvidiaIn != model.UcoreNvidiaLTS {
		t.Errorf("ucoreNvidiaIn = %q, want %q", m.ucoreNvidiaIn, model.UcoreNvidiaLTS)
	}
	if m.ucoreVerifyIn != model.UcoreVerifyUnverified {
		t.Errorf("ucoreVerifyIn = %q, want %q", m.ucoreVerifyIn, model.UcoreVerifyUnverified)
	}
}

// An unset config must seed the selects with the defaults, not with empty
// strings — an empty huh.Select value would be rejected by validation.
func TestUcoreFormSeedsDefaultsWhenUnset(t *testing.T) {
	m := ucoreModel()
	m.Wizard.State.CurrentStep = model.StepUcore
	m.initForm()

	if m.ucoreImageIn != model.UcoreImageFull {
		t.Errorf("ucoreImageIn = %q, want the default %q", m.ucoreImageIn, model.UcoreImageFull)
	}
	if m.ucoreStreamIn != model.UcoreStreamStable {
		t.Errorf("ucoreStreamIn = %q, want the default %q", m.ucoreStreamIn, model.UcoreStreamStable)
	}
	if m.ucoreNvidiaIn != "" {
		t.Errorf("ucoreNvidiaIn = %q, want empty (no NVIDIA driver)", m.ucoreNvidiaIn)
	}
	if m.ucoreVerifyIn != model.UcoreVerifySigned {
		t.Errorf("ucoreVerifyIn = %q, want the default %q", m.ucoreVerifyIn, model.UcoreVerifySigned)
	}
}

// Completing the form commits the selection to the wizard config.
func TestUcoreFormCompleteCommitsSelection(t *testing.T) {
	m := ucoreModel()
	m.Wizard.State.CurrentStep = model.StepUcore
	m.initForm()

	m.ucoreImageIn = model.UcoreImageMinimal
	m.ucoreStreamIn = model.UcoreStreamTesting
	m.ucoreNvidiaIn = model.UcoreNvidiaOpen
	m.ucoreVerifyIn = model.UcoreVerifySigned

	_ = m.onFormComplete()

	uc := m.Wizard.State.Config.Ucore
	if uc.Image != model.UcoreImageMinimal {
		t.Errorf("committed Image = %q, want %q", uc.Image, model.UcoreImageMinimal)
	}
	if uc.Stream != model.UcoreStreamTesting {
		t.Errorf("committed Stream = %q, want %q", uc.Stream, model.UcoreStreamTesting)
	}
	if uc.Nvidia != model.UcoreNvidiaOpen {
		t.Errorf("committed Nvidia = %q, want %q", uc.Nvidia, model.UcoreNvidiaOpen)
	}
	if uc.Verify != model.UcoreVerifySigned {
		t.Errorf("committed Verify = %q, want %q", uc.Verify, model.UcoreVerifySigned)
	}
	// A valid selection advances the wizard past the uCore step.
	if m.Wizard.State.CurrentStep == model.StepUcore {
		t.Error("expected the wizard to advance past the uCore step")
	}
}

// An invalid selection must block the advance and surface the error rather
// than committing a bad image reference.
func TestUcoreFormCompleteRejectsInvalidSelection(t *testing.T) {
	m := ucoreModel()
	m.Wizard.State.CurrentStep = model.StepUcore
	m.initForm()
	m.ucoreImageIn = "ucore-tiny"

	_ = m.onFormComplete()

	if m.Wizard.State.CurrentStep != model.StepUcore {
		t.Errorf("wizard advanced to %s despite an invalid image", m.Wizard.State.CurrentStep)
	}
	if m.err == nil {
		t.Error("expected an error to be surfaced")
	}
	if m.Wizard.State.Config.Ucore.Image == "ucore-tiny" {
		t.Error("an invalid image must not be committed to the config")
	}
}

// uCore publishes its own stream vocabulary, so the channel cards must offer
// uCore's streams and not Flatcar's or FCOS's.
func TestUcoreChannelList(t *testing.T) {
	m := ucoreModel()
	got := m.channelList()

	want := model.UcoreStreams
	if len(got) != len(want) {
		t.Fatalf("channelList() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("channelList()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// "next" is FCOS-only and must not leak into the uCore cards.
	for _, s := range got {
		if s == "next" || s == "beta" || s == "alpha" {
			t.Errorf("channelList() contains %q, which is not a uCore stream", s)
		}
	}
	if m.channelCardCount() != len(model.UcoreStreams) {
		t.Errorf("channelCardCount() = %d, want %d", m.channelCardCount(), len(model.UcoreStreams))
	}
}

// The card cursor writes to Ucore.Stream, not Channel: leaving it in Channel
// would leave the uCore stream at its default no matter what the user picked.
func TestUcoreWelcomeCommitWritesUcoreStream(t *testing.T) {
	m := ucoreModel()
	m.Wizard.State.CurrentStep = model.StepWelcome
	m.osSubView = false
	m.Wizard.State.Config.Channel = "stable"

	// Cursor 2 = the third uCore stream card.
	m.cursor = 2
	_, _ = m.handleEnter()

	if m.Wizard.State.Config.Ucore.Stream != "lts" {
		t.Errorf("Ucore.Stream = %q, want %q", m.Wizard.State.Config.Ucore.Stream, "lts")
	}
	if m.Wizard.State.Config.Channel != "stable" {
		t.Errorf("Channel = %q, want it untouched for a uCore target", m.Wizard.State.Config.Channel)
	}
}

func TestUcoreChannelCardMeta(t *testing.T) {
	m := ucoreModel()
	metas := m.getChannelMeta()

	if len(metas) != len(model.UcoreStreams) {
		t.Fatalf("getChannelMeta() returned %d cards, want %d", len(metas), len(model.UcoreStreams))
	}
	for i, meta := range metas {
		if meta.name != model.UcoreStreams[i] {
			t.Errorf("card %d name = %q, want %q", i, meta.name, model.UcoreStreams[i])
		}
		if meta.desc == "" {
			t.Errorf("card %d (%s) has no description", i, meta.name)
		}
	}
}

func TestUcoreChannelCardsRender(t *testing.T) {
	m := ucoreModel()
	m.Wizard.State.CurrentStep = model.StepWelcome
	m.initStepFields()
	// initStepFields opens the OS sub-view; this test is about the stream
	// cards shown after an OS is chosen.
	m.osSubView = false

	out := m.viewChannelCards()
	if !strings.Contains(out, "uCore release stream") {
		t.Errorf("expected a uCore-specific heading; got:\n%s", out)
	}
	if !strings.Contains(out, "ublue-os/ucore") {
		t.Errorf("expected the uCore project link; got:\n%s", out)
	}
}

func TestUcoreReviewFormTitle(t *testing.T) {
	m := ucoreModel()
	m.Wizard.State.CurrentStep = model.StepReview
	m.initForm()

	if m.activeForm == nil {
		t.Fatal("expected a review form")
	}
	// huh only renders field titles after Init.
	_ = m.activeForm.Init()
	if !strings.Contains(m.activeForm.View(), "uCore") {
		t.Errorf("expected the review confirm to name uCore; got:\n%s", m.activeForm.View())
	}
}

// The review summary is the last thing the user sees before the wipe, so the
// rebase target must be spelled out there.
func TestUcoreReviewSummaryShowsImage(t *testing.T) {
	m := ucoreModel()
	m.Wizard.State.Config.Ucore = model.UcoreConfig{
		Image:  model.UcoreImageMinimal,
		Stream: model.UcoreStreamLTS,
		Nvidia: model.UcoreNvidiaOpen,
		Verify: model.UcoreVerifySigned,
	}
	m.Wizard.State.Config.Hostname = "ucore-node"

	out := m.reviewSummary()
	if !strings.Contains(out, "uCore") {
		t.Errorf("expected the product name in the summary; got:\n%s", out)
	}
	if !strings.Contains(out, "ghcr.io/ublue-os/ucore-minimal:lts-nvidia") {
		t.Errorf("expected the rebase reference in the summary; got:\n%s", out)
	}
	if !strings.Contains(out, "lts") {
		t.Errorf("expected the uCore stream rather than Channel; got:\n%s", out)
	}
}

func TestUcoreViewInstallNamesUcore(t *testing.T) {
	m := ucoreModel()
	m.Wizard.State.CurrentStep = model.StepInstall

	if out := m.viewInstall(); !strings.Contains(out, "Installing uCore") {
		t.Errorf("expected the install screen to name uCore; got:\n%s", out)
	}
}

// The Done screen must warn that one more reboot performs the rebase — without
// it the machine looks finished while still running stock CoreOS.
func TestUcoreViewDoneExplainsRebase(t *testing.T) {
	m := ucoreModel()
	m.Wizard.State.CurrentStep = model.StepDone
	m.Wizard.State.Config.Ucore = model.UcoreConfig{
		Image:  model.UcoreImageHCI,
		Stream: model.UcoreStreamStable,
		Verify: model.UcoreVerifySigned,
	}
	m.Wizard.State.Config.Hostname = "ucore-node"
	m.Wizard.State.Config.DryRun = true

	out := m.viewDone()
	if !strings.Contains(out, "uCore has been installed") {
		t.Errorf("expected the done screen to name uCore; got:\n%s", out)
	}
	if !strings.Contains(out, "reboot") {
		t.Errorf("expected the done screen to mention the extra reboot; got:\n%s", out)
	}
	if !strings.Contains(out, "ghcr.io/ublue-os/ucore-hci:stable") {
		t.Errorf("expected the done screen to name the rebase target; got:\n%s", out)
	}
	if !strings.Contains(out, "ublue-os/ucore") {
		t.Errorf("expected uCore community links; got:\n%s", out)
	}
}

// The advanced-options channel field must validate against uCore's vocabulary
// and land in Ucore.Stream.
func TestUcoreApplyFieldsChannel(t *testing.T) {
	m := ucoreModel()
	m.Wizard.State.CurrentStep = model.StepWelcome
	m.fields = []field{{key: "channel", value: "lts"}}
	m.err = nil

	m.applyFields()

	if m.err != nil {
		t.Fatalf("applyFields() set err = %v for a valid uCore stream", m.err)
	}
	if m.Wizard.State.Config.Ucore.Stream != "lts" {
		t.Errorf("Ucore.Stream = %q, want %q", m.Wizard.State.Config.Ucore.Stream, "lts")
	}
}

func TestUcoreApplyFieldsRejectsNonUcoreChannel(t *testing.T) {
	m := ucoreModel()
	m.Wizard.State.CurrentStep = model.StepWelcome
	// "next" is an FCOS stream, not a uCore one.
	m.fields = []field{{key: "channel", value: "next"}}
	m.err = nil

	m.applyFields()

	if m.err == nil {
		t.Error("expected an FCOS stream to be rejected on the uCore path")
	}
}

func TestUcoreMaxCursorWelcome(t *testing.T) {
	m := ucoreModel()
	m.Wizard.State.CurrentStep = model.StepWelcome
	m.osSubView = false

	if got, want := m.maxCursor(), len(model.UcoreStreams); got != want {
		t.Errorf("maxCursor() = %d, want %d", got, want)
	}
}

func TestUcoreChromeShowsUcoreStream(t *testing.T) {
	m := ucoreModel()
	m.Wizard.State.CurrentStep = model.StepNetwork
	m.Wizard.State.Config.Channel = "beta" // stale Flatcar value
	m.Wizard.State.Config.Ucore.Stream = model.UcoreStreamLTS

	out := m.buildBreadcrumb()
	if !strings.Contains(out, "lts") {
		t.Errorf("expected the chrome to show the uCore stream; got:\n%s", out)
	}
	if strings.Contains(out, "beta") {
		t.Errorf("the chrome must not show the unused Channel for uCore; got:\n%s", out)
	}
}
