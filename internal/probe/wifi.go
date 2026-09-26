package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/projectbluefin/knuckle/internal/model"
	"github.com/projectbluefin/knuckle/internal/validate"
)

// nmProfileSuffix is the extension NetworkManager gives its keyfiles.
const nmProfileSuffix = ".nmconnection"

// ListWifiProfiles reads the NetworkManager connection profiles present in dir
// and returns them as WifiProfiles, ready to be written onto a target system.
//
// The keyfiles are copied verbatim, including any pre-shared key, because
// NetworkManager only reads a profile whose contents match its own formatting.
// The result therefore contains secrets and must not be logged.
//
// Files that fail WifiProfileFilename validation are skipped rather than
// returned: the filename becomes a path in the target's Ignition config, so a
// name that could escape the profile directory is refused outright. An
// unreadable or malformed file is also skipped, since one bad profile on the
// installer host must not be able to fail the whole install.
//
// An absent directory is not an error: it simply means no network has been
// joined yet, which is the normal state before the user runs nmtui.
func ListWifiProfiles(dir string) ([]model.WifiProfile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var profiles []model.WifiProfile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, nmProfileSuffix) {
			continue
		}
		if err := validate.WifiProfileFilename(name); err != nil {
			continue
		}

		path := filepath.Join(dir, name)
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(body)

		profiles = append(profiles, model.WifiProfile{
			Filename: name,
			SSID:     nmKeyfileValue(text, "ssid"),
			Secured:  nmKeyfileValue(text, "psk") != "",
			Contents: text,
		})
	}
	return profiles, nil
}

// nmKeyfileValue returns the value of a top-level key in a NetworkManager
// keyfile, or "" when absent. Values are read verbatim; this is display metadata
// only and is never used to build a path or a command.
func nmKeyfileValue(contents, key string) string {
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		// Skip section headers and comments.
		if line == "" || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(name) != key {
			continue
		}
		return strings.TrimSpace(value)
	}
	return ""
}

// IsWirelessInterface reports whether the named network interface is wireless.
//
// Linux exposes this as the presence of a "wireless" sysfs attribute, which
// only exists on 802.11 devices. That is more reliable than matching interface
// name prefixes, which differ between udev versions and distributions.
func IsWirelessInterface(name string) bool {
	return isWirelessInterfaceIn("/sys/class/net", name)
}

// isWirelessInterfaceIn is the testable core, with netDir standing in for
// /sys/class/net.
func isWirelessInterfaceIn(netDir, name string) bool {
	if name == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(netDir, name, "wireless"))
	return err == nil
}

// WirelessInterfaces returns the names of every wireless interface currently
// present.
//
// A wireless *adapter* with no loaded driver or missing firmware still appears
// here, because the kernel registers the netdev regardless. That makes the
// result a statement about hardware presence, not about whether scanning will
// actually work — see the installer's WiFi preflight for the latter.
func WirelessInterfaces() ([]string, error) {
	return wirelessInterfacesIn("/sys/class/net")
}

// wirelessInterfacesIn is the testable core: netDir stands in for
// /sys/class/net, so both the success and the error path are reachable without
// a real wireless device.
func wirelessInterfacesIn(netDir string) ([]string, error) {
	entries, err := os.ReadDir(netDir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", netDir, err)
	}

	var out []string
	for _, e := range entries {
		if e.IsDir() && isWirelessInterfaceIn(netDir, e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out, nil
}
