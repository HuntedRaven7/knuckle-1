package wizard

import (
	"context"
	"testing"

	"github.com/projectbluefin/knuckle/internal/model"
)

func ucoreWizard() *Wizard {
	w := New(nil, nil, nil)
	w.State.Config.OS = model.OSUcore
	return w
}

// uCore runs a Fedora kernel and updates via rpm-ostreed, so the Flatcar-only
// steps must be bypassed exactly as they are for Bluefin DDI.
func TestUcoreSkipsFlatcarOnlySteps(t *testing.T) {
	skipped := []model.WizardStep{
		model.StepSysext,
		model.StepNvidia,
		model.StepTailscale,
		model.StepUpdate,
	}

	for _, start := range skipped {
		t.Run(start.String(), func(t *testing.T) {
			w := ucoreWizard()
			w.State.CurrentStep = start
			if err := w.Next(); err != nil {
				t.Fatalf("Next() from %s error = %v", start, err)
			}
			if w.State.CurrentStep == start {
				t.Errorf("Next() from %s left the wizard on the skipped step", start)
			}
		})
	}
}

// The uCore image step is conditional: only a uCore target sees it.
func TestUcoreStepIsVisitedOnlyForUcore(t *testing.T) {
	t.Run("ucore visits it", func(t *testing.T) {
		w := ucoreWizard()
		w.State.CurrentStep = model.StepWelcome
		if err := w.Next(); err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		if w.State.CurrentStep != model.StepUcore {
			t.Errorf("after Welcome the step is %s, want uCore Image", w.State.CurrentStep)
		}
	})

	for _, os := range []string{model.OSFlatcar, model.OSFCOS, model.OSBluefinDDI} {
		t.Run(os+" skips it", func(t *testing.T) {
			w := New(nil, nil, nil)
			w.State.Config.OS = os
			w.State.CurrentStep = model.StepWelcome
			if err := w.Next(); err != nil {
				t.Fatalf("Next() error = %v", err)
			}
			if w.State.CurrentStep == model.StepUcore {
				t.Errorf("%s should not visit the uCore image step", os)
			}
			if w.State.CurrentStep != model.StepNetwork {
				t.Errorf("after Welcome the step is %s, want Network", w.State.CurrentStep)
			}
		})
	}

	t.Run("previous skips back over it", func(t *testing.T) {
		w := New(nil, nil, nil)
		w.State.Config.OS = model.OSFlatcar
		w.State.CurrentStep = model.StepNetwork
		w.Previous()
		if w.State.CurrentStep == model.StepUcore {
			t.Error("Previous() from Network landed on the uCore step")
		}
		if w.State.CurrentStep != model.StepWelcome {
			t.Errorf("Previous() from Network = %s, want Welcome", w.State.CurrentStep)
		}
	})

	t.Run("goto refuses it", func(t *testing.T) {
		w := New(nil, nil, nil)
		w.State.Config.OS = model.OSFlatcar
		w.State.CurrentStep = model.StepWelcome
		w.GoToStep(model.StepUcore)
		if w.State.CurrentStep == model.StepUcore {
			t.Error("GoToStep(uCore) should be refused for a Flatcar target")
		}
	})
}

func TestUcoreFullStepTraversal(t *testing.T) {
	w := ucoreWizard()
	// Enough state for the later steps' validators to pass, so the traversal
	// is gated on step skipping rather than on unrelated validation.
	w.State.Disks = []model.DiskInfo{{DevPath: "/dev/sda", Path: "/dev/disk/by-id/test"}}
	w.State.Config.Disk = w.State.Disks[0]
	w.State.Config.Hostname = "ucore-node"
	w.State.Interfaces = []model.NetworkInterface{{Name: "eth0"}}
	w.State.Config.Network.Interface = "eth0"
	w.State.Config.Users = []model.UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAA test"}}}
	w.State.Config.SSHKeys = []string{"ssh-ed25519 AAAA test"}
	w.State.CurrentStep = model.StepWelcome

	var visited []model.WizardStep
	// Step up to Done; validateWelcome needs a valid stream, which the
	// default provides.
	for i := 0; i < 20 && w.State.CurrentStep < model.StepDone; i++ {
		if err := w.Next(); err != nil {
			t.Fatalf("Next() from %s error = %v", w.State.CurrentStep, err)
		}
		visited = append(visited, w.State.CurrentStep)
	}

	for _, s := range visited {
		switch s {
		case model.StepSysext, model.StepNvidia, model.StepTailscale, model.StepUpdate:
			t.Errorf("uCore visited the Flatcar-only step %s", s)
		}
	}
	// The expected uCore path.
	want := []model.WizardStep{
		model.StepUcore, model.StepNetwork, model.StepWifi, model.StepStorage,
		model.StepUser, model.StepReview, model.StepInstall, model.StepDone,
	}
	if len(visited) != len(want) {
		t.Fatalf("visited %v, want %v", visited, want)
	}
	for i := range want {
		if visited[i] != want[i] {
			t.Errorf("step %d = %s, want %s", i, visited[i], want[i])
		}
	}

	// And walking back must retrace the same path.
	for i := 0; i < 20 && w.State.CurrentStep > model.StepWelcome; i++ {
		w.Previous()
		if w.State.CurrentStep == model.StepDone {
			continue
		}
		if w.State.CurrentStep == model.StepUcore && w.State.Config.OS != model.OSUcore {
			t.Error("Previous() entered the uCore step on the way back")
		}
	}
	if w.State.CurrentStep != model.StepWelcome {
		t.Errorf("walking back ended at %s, want Welcome", w.State.CurrentStep)
	}
}

// uCore must not be offered Flatcar bakery sysexts: they are built against
// Flatcar kernels and will not load on a Fedora one.
func TestUcoreFetchesNoSysexts(t *testing.T) {
	w := ucoreWizard()
	w.State.Sysexts = []model.SysextEntry{{Name: "docker", Selected: true}}

	if err := w.FetchSysexts(context.Background()); err != nil {
		t.Fatalf("FetchSysexts() error = %v", err)
	}
	if w.State.Sysexts != nil {
		t.Errorf("Sysexts = %v, want nil for uCore", w.State.Sysexts)
	}
}

func TestUcoreIsNvidiaSelectedIsFalse(t *testing.T) {
	w := ucoreWizard()
	w.State.Sysexts = []model.SysextEntry{{Name: "nvidia-runtime", Selected: true}}

	if w.isNvidiaSelected() {
		t.Error("isNvidiaSelected() = true for uCore; the driver comes from the image tag")
	}
}

func TestUcoreValidateWelcomeUsesUcoreStream(t *testing.T) {
	t.Run("valid stream passes", func(t *testing.T) {
		w := ucoreWizard()
		if err := w.validateWelcome(); err != nil {
			t.Errorf("validateWelcome() = %v, want nil", err)
		}
	})

	t.Run("invalid stream is rejected", func(t *testing.T) {
		w := ucoreWizard()
		// "next" is an FCOS stream, not a uCore one.
		w.State.Config.Ucore.Stream = "next"
		if err := w.validateWelcome(); err == nil {
			t.Error("validateWelcome() = nil, want an error for an FCOS stream on uCore")
		}
	})

	t.Run("channel is not consulted", func(t *testing.T) {
		w := ucoreWizard()
		// "beta" is a valid Flatcar channel but an invalid uCore stream; the
		// check must be driven by Ucore.Stream, not Channel.
		w.State.Config.Channel = "beta"
		if err := w.validateWelcome(); err != nil {
			t.Errorf("validateWelcome() = %v, want nil — Channel must be ignored for uCore", err)
		}
	})
}

func TestValidateUcore(t *testing.T) {
	t.Run("empty config takes the defaults and passes", func(t *testing.T) {
		w := ucoreWizard()
		w.State.Config.Ucore = model.UcoreConfig{}
		if err := w.validateUcore(); err != nil {
			t.Errorf("validateUcore() = %v, want nil", err)
		}
	})

	t.Run("full valid selection passes", func(t *testing.T) {
		w := ucoreWizard()
		w.State.Config.Ucore = model.UcoreConfig{
			Image:  model.UcoreImageHCI,
			Stream: model.UcoreStreamLTS,
			Nvidia: model.UcoreNvidiaLTS,
			Verify: model.UcoreVerifyUnverified,
		}
		if err := w.validateUcore(); err != nil {
			t.Errorf("validateUcore() = %v, want nil", err)
		}
	})

	for _, tt := range []struct {
		name string
		cfg  model.UcoreConfig
	}{
		{"bad image", model.UcoreConfig{Image: "ucore-tiny"}},
		{"bad stream", model.UcoreConfig{Stream: "next"}},
		{"bad nvidia", model.UcoreConfig{Nvidia: "570-open"}},
		{"bad verify", model.UcoreConfig{Verify: "yes"}},
	} {
		t.Run(tt.name+" is rejected", func(t *testing.T) {
			w := ucoreWizard()
			w.State.Config.Ucore = tt.cfg
			if err := w.validateUcore(); err == nil {
				t.Errorf("validateUcore(%+v) = nil, want an error", tt.cfg)
			}
		})
	}
}

// The conditional step must be reachable through the standard validation entry
// point, not only the private helper.
func TestUcoreValidateCurrentStep(t *testing.T) {
	w := ucoreWizard()
	w.State.CurrentStep = model.StepUcore
	w.State.Config.Ucore = model.UcoreConfig{Image: "ucore-tiny"}
	if err := w.ValidateCurrentStep(); err == nil {
		t.Error("ValidateCurrentStep() on the uCore step = nil, want an error")
	}
}

func TestUcoreSkipsFlatcarExtras(t *testing.T) {
	for _, tt := range []struct {
		os   string
		want bool
	}{
		{model.OSUcore, true},
		{model.OSBluefinDDI, true},
		{model.OSFlatcar, false},
		{model.OSFCOS, false},
	} {
		w := New(nil, nil, nil)
		w.State.Config.OS = tt.os
		if got := w.skipsFlatcarExtras(); got != tt.want {
			t.Errorf("skipsFlatcarExtras() for %s = %v, want %v", tt.os, got, tt.want)
		}
	}
}
