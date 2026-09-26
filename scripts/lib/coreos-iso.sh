#!/usr/bin/env bash
# Shared implementation for building a knuckle installer ISO on a Fedora
# CoreOS live image. Used by scripts/build-fcos-iso.sh and
# scripts/build-ucore-iso.sh.
#
# The build has four stages:
#   1. Resolve (or cross-compile) the knuckle binary for the target arch.
#   2. Fetch the upstream FCOS live ISO, caching it per stream+arch.
#   3. Generate a "live ignition" config that starts the knuckle TUI on tty1.
#   4. Produce the output ISO: coreos-installer embeds the ignition, then
#      xorriso injects the knuckle binary as an ISO file.
#
# # Why the binary is an ISO file and not part of the ignition
#
# coreos-installer embeds --live-ignition inside the live environment's
# compressed initramfs and refuses to build if the result exceeds 262144 bytes
# ("Compressed initramfs is too large"). A knuckle binary is ~18 MiB, so it
# cannot be embedded as an Ignition data URL. The live rootfs cannot be used
# either: FCOS ships it as an EROFS image, which is read-only and has no
# "add a file" path. So the binary is placed on the ISO itself and staged onto
# the live rootfs by a systemd unit at boot. The medium stays offline-bootable.
#
# Sourcing this file provides:
#   build_coreos_iso <os-label> <stream> <arch> <binary-override> <ssh-key>
#
# shellcheck shell=bash

# coreos-installer's hard cap on the embedded live ignition.
LIVE_IGNITION_MAX_BYTES=262144

# resolve_knuckle_binary echoes the path to a knuckle binary for $1 (arch),
# preferring an explicit override, then an arch-specific build, then any
# previously built binary. Sets KNUCKLE_BINARY as a side effect.
resolve_knuckle_binary() {
    local arch="$1" override="$2"
    local root="$ROOT_DIR"

    if [[ -n "$override" ]]; then
        KNUCKLE_BINARY="$override"
    elif [[ -f "$root/bin/knuckle-${arch}" ]]; then
        KNUCKLE_BINARY="$root/bin/knuckle-${arch}"
    elif [[ -f "$root/bin/knuckle" ]]; then
        KNUCKLE_BINARY="$root/bin/knuckle"
    elif [[ -f "$root/knuckle" ]]; then
        # CI fallback: binary placed at repo root (no bin/ dir in CI artifacts)
        KNUCKLE_BINARY="$root/knuckle"
    else
        KNUCKLE_BINARY=""
    fi
}

# build_knuckle_binary cross-compiles knuckle for $1 (arch) unless a binary is
# already present, and sets KNUCKLE_BINARY.
build_knuckle_binary() {
    local arch="$1" override="$2"
    resolve_knuckle_binary "$arch" "$override"

    if [[ -n "$KNUCKLE_BINARY" && -f "$KNUCKLE_BINARY" ]]; then
        echo "[1/4] Using existing knuckle binary: $KNUCKLE_BINARY"
        return
    fi

    echo "[1/4] Building knuckle ($arch)..."
    local version
    version="$(git -C "$ROOT_DIR" describe --tags --always 2>/dev/null || echo dev)"
    (cd "$ROOT_DIR" && GOOS=linux GOARCH="$arch" CGO_ENABLED=0 \
        go build -ldflags="-s -w -X main.version=${version}" -o "bin/knuckle-${arch}" ./cmd/knuckle)
    KNUCKLE_BINARY="$ROOT_DIR/bin/knuckle-${arch}"
}

# fetch_live_iso downloads (or reuses a cached) FCOS live ISO for the given
# stream and arch, and sets LIVE_ISO. $1 = stream, $2 = arch, $3 = coreos arch.
#
# When KNUCKLE_BASE_ISO is set, that ISO is used instead of downloading one.
# This is how a custom ISO built with coreos-assembler — for example one
# carrying NetworkManager-tui, NetworkManager-wifi and linux-firmware so the
# installer can scan WiFi — is fed into the build.
fetch_live_iso() {
    local stream="$1" arch="$2" coreos_arch="$3"

    if [[ -n "${KNUCKLE_BASE_ISO:-}" ]]; then
        if [[ ! -f "$KNUCKLE_BASE_ISO" ]]; then
            echo "error: base ISO not found: $KNUCKLE_BASE_ISO" >&2
            return 1
        fi
        echo "  Using custom base ISO: $KNUCKLE_BASE_ISO ($(du -h "$KNUCKLE_BASE_ISO" | cut -f1))"
        LIVE_ISO="$KNUCKLE_BASE_ISO"
        return
    fi

    local iso_cache_dir="$BUILD_DIR/iso-cache-${stream}-${arch}"
    mkdir -p "$iso_cache_dir"

    local existing
    existing="$(ls "$iso_cache_dir"/*.iso 2>/dev/null | head -1 || true)"
    if [[ -n "$existing" ]]; then
        echo "  Using cached FCOS ISO: $(basename "$existing")"
        LIVE_ISO="$existing"
        return
    fi

    echo "  Fetching from builds.coreos.fedoraproject.org..."
    coreos-installer download \
        --stream "$stream" \
        --platform metal \
        --format iso \
        --architecture "$coreos_arch" \
        --directory "$iso_cache_dir"
    LIVE_ISO="$(ls "$iso_cache_dir"/*.iso | head -1)"
    echo "  Downloaded: $(basename "$LIVE_ISO") ($(du -h "$LIVE_ISO" | cut -f1))"
}

# add_binary_to_iso injects the knuckle binary into an ISO as a plain ISO9660
# file at /knuckle, preserving all boot structures.
#
# Why not the live ignition: coreos-installer embeds --live-ignition inside the
# compressed initramfs, which is capped at 262144 bytes. A knuckle binary is
# ~18 MiB, so embedding it as an Ignition data URL is impossible — the build
# dies with "Compressed initramfs is too large". The live rootfs itself is an
# EROFS image and cannot be modified either. Putting the binary on the ISO
# itself sidesteps both limits and keeps the medium bootable offline.
#
# -boot_image any replay copies the original El Torito (BIOS isolinux + UEFI
# efiboot.img) and hybrid MBR/GPT, so both boot paths survive the rewrite.
#
# $1 = input ISO, $2 = output ISO, $3 = binary, $4 = volume id.
add_binary_to_iso() {
    local in_iso="$1" out_iso="$2" binary="$3" volid="$4"

    echo "  adding $(basename "$binary") to the ISO as /knuckle ($(du -h "$binary" | cut -f1))..."
    rm -f "$out_iso"
    xorriso -indev "$in_iso" \
            -outdev "$out_iso" \
            -map "$binary" /knuckle \
            -boot_image any replay \
            -volid "$volid" \
            -commit > /dev/null 2>&1 \
        || { echo "error: xorriso failed to inject the binary into the ISO" >&2; return 1; }
}

# write_live_ignition generates the Ignition config applied inside the live
# environment. It does NOT carry the knuckle binary — see add_binary_to_iso —
# only the units that stage the binary off the ISO and then start the TUI.
#
# The service conflicts with getty@tty1.service because CoreOS live images
# autologin the "core" user on tty1; without the conflict both would fight over
# the console.
#
# $1 = output path, $2 = optional SSH public key.
write_live_ignition() {
    local ign_file="$1" ssh_pub_key="$2"

    python3 - "$ign_file" "$ssh_pub_key" <<'PYEOF'
import json, sys

ign_file    = sys.argv[1]
ssh_pub_key = sys.argv[2].strip()

# Stages /knuckle from the boot medium onto the live rootfs. The ISO stays
# mounted in the live environment, but the mount point is not fixed across
# images, so the search is by glob over the usual media mount points rather
# than a hardcoded path. Knuckle itself is a single static binary with no
# runtime dependencies, so a plain copy is all that is needed.
stage_unit = """\
[Unit]
Description=Stage the knuckle binary off the installer ISO
Before=knuckle-installer.service
DefaultDependencies=no
After=local-fs.target

[Service]
Type=oneshot
RemainAfterExit=yes
StandardOutput=journal+console
StandardError=journal+console
ExecStart=/bin/sh -c 'p=$(ls /media/*/knuckle /run/media/*/knuckle /mnt/*/knuckle /media/knuckle /run/media/knuckle 2>/dev/null | head -n 1); test -n "$p" && install -m 0755 "$p" /opt/knuckle'
ExecStart=/bin/sh -c 'test -x /opt/knuckle || { echo "knuckle-stage: binary not found on the boot medium"; exit 1; }'

[Install]
WantedBy=multi-user.target"""

service_unit = """\
[Unit]
Description=Knuckle Installer
After=multi-user.target knuckle-stage-binary.service
Requires=knuckle-stage-binary.service
Conflicts=getty@tty1.service
Before=getty@tty1.service
ConditionPathExists=/opt/knuckle

[Service]
Type=idle
ExecStart=/opt/knuckle
StandardInput=tty
StandardOutput=tty
TTYPath=/dev/tty1
TTYReset=yes
TTYVHangup=yes
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target"""

enabled = True

config = {
    "ignition": {"version": "3.3.0"},
    "systemd": {
        "units": [
            {"name": "sshd.service", "enabled": True},
            {
                "name": "knuckle-stage-binary.service",
                "enabled": True,
                "contents": stage_unit
            },
            {
                "name": "knuckle-installer.service",
                "enabled": True,
                "contents": service_unit
            }
        ]
    }
}

if ssh_pub_key:
    config["passwd"] = {
        "users": [{"name": "core", "sshAuthorizedKeys": [ssh_pub_key]}]
    }

with open(ign_file, "w") as f:
    json.dump(config, f, indent=2)

print(f"  Ignition: {ign_file}")
print(f"  size: {len(json.dumps(config)):,} bytes (limit {262144:,})")
PYEOF
}

# build_coreos_iso is the whole pipeline.
# $1 = os label used in the output filename (e.g. "fcos" or "ucore")
# $2 = FCOS stream, $3 = knuckle arch (amd64|arm64), $4 = binary override,
# $5 = optional SSH public key.
build_coreos_iso() {
    local os_label="$1" stream="$2" arch="$3" binary_override="$4" ssh_pub_key="$5"

    local coreos_arch
    case "$arch" in
        amd64)  coreos_arch="x86_64" ;;
        arm64)  coreos_arch="aarch64" ;;
        *)      echo "error: arch must be amd64 or arm64 (got '$arch')" >&2; return 1 ;;
    esac

    mkdir -p "$BUILD_DIR" "$OUTPUT_DIR"

    echo "=== Building knuckle ${os_label} installer ISO (stream: $stream, arch: $arch) ==="

    # ── 1. Binary ───────────────────────────────────────────────────────────
    build_knuckle_binary "$arch" "$binary_override"
    echo "  binary : $(du -h "$KNUCKLE_BINARY" | cut -f1)"

    # ── 2. Live ISO ─────────────────────────────────────────────────────────
    echo "[2/4] Downloading FCOS live ISO (stream: $stream, arch: $coreos_arch)..."
    fetch_live_iso "$stream" "$arch" "$coreos_arch"

    # ── 3. Live ignition ────────────────────────────────────────────────────
    # Small by construction: it carries unit text only. The binary travels as an
    # ISO file in stage 4, not inside this config.
    echo "[3/4] Generating live Ignition config..."
    local ign_file="$BUILD_DIR/live-ignition-${os_label}.ign"
    write_live_ignition "$ign_file" "$ssh_pub_key"

    # Assert the invariant rather than trusting it. Adding anything large to the
    # live ignition later (a file blob, a big embedded script) would otherwise
    # fail much later, inside coreos-installer, with a confusing message.
    local ign_bytes
    ign_bytes="$(wc -c < "$ign_file")"
    if (( ign_bytes > LIVE_IGNITION_MAX_BYTES )); then
        echo "error: live ignition is ${ign_bytes} bytes, over the" \
             "${LIVE_IGNITION_MAX_BYTES}-byte initramfs limit." >&2
        echo "  Put large content on the ISO instead (see add_binary_to_iso)." >&2
        return 1
    fi

    # ── 4. Build the ISO ────────────────────────────────────────────────────
    # Two steps, in this order: coreos-installer embeds the ignition into the
    # stock ISO, then xorriso adds the binary. Injecting first and customising
    # second would rely on coreos-installer preserving a foreign ISO file, which
    # is untested; the reverse order uses only xorriso's own replay path.
    echo "[4/4] Building the installer ISO..."

    local volid
    volid="$(basename "$LIVE_ISO" | sed 's/-\(live\|metal\).*//; s/\.iso$//')"
    local iso_tmp="$BUILD_DIR/knuckle-${os_label}-customized-${stream}-${arch}.iso"
    local iso_out="$OUTPUT_DIR/knuckle-${os_label}-installer-${stream}-${arch}.iso"

    coreos-installer iso customize \
        --live-ignition "$ign_file" \
        --output "$iso_tmp" \
        "$LIVE_ISO"

    add_binary_to_iso "$iso_tmp" "$iso_out" "$KNUCKLE_BINARY" "$volid"
    rm -f "$iso_tmp"

    echo ""
    echo "ISO built: $iso_out ($(du -h "$iso_out" | cut -f1))"
    print_coreos_iso_usage "$iso_out" "$arch"
}

print_coreos_iso_usage() {
    local iso_out="$1" arch="$2"
    echo ""
    if [[ "$arch" == "arm64" ]]; then
        echo "Test with QEMU (UEFI, arm64):"
        echo "  OVMF=/usr/share/AAVMF/AAVMF_CODE.fd"
        echo "  qemu-system-aarch64 -m 4096 -cpu cortex-a57 -M virt \\"
        echo "    -drive if=pflash,format=raw,readonly=on,file=\$OVMF \\"
        echo "    -cdrom $iso_out \\"
        echo "    -drive if=virtio,file=target.qcow2,format=qcow2 \\"
        echo "    -nographic"
    else
        echo "Test with QEMU (UEFI, amd64):"
        echo "  OVMF=/usr/share/OVMF/OVMF_CODE.fd"
        echo "  qemu-system-x86_64 -m 4096 -enable-kvm \\"
        echo "    -drive if=pflash,format=raw,readonly=on,file=\$OVMF \\"
        echo "    -cdrom $iso_out \\"
        echo "    -drive if=virtio,file=target.qcow2,format=qcow2 \\"
        echo "    -nographic"
    fi
    echo ""
    echo "Write to USB:"
    echo "  sudo dd if=$iso_out of=/dev/sdX bs=4M status=progress"
}

# require_coreos_installer exits with an actionable message if any tool needed to
# build the ISO is missing.
require_coreos_installer() {
    local missing=0
    if ! command -v coreos-installer &>/dev/null; then
        echo "error: coreos-installer not found" >&2
        echo "  Install (Fedora): sudo dnf install -y coreos-installer" >&2
        echo "  Or run:  just tools-fcos" >&2
        missing=1
    fi
    if ! command -v xorriso &>/dev/null; then
        echo "error: xorriso not found (needed to inject the knuckle binary)" >&2
        echo "  Install (Fedora): sudo dnf install -y xorriso" >&2
        echo "  Install (Debian): sudo apt-get install -y xorriso" >&2
        missing=1
    fi
    if ! command -v python3 &>/dev/null; then
        echo "error: python3 not found (needed to generate the live ignition)" >&2
        missing=1
    fi
    [[ $missing -eq 0 ]] || exit 1
}
