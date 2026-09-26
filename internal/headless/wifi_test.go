package headless

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/projectbluefin/knuckle/internal/model"
)

const wifiKeyfile = `[connection]
id=HomeWifi
type=wifi

[wifi]
ssid=HomeWifi
mode=infrastructure

[wifi-security]
key-mgmt=wpa-psk
psk=supersecret123`

func baseConfig() *Config {
	return &Config{
		OS:       model.OSFlatcar,
		Hostname: "wifi-node",
		Timezone: "UTC",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAA test"}}},
		Disk:     "/dev/vdb",
	}
}

func TestWifiToModel(t *testing.T) {
	tests := []struct {
		name string
		in   *WifiConfig
		want model.WifiConfig
	}{
		{
			name: "nil is skipped",
			in:   nil,
			want: model.WifiConfig{},
		},
		{
			name: "empty profile list is skipped",
			in:   &WifiConfig{},
			want: model.WifiConfig{},
		},
		{
			name: "secured profile is parsed for display",
			in:   &WifiConfig{Profiles: []WifiProfile{{Filename: "home.nmconnection", Contents: wifiKeyfile}}},
			want: model.WifiConfig{Enabled: true, Profiles: []model.WifiProfile{{
				Filename: "home.nmconnection",
				SSID:     "HomeWifi",
				Secured:  true,
				Contents: wifiKeyfile,
			}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in.ToModel()
			if len(got.Profiles) != len(tt.want.Profiles) {
				t.Fatalf("ToModel() = %+v, want %+v", got, tt.want)
			}
			if len(got.Profiles) == 1 {
				if got.Profiles[0] != tt.want.Profiles[0] {
					t.Errorf("profile = %+v, want %+v", got.Profiles[0], tt.want.Profiles[0])
				}
			}
			if got.Enabled != tt.want.Enabled {
				t.Errorf("Enabled = %v, want %v", got.Enabled, tt.want.Enabled)
			}
		})
	}
}

// An open network has no psk line, and must not be labelled secured.
func TestWifiSSIDAndSecuredParsing(t *testing.T) {
	open := "[connection]\nid=Open\ntype=wifi\n\n[wifi]\nssid=OpenNet\n"
	cfg := &WifiConfig{Profiles: []WifiProfile{{Filename: "o.nmconnection", Contents: open}}}

	got := cfg.ToModel()
	if len(got.Profiles) != 1 {
		t.Fatalf("got %+v", got)
	}
	if got.Profiles[0].SSID != "OpenNet" {
		t.Errorf("SSID = %q, want OpenNet", got.Profiles[0].SSID)
	}
	if got.Profiles[0].Secured {
		t.Error("an open network must not report Secured=true")
	}
}

// A keyfile with no parsable ssid still works; SSID is display-only.
func TestWifiSSIDUnparsable(t *testing.T) {
	cfg := &WifiConfig{Profiles: []WifiProfile{{Filename: "x.nmconnection", Contents: "garbage"}}}
	got := cfg.ToModel()
	if len(got.Profiles) != 1 {
		t.Fatalf("got %+v, want the profile retained", got)
	}
	if got.Profiles[0].SSID != "" {
		t.Errorf("SSID = %q, want empty", got.Profiles[0].SSID)
	}
}

func TestWifiJSONRoundTrip(t *testing.T) {
	raw := []byte(`{
		"os": "fcos",
		"hostname": "wifi-node",
		"network": {"mode": "dhcp"},
		"users": [{"username": "core", "ssh_keys": ["ssh-ed25519 AAAA test"]}],
		"disk": "/dev/vdb",
		"wifi": {"profiles": [{"filename": "home.nmconnection", "contents": "[wifi]\nssid=HomeWifi\n"}]}
	}`)

	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg.Wifi == nil || len(cfg.Wifi.Profiles) != 1 {
		t.Fatalf("wifi block did not decode: %+v", cfg.Wifi)
	}
	if cfg.Wifi.Profiles[0].Filename != "home.nmconnection" {
		t.Errorf("filename = %q", cfg.Wifi.Profiles[0].Filename)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestValidateWifiProfiles(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *WifiConfig
		wantOK bool
	}{
		{"nil wifi is valid", nil, true},
		{"valid profile", &WifiConfig{Profiles: []WifiProfile{{Filename: "a.nmconnection", Contents: "x"}}}, true},
		{
			name:   "traversal filename is rejected",
			cfg:    &WifiConfig{Profiles: []WifiProfile{{Filename: "../escape.nmconnection", Contents: "x"}}},
			wantOK: false,
		},
		{
			name:   "absolute filename is rejected",
			cfg:    &WifiConfig{Profiles: []WifiProfile{{Filename: "/etc/shadow", Contents: "x"}}},
			wantOK: false,
		},
		{
			name:   "empty contents is rejected",
			cfg:    &WifiConfig{Profiles: []WifiProfile{{Filename: "a.nmconnection", Contents: "  \n "}}},
			wantOK: false,
		},
		{
			name:   "second profile is still validated",
			cfg:    &WifiConfig{Profiles: []WifiProfile{{Filename: "a.nmconnection", Contents: "x"}, {Filename: "..", Contents: "x"}}},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseConfig()
			cfg.Wifi = tt.cfg
			err := cfg.Validate()
			if tt.wantOK && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
			if !tt.wantOK {
				if err == nil {
					t.Error("Validate() = nil, want an error")
				} else if !strings.Contains(err.Error(), "wifi") {
					t.Errorf("error %q should name the wifi field", err)
				}
			}
		})
	}
}

func TestToInstallConfigCarriesWifi(t *testing.T) {
	cfg := baseConfig()
	cfg.Wifi = &WifiConfig{Profiles: []WifiProfile{{Filename: "home.nmconnection", Contents: wifiKeyfile}}}

	ic := cfg.ToInstallConfig()
	if !ic.Wifi.Enabled || len(ic.Wifi.Profiles) != 1 {
		t.Fatalf("Wifi = %+v, want the profile carried through", ic.Wifi)
	}
	if ic.Wifi.Profiles[0].SSID != "HomeWifi" {
		t.Errorf("SSID = %q, want HomeWifi", ic.Wifi.Profiles[0].SSID)
	}
}

func TestToInstallConfigWifiOmitted(t *testing.T) {
	ic := baseConfig().ToInstallConfig()
	if ic.Wifi.Enabled || len(ic.Wifi.Profiles) != 0 {
		t.Errorf("Wifi = %+v, want empty when not configured", ic.Wifi)
	}
}
