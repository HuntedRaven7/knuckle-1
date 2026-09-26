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

**Minimal CoreOS live media has no wireless firmware, and often no nmtui
either.** Fedora's own wiki lists `nmtui` as the separate `NetworkManager-tui`
package, and a 2026-08-07 Fedora discussion thread on exactly the uCore case
requires `rpm-ostree install NetworkManager-wifi iwlwifi-mvm-firmware`, linking
to "building an iso with wifi pre-installed". So on the stock FCOS live ISO that
the knuckle uCore ISO is built from, nmtui launches and finds nothing, because
there is no radio to scan with.

**Building your own uCore does not fix this.** uCore's README notes the full
`ucore` image adds "all wireless (wifi) card firmwares (CoreOS does not include
them)" — but that is the *installed* system. The live medium of the uCore
installer ISO is the stock FCOS live ISO, not uCore, so firmware baked into a
uCore image never reaches the installer environment. Making the installer scan
would need a custom **live ISO** (coreos-assembler) carrying
`NetworkManager-tui`, `NetworkManager-wifi` and `linux-firmware`.

**That is why the step has a manual fallback.** The step's goal is to get the
*installed* system onto a network, and the full uCore image does ship the
firmware — so a hand-entered keyfile still lands the machine on WiFi even
though the installer could never see a network. Press `m`.

**Do not hardcode "CoreOS has no WiFi".** `Wizard.WifiPreflight` probes the
running machine (`runner.LookPath("nmtui")` plus `/sys/class/net/*/wireless`) and
is cached, so a custom ISO that *does* ship firmware gets a clean, warning-free
step automatically.

If the step seems broken, check the diagnosis it now prints before suspecting
the wizard:

- `nmtui is not installed on this live image` — expected on stock FCOS media
- `No wireless adapter detected` — no firmware/driver for the adapter

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
