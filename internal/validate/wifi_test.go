package validate

import "testing"

func TestWifiProfileFilename(t *testing.T) {
	valid := []string{
		"knuckle-wifi.nmconnection",
		"HomeWifi.nmconnection",
		"a.nmconnection",
		"guest_network-2.nmconnection",
		"0.nmconnection",
	}
	for _, name := range valid {
		if err := WifiProfileFilename(name); err != nil {
			t.Errorf("WifiProfileFilename(%q) = %v, want nil", name, err)
		}
	}
}

// The filename becomes a path on the target disk, so anything that could climb
// out of the profile directory must be rejected.
func TestWifiProfileFilenameRejectsTraversal(t *testing.T) {
	invalid := []string{
		"",
		".",
		"..",
		"../etc/shadow",
		"../../etc/passwd",
		"/etc/shadow",
		"/absolute.nmconnection",
		"dir/profile.nmconnection",
		"nested/../profile.nmconnection",
		".hidden.nmconnection",
		"pro file.nmconnection",
		"profile\n.nmconnection",
		"profile\x00.nmconnection",
		"pro$file.nmconnection",
		"pro;file.nmconnection",
		"pro`file`",
		"pro|file",
		"pro&file",
	}
	for _, name := range invalid {
		if err := WifiProfileFilename(name); err == nil {
			t.Errorf("WifiProfileFilename(%q) = nil, want an error", name)
		}
	}
}
