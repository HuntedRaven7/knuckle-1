# Testlab — knuckle Local VM & ISO Testing

Run knuckle interactively or headlessly inside a real Flatcar Container Linux VM.

## Quick Start

```bash
cd ~/src/knuckle

just vm           # interactive TUI — real install → auto-boots installed system
just vm-e2e       # automated 4-pass: DHCP · static · sysext · NVIDIA
just boot-iso     # build ISO → boot in QEMU serial console (Ctrl-a x to quit)
just e2e          # build ISO → launch interactive VM in Ghostty
just headless-test  # no VM — config generation only (runs anywhere)
just stop         # kill running VM
just clean        # kill VM + remove all artifacts
```

## How `just vm` Works

1. Builds binary (`linux/amd64`, CGO_ENABLED=0)
2. Creates qcow2 overlay on cached Flatcar base image (instant)
3. Creates 20G target disk
4. Boots QEMU with port-forward (2222→22)
5. Waits for SSH (~6s with KVM)
6. SCPs binary to `/tmp/knuckle`
7. SSHes in — runs knuckle interactively (real install, writes to /dev/vdb)
8. After install: kills installer VM and boots the installed target disk

**Antipattern:** Never embed the binary into Ignition via base64 (19 MB → 26 MB JSON).

## How `just vm-e2e` Works

Runs 4 automated passes back-to-back. No user interaction required.

| Pass | What it tests | Timeout |
|---|---|---|
| DHCP | Hostname, update strategy, locksmith | 15m |
| Static | `/etc/systemd/network/10-static.network` | 15m |
| Sysext | docker.raw present, `docker version` exits 0 | 25m |
| NVIDIA | NVIDIA driver sysext config, enabled-sysext.conf | 15m |

Each pass builds a fresh qcow2 overlay — passes are independent.

## How `just e2e` / `just boot-iso` Work

1. Builds ISO (if not already present in `output/`)
2. Opens Ghostty window with QEMU UEFI VM booting from ISO
3. GRUB menu appears (3s timeout), boots Flatcar
4. knuckle auto-launches on tty1 via systemd unit
5. User completes install interactively

After install, run `just boot-target` to boot the installed system.

## ISO Architecture

### Flatcar (`just iso`)

- **Kernel:** `flatcar_production_pxe.vmlinuz` (Flatcar CDN)
- **Initrd:** `flatcar_production_pxe_image.cpio.gz` + knuckle overlay cpio
- **Boot:** GRUB standalone EFI (`grub-mkstandalone`)
- **Assembly:** xorriso with El Torito EFI boot image
- **Overlay:** `/opt/knuckle` binary + `knuckle-installer.service`
- **UEFI only** (no BIOS/legacy)

Build deps: `x86_64-elf-grub-mkstandalone`, `xorriso`, `mtools`, `cpio`

### FCOS and uCore (`just build-fcos-iso`, `just build-ucore-iso`)

Both take a different route: they download the **upstream FCOS live ISO** and
build the installer ISO in two steps.

1. `coreos-installer iso customize --live-ignition` embeds a small Ignition
   config (~1.4 KB) that stages and starts knuckle on tty1.
2. `xorriso -map knuckle /knuckle -boot_image any replay` injects the binary as
   a plain ISO9660 file, preserving the hybrid MBR/GPT and both El Torito
   entries (BIOS isolinux + UEFI `efiboot.img`).

**The binary must be a file on the ISO, not part of the ignition.** This is
forced, not stylistic: coreos-installer embeds `--live-ignition` inside the
compressed initramfs, capped at **262144 bytes**, and fails with
`Compressed initramfs is too large`. A knuckle binary is ~18 MiB. The live
rootfs is not an alternative — FCOS ships it as an **EROFS** image
(`/images/pxeboot/rootfs.img`, a newc cpio wrapping `root.erofs`), which is
read-only and has no add-a-file path; rebuilding it needs root or `fuse-erofs`.

Shared implementation: `scripts/lib/coreos-iso.sh`. The two wrapper scripts
(`build-fcos-iso.sh`, `build-ucore-iso.sh`) only differ in the output label, so
fix a bug in the lib, not in both scripts.

Requires `coreos-installer` (`just tools-fcos`), `xorriso`, and `python3`.

**The uCore ISO's live medium is FCOS, not uCore.** uCore publishes no ISO — its
GitHub releases are changelogs with an empty `assets` array, images live on
`ghcr.io`, and the project states it ships no installer of its own. The ISO just
offers uCore as a target choice; the target disk gets a CoreOS deployment that a
first-boot `rpm-ostree rebase` turns into uCore.

### Custom live medium (`just build-wifi-base-iso`)

Both scripts above accept `--base-iso` (or `KNUCKLE_BASE_ISO`) to substitute a
custom live medium for the downloaded stock one. That is the hook the
WiFi-capable medium plugs into — see "Building a WiFi-capable live ISO" under
The WiFi step. `fetch_live_iso` in `scripts/lib/coreos-iso.sh` is the only
consumer, and it validates that the path exists before using it.

**A custom medium does not change what lands on the target disk.**
`coreos-installer` is always invoked with `--stream`, so the installed system is
a stock upstream FCOS deployment regardless of what the live ISO contained. WiFi
on the *installed* machine is handled by the first-boot layering unit in
`internal/ignition/wifi.go`, not by the medium.

### Verifying the built ISO (no boot required)

```bash
X=xorriso
$X -indev output/knuckle-ucore-installer-stable-amd64.iso -report_el_torito plain
# expect: Boot record: El Torito ; one BIOS + one UEFI boot img
$X -indev output/knuckle-ucore-installer-stable-amd64.iso -osirrox on -extract /knuckle /tmp/k
cmp /tmp/k bin/knuckle && echo "binary intact"
```

If `knuckle-installer.service` does not start, check the staging unit first —
it searches the media mount points for `/knuckle`:

```bash
systemctl status knuckle-stage-binary.service
```

**Known unverified assumption:** the staging unit finds the binary by globbing
`/media/*`, `/run/media/*`, `/mnt/*`. The ISO is definitely mounted in the live
environment (coreos-installer itself reads `/coreos/kargs.json` from it), but the
exact path has not been confirmed on a real boot. The unit exits non-zero and
logs `knuckle-stage: binary not found on the boot medium` if it comes up empty.

### Verifying a uCore install — two boots

```bash
# boot the installed disk; the rebase unit runs and reboots the machine
ssh -p 2222 core@127.0.0.1 'systemctl status ucore-knuckle-autorebase'

# after that reboot, confirm the machine is actually uCore and not stock CoreOS
ssh -p 2222 core@127.0.0.1 \
  'rpm-ostree status && systemctl is-active ucore-knuckle-autorebase && cat /etc/ucore-knuckle/rebased'
```

The sentinel file `/etc/ucore-knuckle/rebased` is what makes the unit idempotent;
without it the rebase would re-run on every boot.

## Post-Install Verification

```bash
# just vm boots the installed system automatically after knuckle exits.
# For vm-e2e, pass output shows SSH verification results.
# For ISO installs, reboot from knuckle's done screen, then:
ssh -p 2222 core@127.0.0.1 -o StrictHostKeyChecking=no \
  "hostname && uname -r && cat /etc/flatcar/update.conf"
```

## Key Facts

| Item | Value |
|---|---|
| Local VM SSH | `ssh -p 2222 core@127.0.0.1` |
| Target disk in VM | `/dev/vdb` (20G virtio) |
| Image format | qcow2 overlay (backing: `.vm/flatcar_base_amd64.img`) |
| Boot time (KVM) | ~6s to SSH |
| SSH options | `-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR` |

## The WiFi step

The WiFi wizard step hands the terminal to `nmtui`, then copies the
NetworkManager keyfiles it produced onto the target disk so the installed
machine comes up already joined.

Key mechanics, none of which are guessable from the code:

- **Terminal handoff uses `tea.ExecProcess`**, not the runner. `Runner.Run`
  captures output into a `Result` and gives the child no TTY, so a full-screen
  program would draw into a buffer and exit. `tea.ExecProcess` suspends the
  Bubble Tea program, releases the terminal, runs the child against the real
  TTY, and restores. `runner.InteractiveCommand` builds the `*exec.Cmd` so
  `exec.Command` stays inside `internal/runner`.
- **The profile must be written mode 0600.** NetworkManager silently ignores a
  keyfile that is group- or world-readable, so 0644 yields a machine that boots
  with no network while looking correctly provisioned.
- **Profiles only take effect on the NetworkManager targets** (FCOS, uCore).
  Flatcar uses systemd-networkd and Bluefin Server has no Ignition; the profiles
  are still written but inert, and the UI says so.
- **Captured keyfiles contain the PSK** — treat `model.WifiProfile.Contents` as
  a secret. Never log it or render it in a dry-run view.

Testing it end-to-end needs a real radio. What you can check without one:

```bash
# headless path — supplies a keyfile directly, no terminal needed
bin/knuckle --config wifi.json --headless --dry-run

# the generated config must contain the profile at 0600
go test ./internal/ignition/ -run Wifi -v
```

### Why nmtui often finds nothing — do not go hunting for a bug

This trips people up, so the reasoning is recorded here.

**The stock FCOS live ISO has nmtui but cannot use it.** This is the detail that
sends people looking in the wrong place. `NetworkManager-tui` *is* in upstream's
`manifests/networking-tools.yaml`, so the binary is present and `nmtui` launches
fine. What is missing is `NetworkManager-wifi` — a single plugin library,
`libnm-device-plugin-wifi.so` — plus `wpa_supplicant` and any adapter firmware.
nmtui starts, has no device type to show, and looks broken. In practice the
diagnostic you see on stock media is the *second* one below; the "nmtui is not
installed" branch is for a genuinely stripped image.

**`linux-firmware` does not give you WiFi firmware on Fedora.** This is the
other thing that sends people down a rabbit hole. Fedora split the wireless
blobs out of `linux-firmware` into per-vendor subpackages and made them
`Recommends` rather than `Requires`. FCOS composes with `recommends: false`, so
`linux-firmware` resolves to *no* wireless firmware at all on a CoreOS base.
There is no `linux-firmware-free` / `linux-firmware-nonfree` to ask for instead
— the split is by vendor, not by licence. The names that work are
`iwlwifi-mvm-firmware`, `iwlwifi-dvm-firmware`, `brcmfmac-firmware`,
`atheros-firmware`, `mt7xxx-firmware`, `realtek-firmware` and friends. There is
no `iwlwifi-ucode` either.

**Building your own uCore does not fix the installer.** uCore's README notes the
full `ucore` image adds "all wireless (wifi) card firmwares (CoreOS does not
include them)" — but that is the *installed* system. The live medium of the
uCore installer ISO is the stock FCOS live ISO, so firmware baked into a uCore
image never reaches the installer environment. See "Building a WiFi-capable
live ISO" below for the fix.

**The installed system is a separate problem with a separate fix.** A keyfile
alone is inert: `coreos-installer` is invoked with `--stream` and lays down a
*stock* upstream FCOS deployment, it does not derive the target from the live
ISO's package set. So even installing from a firmware-carrying custom ISO leaves
the target without a WiFi plugin. `internal/ignition/wifi.go` emits a first-boot
oneshot that layers the stack (`ignition.addWifiStackUnit`), following the
upstream Fedora recipe. Two consequences to know about:

- **The target needs network on its first boot** to fetch those packages. A
  machine with no ethernet connected cannot pull its own WiFi firmware. The
  Fedora docs call this out; it is inherent to layering, not a knuckle bug.
- **Layering stages a deployment, so the machine reboots once** afterwards.

**That is also why the step has a manual fallback.** Press `m` to type details
in by hand when the installer cannot see a network. Because the stack is layered
onto the target, the hand-built keyfile does land the machine on WiFi — the
firmware does not have to be visible from the installer at all.

**Do not hardcode "CoreOS has no WiFi".** `Wizard.WifiPreflight` probes the
running machine (`runner.LookPath("nmtui")` plus `/sys/class/net/*/wireless`) and
is cached, so a custom ISO that *does* ship firmware gets a clean, warning-free
step automatically.

If the step seems broken, check the diagnosis it now prints before suspecting
the wizard:

- `nmtui is not installed on this live image` — only on a stripped image; stock
  FCOS media has nmtui
- `No wireless adapter detected` — the usual one on stock FCOS: the WiFi plugin
  and firmware are missing, so there is no radio to scan with

### Building a WiFi-capable live ISO

`just build-wifi-base-iso` produces a live medium that carries the WiFi stack, so
nmtui can actually scan. Hand the result to the installer build with
`KNUCKLE_BASE_ISO`:

```bash
just build-wifi-base                                     # hours, ~200GB disk
KNUCKLE_BASE_ISO=output/knuckle-wifi-base-testing-devel-x86_64.iso just build-fcos-iso
```

**What it costs.** This is a real Fedora CoreOS build, not a repack: podman,
`/dev/kvm`, ~10.5 GiB RAM, 6 CPUs, ~200 GB free disk (about 50 GB of that is
the supermin cache) and a couple of hours. It cannot run in CI, which is why the
repo covers the config materialisation and ISO discovery in BATS and leaves the
build itself to the operator.

**How it works.** `cosa/manifest.yaml` includes upstream's `manifest.yaml` and
adds `NetworkManager-wifi` plus the vendor firmware.
`scripts/lib/cosa-config.sh` materialises the derived config repo: it clones
`fedora-coreos-config` and symlinks its components in, which is the layout
upstream's own README recommends. `cosa/image.yaml` overrides one value for
safety — see below.

Four things that are easy to get wrong here:

1. **There is no live-only package list.** The live ISO's rootfs is the same
   deployed tree as the metal and qemu images, so added packages land
   everywhere. There is no way to add firmware to just the live target.
2. **The lockfile does not need updating.** A package in `packages:` that is
   absent from `manifest-lock.<arch>.json` resolves to the newest build in the
   enabled repos; only `cosa build --strict` rejects that, and this build does
   not use it. The tradeoff is that the added firmware is *not* version-pinned.
   (`cosa fetch --update-lockfile` no longer exists, so hand-editing the
   lockfile is the only alternative if you ever do need a pin.)
3. **`overlay.d` must be symlinked as a whole directory.** On the Fedora-ID
   branch of `manifests/shared-el.yaml` the `ostree-layers` entries are relative
   (`overlay.d/05core`), not submodule-qualified, so the derived repo has to
   present those directories itself.
4. **`container-imgref` is overridden deliberately.** Upstream points it at
   `quay.io/fedora/fedora-coreos` — the official repository. This image is FCOS
   plus packages the FCOS project deliberately excludes, built locally and not
   signed by the Fedora release keys. Leaving that value in place would let a
   `cosa push-container` aim a modified image at a repo whose contents are
   trusted as official. `cosa/image.yaml` points it at a knuckle-owned name so
   the mistake is loud rather than silent.

**Supply-chain note.** Because the added packages are not lockfile-pinned and
the image is not signed by Fedora, treat a locally built medium as a development
artifact. Do not distribute it as a release, and do not re-tag it as official
Fedora CoreOS.

## Agent Limitations

**Agents CANNOT verify TUI interactive behavior.**

| Can verify | Cannot verify |
|---|---|
| Binary builds | Forms render correctly |
| Unit tests pass | User can navigate steps |
| VM boots (SSH works) | Install progress animates |
| Installed system config | TUI doesn't crash mid-flow |
| Headless mode output | Interactive experience |

Correct protocol: launch a Ghostty terminal for the user, say "launched — awaiting feedback", wait.

```bash
ghostty --gtk-single-instance=false -e bash -c "cd ~/src/knuckle && just vm ''" &
```

## Remote Testing on Ghost

`just vm-e2e` and `just headless-test` are ghost-safe (headless, no display).
`just vm`, `just e2e`, `just boot-iso` require local display — **never run these on ghost**.

QEMU port-forward binds `127.0.0.1:2222`. To SSH into a VM running on ghost:
```bash
ssh jorge@ghost   # then from ghost:
ssh -p 2222 core@127.0.0.1
```
Never `ssh -p 2222 core@jorge@ghost` — that reaches ghost's sshd.

## Gotchas

| Problem | Fix |
|---|---|
| Ghostty window invisible | `--gtk-single-instance=false` |
| ISO doesn't boot (EFI shell) | Need OVMF firmware (`-drive if=pflash,...`) |
| GRUB "file not found" | Needs `search --file /vmlinuz --set=root` in grub.cfg |
| VM port 2222 in use | `just stop` |
| Base image missing | First `just vm` downloads ~470 MB Flatcar image (cached in `.vm/`) |
| `just e2e` fails on ghost | Uses `-display gtk` — local display required |
