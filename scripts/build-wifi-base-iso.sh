#!/usr/bin/env bash
# Build a WiFi-capable Fedora CoreOS live ISO to use as the base medium for a
# knuckle installer ISO.
#
# # What this produces, and why it exists
#
# `just build-fcos-iso` / `just build-ucore-iso` start from the stock FCOS live
# ISO. That image has nmtui but not `NetworkManager-wifi`, and no wireless
# firmware at all: Fedora split the firmware blobs out of linux-firmware into
# per-vendor subpackages, made them Recommends, and FCOS composes with
# "recommends: false". So nmtui launches on the stock medium, finds no radio,
# and the installer's WiFi step can only fall back to hand-entered details.
#
# This script builds a replacement live medium that carries those packages, so
# nmtui can actually scan. Feed the result to the installer build with
# --base-iso (or KNUCKLE_BASE_ISO).
#
# # Scope: this fixes the installer, not the installed system
#
# The live ISO's package set is not inherited by the target — coreos-installer
# writes a stock upstream FCOS deployment. WiFi on the installed machine is
# handled separately, by the first-boot layering unit that knuckle's Ignition
# config emits (internal/ignition/wifi.go).
#
# # Cost
#
# This is a real Fedora CoreOS build, not a repack: it needs podman, /dev/kvm,
# and roughly 10.5 GiB RAM, 6 CPUs and 200 GB of free disk (about 50 GB of that
# is the supermin cache), and runs for a couple of hours. It cannot run in CI.
#
# Requirements: podman, git, /dev/kvm
#
# Usage: ./scripts/build-wifi-base-iso.sh [--stream testing-devel|stable] [--arch x86_64|aarch64] [--ref <git-ref>] [--out <path>]
set -euo pipefail

STREAM="testing-devel"
ARCH="x86_64"
UPSTREAM_REF=""
OUTPUT_OVERRIDE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --stream=*) STREAM="${1#--stream=}"; shift ;;
        --stream)   STREAM="$2"; shift 2 ;;
        --arch=*)   ARCH="${1#--arch=}"; shift ;;
        --arch)     ARCH="$2"; shift 2 ;;
        --ref=*)    UPSTREAM_REF="${1#--ref=}"; shift ;;
        --ref)      UPSTREAM_REF="$2"; shift 2 ;;
        --out=*)    OUTPUT_OVERRIDE="${1#--out=}"; shift ;;
        --out)      OUTPUT_OVERRIDE="$2"; shift 2 ;;
        testing-devel|stable) STREAM="$1"; shift ;;
        *) echo "Unknown argument: $1" >&2; exit 1 ;;
    esac
done

case "$STREAM" in
    testing-devel|stable) ;;
    *) echo "error: --stream must be testing-devel or stable (got '$STREAM')" >&2; exit 1 ;;
esac
case "$ARCH" in
    x86_64|aarch64) ;;
    *) echo "error: --arch must be x86_64 or aarch64 (got '$ARCH')" >&2; exit 1 ;;
esac

# coreos-assembler builds for the *host* basearch — there is no cross-build. So a
# mismatched --arch would not fail, it would quietly produce an x86_64 ISO under
# an aarch64 filename. Refuse instead, since a mislabelled base medium is worse
# than no base medium: it gets written to a USB stick and booted.
HOST_ARCH="$(uname -m)"
if [[ "$ARCH" != "$HOST_ARCH" ]]; then
    echo "error: --arch $ARCH does not match this host ($HOST_ARCH)." >&2
    echo "  coreos-assembler has no cross-build; it always builds for the host." >&2
    echo "  To build for $ARCH, run this on a $ARCH machine." >&2
    exit 1
fi
# Ref defaults to the stream so a rebuild of the same stream tracks upstream.
[[ -n "$UPSTREAM_REF" ]] || UPSTREAM_REF="$STREAM"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BUILD_DIR="$ROOT_DIR/.cosa-build"
WORK_DIR="$BUILD_DIR/work"
CONFIG_DIR="$BUILD_DIR/config"
COSA_SRC_DIR="$ROOT_DIR/cosa"
OUTPUT_DIR="$ROOT_DIR/output"

# shellcheck source=scripts/lib/cosa-config.sh
source "$SCRIPT_DIR/lib/cosa-config.sh"

# ── Preflight ────────────────────────────────────────────────────────────────
# Everything is checked before the (slow) clone, because a build that dies two
# hours in on a missing device is the expensive way to learn about a missing podman.
missing=0
if ! command -v podman &>/dev/null; then
    echo "error: podman not found" >&2
    echo "  Install it, or run the COSA container under docker instead." >&2
    missing=1
fi
if ! command -v git &>/dev/null; then
    echo "error: git not found — needed to fetch the upstream config repo" >&2
    missing=1
fi
if [[ ! -c /dev/kvm ]]; then
    echo "error: /dev/kvm not present — coreos-assembler builds disk images in a VM" >&2
    echo "  This needs KVM. On a KVM-less host the build falls back to software" >&2
    echo "  emulation and takes many hours; upstream's escape hatch is COSA_NO_KVM=1." >&2
    missing=1
elif [[ ! -w /dev/kvm ]]; then
    echo "error: /dev/kvm is not writable by $USER" >&2
    echo "  Fix with: sudo setfacl -m u:$USER:rw /dev/kvm" >&2
    missing=1
fi
[[ $missing -eq 0 ]] || exit 1

# ── 1. Materialise the derived config repo ───────────────────────────────────
echo "=== Building WiFi-capable FCOS live ISO (stream: $STREAM, arch: $ARCH) ==="
echo ""
echo "[1/4] Preparing derived coreos-assembler config..."
mkdir -p "$WORK_DIR" "$OUTPUT_DIR"
COMMIT="$(cosa_config_materialize "$CONFIG_DIR" "$UPSTREAM_REF" "$COSA_SRC_DIR")" || exit 1
echo "  upstream config: $COSA_UPSTREAM_CONFIG_REPO @ ${COMMIT:0:12} ($UPSTREAM_REF)"

# ── 2. Build the OS ──────────────────────────────────────────────────────────
# COREOS_ASSEMBLER_CONFIG_GIT points COSA at our derived repo instead of cloning
# upstream, so `cosa init` has nothing to fetch.
echo ""
echo "[2/4] Building the OS image (this is the long part)..."
export COREOS_ASSEMBLER_CONFIG_GIT="$CONFIG_DIR"
export COSA_WORKDIR="$WORK_DIR"

# `cosa init --force /dev/null` is the documented way to initialise from a local
# config git: /dev/null is a placeholder repo URL that the forced init skips.
"$SCRIPT_DIR/cosa.sh" init --force /dev/null
"$SCRIPT_DIR/cosa.sh" build

# ── 3. Build the live ISO ────────────────────────────────────────────────────
# metal and metal4k are named first because the live target consumes them as
# inputs; asking for them together lets the ISO reuse them instead of rebuilding.
echo ""
echo "[3/4] Building the live ISO..."
"$SCRIPT_DIR/cosa.sh" osbuild metal metal4k live

# ── 4. Publish ───────────────────────────────────────────────────────────────
echo ""
echo "[4/4] Publishing..."
ISO_SRC="$(cosa_find_live_iso "$WORK_DIR")" || exit 1

if [[ -n "$OUTPUT_OVERRIDE" ]]; then
    ISO_OUT="$OUTPUT_OVERRIDE"
else
    ISO_OUT="$OUTPUT_DIR/knuckle-wifi-base-${STREAM}-${ARCH}.iso"
fi
mkdir -p "$(dirname "$ISO_OUT")"
cp -f "$ISO_SRC" "$ISO_OUT"
chmod u+w "$ISO_OUT"

echo ""
echo "Base ISO: $ISO_OUT ($(du -h "$ISO_OUT" | cut -f1))"
echo "  built from fedora-coreos-config @ ${COMMIT:0:12}"
echo ""
echo "Build a knuckle installer ISO on top of it:"
echo "  KNUCKLE_BASE_ISO=$ISO_OUT just build-fcos-iso"
echo "  KNUCKLE_BASE_ISO=$ISO_OUT just build-ucore-iso"
echo ""
echo "Or directly:"
echo "  ./scripts/build-fcos-iso.sh  --base-iso $ISO_OUT"
echo "  ./scripts/build-ucore-iso.sh --base-iso $ISO_OUT"
