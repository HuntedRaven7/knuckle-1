package ignition

import (
	"fmt"
	"strings"

	"github.com/projectbluefin/knuckle/internal/model"
	"github.com/projectbluefin/knuckle/internal/validate"
)

// WifiTargetDir is where captured NetworkManager profiles are written on the
// installed system. It matches NMConnectionDir in the model.
const WifiTargetDir = model.NMConnectionDir

// renderWifiProfiles turns the captured keyfiles into validated, pre-indented
// entries ready to be embedded.
//
// Every filename is validated here, at the point where it becomes part of a
// path written to a disk, rather than trusted from the prober that read it: a
// name containing a separator or ".." would otherwise write outside the profile
// directory on the target.
//
// Returns nil when the step was skipped, so both callers can pass the result
// straight through without their own emptiness check.
func renderWifiProfiles(cfg *model.InstallConfig) ([]renderedWifiProfile, error) {
	if !cfg.Wifi.Enabled || len(cfg.Wifi.Profiles) == 0 {
		return nil, nil
	}

	out := make([]renderedWifiProfile, 0, len(cfg.Wifi.Profiles))
	for _, p := range cfg.Wifi.Profiles {
		if err := validate.WifiProfileFilename(p.Filename); err != nil {
			return nil, fmt.Errorf("wifi profile: %w", err)
		}
		body := strings.TrimRight(p.Contents, "\n")
		if strings.TrimSpace(body) == "" {
			return nil, fmt.Errorf("wifi profile %q is empty", p.Filename)
		}
		out = append(out, renderedWifiProfile{Filename: p.Filename, Body: body})
	}
	return out, nil
}

// addWifiProfiles adds the captured keyfiles to a Builder-based config (the FCOS
// and uCore generators).
//
// mode 0600 is not cosmetic: NetworkManager refuses to use a keyfile that is
// group- or world-readable, so a 0644 file would be silently ignored and the
// machine would boot with no network while looking correctly provisioned.
func addWifiProfiles(b *Builder, cfg *model.InstallConfig) error {
	profiles, err := renderWifiProfiles(cfg)
	if err != nil {
		return err
	}
	for _, p := range profiles {
		b.AddStorageFile("- path: " + WifiTargetDir + "/" + p.Filename + "\n" +
			"  mode: 0600\n" +
			"  overwrite: true\n" +
			"  contents:\n" +
			"    inline: |\n" +
			indentBlock(10, p.Body) + "\n")
	}
	return nil
}

// indentBlock prefixes every non-empty line with n spaces. Multi-line content
// inside a YAML block scalar must be indented past the block marker or YAML
// terminates the scalar early and the document fails to parse.
func indentBlock(n int, s string) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}
