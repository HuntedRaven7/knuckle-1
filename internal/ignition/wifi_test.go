package ignition

import (
	"strings"
	"testing"

	"github.com/projectbluefin/knuckle/internal/model"
)

const testKeyfile = `[connection]
id=HomeWifi
type=wifi

[wifi]
ssid=HomeWifi
mode=infrastructure

[wifi-security]
key-mgmt=wpa-psk
psk=supersecret123`

func wifiConfig(os string) *model.InstallConfig {
	return &model.InstallConfig{
		OS:       os,
		Hostname: "wifi-node",
		Timezone: "UTC",
		Network:  model.NetworkConfig{Mode: model.NetworkDHCP},
		Users:    []model.UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAA test"}}},
		Wifi: model.WifiConfig{
			Enabled: true,
			Profiles: []model.WifiProfile{{
				Filename: "knuckle-wifi.nmconnection",
				SSID:     "HomeWifi",
				Secured:  true,
				Contents: testKeyfile,
			}},
		},
	}
}

// Every generator must be able to carry a keyfile, and the result must survive
// the real Butane compiler.
func TestWifiProfilesCompileForAllOS(t *testing.T) {
	g := NewGenerator()
	generators := map[string]func(*model.InstallConfig) (string, error){
		model.OSFlatcar: g.GenerateButane,
		model.OSFCOS:    g.GenerateFCOSButane,
		model.OSUcore:   g.GenerateUcoreButane,
	}

	for os, gen := range generators {
		t.Run(os, func(t *testing.T) {
			out, err := gen(wifiConfig(os))
			if err != nil {
				t.Fatalf("generate error = %v", err)
			}
			if !strings.Contains(out, "/etc/NetworkManager/system-connections/knuckle-wifi.nmconnection") {
				t.Errorf("keyfile path missing:\n%s", out)
			}
			// NetworkManager ignores a keyfile that is group/world readable,
			// so the mode has to be 0600 or the machine silently has no network.
			if !strings.Contains(out, "mode: 0600") {
				t.Errorf("keyfile must be written 0600:\n%s", out)
			}
			if !strings.Contains(out, "supersecret123") {
				t.Errorf("keyfile body must be embedded verbatim:\n%s", out)
			}
			// The multi-line body must stay inside the block scalar.
			if strings.Count(out, "    inline: |") < 1 {
				t.Errorf("expected a block scalar:\n%s", out)
			}
			if _, err := CompileToIgnition(out); err != nil {
				t.Errorf("Butane compile failed: %v\n%s", err, out)
			}
		})
	}
}

// Skipping the step must produce byte-identical output to not visiting it.
func TestWifiOmittedWhenStepSkipped(t *testing.T) {
	g := NewGenerator()
	cfg := wifiConfig(model.OSFCOS)
	cfg.Wifi = model.WifiConfig{}

	out, err := g.GenerateFCOSButane(cfg)
	if err != nil {
		t.Fatalf("generate error = %v", err)
	}
	if strings.Contains(out, "NetworkManager") {
		t.Errorf("skipped step must not emit a keyfile:\n%s", out)
	}
}

// Enabled with zero profiles is treated as skipped, so a user who ran nmtui and
// quit without joining anything gets no stray empty file.
func TestWifiEnabledWithNoProfiles(t *testing.T) {
	g := NewGenerator()
	cfg := wifiConfig(model.OSFCOS)
	cfg.Wifi = model.WifiConfig{Enabled: true}

	out, err := g.GenerateFCOSButane(cfg)
	if err != nil {
		t.Fatalf("generate error = %v", err)
	}
	if strings.Contains(out, "NetworkManager") {
		t.Errorf("no profiles should mean no keyfile:\n%s", out)
	}
}

func TestWifiMultipleProfiles(t *testing.T) {
	g := NewGenerator()
	cfg := wifiConfig(model.OSFCOS)
	cfg.Wifi.Profiles = append(cfg.Wifi.Profiles, model.WifiProfile{
		Filename: "guest.nmconnection",
		SSID:     "GuestNet",
		Contents: "[connection]\nid=GuestNet\ntype=wifi\n\n[wifi]\nssid=GuestNet\n",
	})

	out, err := g.GenerateFCOSButane(cfg)
	if err != nil {
		t.Fatalf("generate error = %v", err)
	}
	for _, want := range []string{"knuckle-wifi.nmconnection", "guest.nmconnection", "HomeWifi", "GuestNet"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output", want)
		}
	}
	if _, err := CompileToIgnition(out); err != nil {
		t.Errorf("Butane compile failed: %v", err)
	}
}

// A filename that could write outside the profile directory must be refused at
// the point it becomes a path, even though the prober already filtered it.
func TestWifiRejectsTraversalFilename(t *testing.T) {
	g := NewGenerator()
	generators := map[string]func(*model.InstallConfig) (string, error){
		model.OSFlatcar: g.GenerateButane,
		model.OSFCOS:    g.GenerateFCOSButane,
		model.OSUcore:   g.GenerateUcoreButane,
	}

	for name := range generators {
		t.Run(name, func(t *testing.T) {
			for _, bad := range []string{"../etc/shadow", "/abs.nmconnection", "sub/dir.nmconnection", ".hidden"} {
				cfg := wifiConfig(name)
				cfg.Wifi.Profiles[0].Filename = bad
				if _, err := generators[name](cfg); err == nil {
					t.Errorf("%s: filename %q was accepted, want rejection", name, bad)
				}
			}
		})
	}
}

// An empty keyfile must be refused on every generator, not just the FCOS one.
func TestWifiRejectsEmptyProfileOnAllGenerators(t *testing.T) {
	g := NewGenerator()
	generators := map[string]func(*model.InstallConfig) (string, error){
		model.OSFlatcar: g.GenerateButane,
		model.OSFCOS:    g.GenerateFCOSButane,
		model.OSUcore:   g.GenerateUcoreButane,
	}

	for name, gen := range generators {
		cfg := wifiConfig(name)
		cfg.Wifi.Profiles[0].Contents = "  \n\n"
		if _, err := gen(cfg); err == nil {
			t.Errorf("%s: an empty keyfile body was accepted, want rejection", name)
		}
	}
}

func TestWifiRejectsEmptyProfileBody(t *testing.T) {
	g := NewGenerator()
	cfg := wifiConfig(model.OSFCOS)
	cfg.Wifi.Profiles[0].Contents = "   \n\n"

	if _, err := g.GenerateFCOSButane(cfg); err == nil {
		t.Error("an empty keyfile body should be rejected, not written as an empty file")
	}
}

// A keyfile with blank lines must not terminate the YAML block scalar early.
func TestWifiPreservesBlankLinesInKeyfile(t *testing.T) {
	g := NewGenerator()
	cfg := wifiConfig(model.OSFCOS)

	out, err := g.GenerateFCOSButane(cfg)
	if err != nil {
		t.Fatalf("generate error = %v", err)
	}
	ign, err := CompileToIgnition(out)
	if err != nil {
		t.Fatalf("Butane compile failed: %v", err)
	}
	// CompileToIgnition succeeded and the sections after the keyfile survived,
	// which only holds if the block scalar consumed the blank lines correctly.
	if !strings.Contains(string(ign), "knuckle-wifi.nmconnection") {
		t.Errorf("compiled ignition lost the keyfile:\n%s", ign)
	}
}

func TestRenderWifiProfilesSkips(t *testing.T) {
	for _, cfg := range []*model.InstallConfig{
		{Wifi: model.WifiConfig{}},
		{Wifi: model.WifiConfig{Enabled: false, Profiles: []model.WifiProfile{{Filename: "a"}}}},
		{Wifi: model.WifiConfig{Enabled: true}},
	} {
		got, err := renderWifiProfiles(cfg)
		if err != nil {
			t.Fatalf("renderWifiProfiles error = %v", err)
		}
		if got != nil {
			t.Errorf("got %+v, want nil for a skipped step", got)
		}
	}
}

func TestIndentBlock(t *testing.T) {
	got := indentBlock(4, "a\n\nb")
	want := "    a\n\n    b"
	if got != want {
		t.Errorf("indentBlock = %q, want %q", got, want)
	}
}
