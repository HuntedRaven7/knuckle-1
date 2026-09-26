package probe

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/projectbluefin/knuckle/internal/model"
)

func writeProfile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func TestListWifiProfiles(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "secured.nmconnection", "[connection]\nid=Secured\ntype=wifi\n\n[wifi]\nssid=Secured\n\n[wifi-security]\npsk=hunter2\n")
	writeProfile(t, dir, "open.nmconnection", "[connection]\nid=Open\ntype=wifi\n\n[wifi]\nssid=OpenNet\n")
	// Not a keyfile — must be ignored.
	writeProfile(t, dir, "notes.txt", "hello")
	writeProfile(t, dir, "unrelated", "hello")

	got, err := ListWifiProfiles(dir)
	if err != nil {
		t.Fatalf("ListWifiProfiles() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d profiles, want 2: %+v", len(got), got)
	}

	byName := map[string]model.WifiProfile{}
	for _, p := range got {
		byName[p.Filename] = p
	}

	sec, ok := byName["secured.nmconnection"]
	if !ok {
		t.Fatal("secured.nmconnection missing")
	}
	if sec.SSID != "Secured" {
		t.Errorf("SSID = %q, want %q", sec.SSID, "Secured")
	}
	if !sec.Secured {
		t.Error("secured.nmconnection should report Secured=true")
	}
	if sec.Contents == "" {
		t.Error("Contents must be carried verbatim so NetworkManager can read it")
	}

	open, ok := byName["open.nmconnection"]
	if !ok {
		t.Fatal("open.nmconnection missing")
	}
	if open.SSID != "OpenNet" {
		t.Errorf("SSID = %q, want %q", open.SSID, "OpenNet")
	}
	if open.Secured {
		t.Error("open.nmconnection has no psk and must report Secured=false")
	}
}

// A missing directory is the normal pre-nmtui state, not an error.
func TestListWifiProfilesMissingDir(t *testing.T) {
	got, err := ListWifiProfiles(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("missing dir should not error, got %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

// The filename becomes a write path on the target, so unsafe names are dropped
// at the boundary rather than carried forward.
func TestListWifiProfilesRejectsUnsafeNames(t *testing.T) {
	dir := t.TempDir()
	// os.WriteFile with a path containing a separator writes into a subdir, so
	// create the traversal target explicitly.
	if err := os.MkdirAll(filepath.Join(dir, "..", "escape"), 0o755); err == nil {
		writeProfile(t, filepath.Join(dir, "..", "escape"), "evil.nmconnection", "[connection]\n")
	}
	writeProfile(t, dir, "good.nmconnection", "[connection]\nssid=Good\n")
	writeProfile(t, dir, ".hidden.nmconnection", "[connection]\nssid=Hidden\n")

	got, err := ListWifiProfiles(dir)
	if err != nil {
		t.Fatalf("ListWifiProfiles() error = %v", err)
	}
	for _, p := range got {
		if p.Filename == ".hidden.nmconnection" {
			t.Errorf("unsafe filename was accepted: %q", p.Filename)
		}
	}
	if len(got) != 1 || got[0].Filename != "good.nmconnection" {
		t.Errorf("got %+v, want only good.nmconnection", got)
	}
}

func TestListWifiProfilesSkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub.nmconnection"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	got, err := ListWifiProfiles(dir)
	if err != nil {
		t.Fatalf("ListWifiProfiles() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("directories must be skipped, got %+v", got)
	}
}

// A directory that exists but cannot be read is a real error, distinct from a
// missing one — the user may need to know their profile state is unknown.
func TestListWifiProfilesUnreadableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission bits")
	}
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o000); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	got, err := ListWifiProfiles(dir)
	if err == nil {
		t.Fatalf("expected an error for an unreadable directory, got %+v", got)
	}
	if got != nil {
		t.Errorf("got %+v, want nil alongside the error", got)
	}
}

// One unreadable keyfile must not fail the whole capture — the remaining
// profiles are still usable.
func TestListWifiProfilesSkipsUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission bits")
	}
	dir := t.TempDir()
	writeProfile(t, dir, "readable.nmconnection", "[connection]\nssid=Readable\n")

	locked := filepath.Join(dir, "locked.nmconnection")
	if err := os.WriteFile(locked, []byte("[connection]\n"), 0o000); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o600) })

	got, err := ListWifiProfiles(dir)
	if err != nil {
		t.Fatalf("ListWifiProfiles() error = %v", err)
	}
	if len(got) != 1 || got[0].Filename != "readable.nmconnection" {
		t.Errorf("got %+v, want only the readable profile", got)
	}
}

func TestNMKeyfileValue(t *testing.T) {
	body := "# a comment\n[connection]\nid=Foo\nssid=Bar\n  spaced  =  val  \nempty=\npsk=secret\n"
	tests := []struct {
		key  string
		want string
	}{
		{"id", "Foo"},
		{"ssid", "Bar"},
		{"spaced", "val"},
		{"empty", ""},
		{"psk", "secret"},
		{"missing", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := nmKeyfileValue(body, tt.key); got != tt.want {
			t.Errorf("nmKeyfileValue(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

// A key with an empty name must not match the trailing "=" line above.
func TestNMKeyfileValueDoesNotMatchSectionLines(t *testing.T) {
	if got := nmKeyfileValue("[connection]\nid=x\n", "id"); got != "x" {
		t.Errorf("nmKeyfileValue = %q, want x", got)
	}
}

func TestIsWirelessInterface(t *testing.T) {
	// lo always exists and has no "wireless" attribute.
	if IsWirelessInterface("lo") {
		t.Error("lo must not be reported as wireless")
	}
	if IsWirelessInterface("") {
		t.Error("empty interface name must not be reported as wireless")
	}
	if IsWirelessInterface("definitely-not-an-iface-xyzzy") {
		t.Error("nonexistent interface must not be reported as wireless")
	}
}

// A hand-built keyfile must be readable by the same reader the installer uses,
// or the profile would be written to the target and then silently ignored.
func TestListWifiProfilesReadsHandBuiltKeyfile(t *testing.T) {
	dir := t.TempDir()
	body := model.BuildWifiKeyfile("HomeWifi", "hunter2", true)
	if err := os.WriteFile(filepath.Join(dir, model.ManualProfileFilename), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := ListWifiProfiles(dir)
	if err != nil {
		t.Fatalf("ListWifiProfiles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d profiles, want 1", len(got))
	}
	if got[0].SSID != "HomeWifi" {
		t.Errorf("SSID = %q, want HomeWifi", got[0].SSID)
	}
	if !got[0].Secured {
		t.Error("a keyfile with a psk must be detected as secured")
	}
}

func TestWirelessInterfaces(t *testing.T) {
	got, err := WirelessInterfaces()
	if err != nil {
		t.Fatalf("WirelessInterfaces() error = %v", err)
	}
	// lo is never wireless, so a positive result means sysfs was really read.
	for _, iface := range got {
		if iface == "lo" {
			t.Error("lo must never be reported as wireless")
		}
		if !IsWirelessInterface(iface) {
			t.Errorf("%q was reported but has no wireless attribute", iface)
		}
	}
}

// The error path has to be reachable, so the directory is a parameter.
func TestWirelessInterfacesIn(t *testing.T) {
	dir := t.TempDir()
	// A wired-looking interface: a directory with no "wireless" attribute.
	if err := os.Mkdir(filepath.Join(dir, "eth0"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	// A wireless one, and a plain file that is not an interface at all.
	if err := os.MkdirAll(filepath.Join(dir, "wlan0", "wireless"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notaniface"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := wirelessInterfacesIn(dir)
	if err != nil {
		t.Fatalf("wirelessInterfacesIn() error = %v", err)
	}
	if len(got) != 1 || got[0] != "wlan0" {
		t.Errorf("got %v, want [wlan0]", got)
	}
}

func TestIsWirelessInterfaceIn(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "wlan0", "wireless"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if !isWirelessInterfaceIn(dir, "wlan0") {
		t.Error("wlan0 has a wireless attribute and should be detected")
	}
	if isWirelessInterfaceIn(dir, "eth0") {
		t.Error("eth0 has no wireless attribute")
	}
	if isWirelessInterfaceIn(dir, "") {
		t.Error("empty name should never be wireless")
	}
}

func TestWirelessInterfacesInError(t *testing.T) {
	if _, err := wirelessInterfacesIn(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected an error for a missing directory")
	}
}
