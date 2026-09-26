#!/usr/bin/env bash
# Build a bootable installer ISO that installs uCore (https://github.com/ublue-os/ucore).
#
# # Why the live medium is Fedora CoreOS and not uCore
#
# uCore publishes no ISO. Its GitHub releases carry changelogs only — the
# "assets" array is empty on every release — and the images themselves are OCI
# artifacts on ghcr.io. The project states that it "does not provide its own
# custom or GUI installer". uCore is a bootc image that *extends* Fedora
# CoreOS, so the live installer medium is the stock FCOS live ISO that uCore
# already builds on.
#
# That is not a limitation of this script; it is how uCore is meant to be
# installed. Selecting uCore in the wizard makes knuckle:
#   1. run coreos-installer to lay down a Fedora CoreOS deployment on the target
#      disk (using the base stream derived from the chosen uCore stream), and
#   2. write an Ignition oneshot unit that rebases that deployment onto the
#      selected uCore OCI image on the installed system's first boot.
#
# The target therefore becomes uCore; the live environment the user interacts
# with is Fedora CoreOS.
#
# Requirements: coreos-installer, python3
#   Install (Fedora): sudo dnf install -y coreos-installer   (or: just tools-fcos)
#   Non-Fedora: run the digest-pinned quay.io/coreos/coreos-installer:release container
#
# Usage: ./scripts/build-ucore-iso.sh [--stream stable|testing|next] [--arch amd64|arm64] [--binary /path/to/knuckle] [--ssh-key "ssh-ed25519 ..."]
#
# Note: --stream selects the *Fedora CoreOS base* stream that coreos-installer
# lays down. The uCore release stream (stable/testing/lts) and image variant are
# chosen interactively in the wizard, or via headless config.
set -euo pipefail

STREAM="stable"
ARCH="amd64"
BINARY_OVERRIDE=""
SSH_PUB_KEY=""
BASE_ISO=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --stream=*) STREAM="${1#--stream=}"; shift ;;
        --stream)   STREAM="$2"; shift 2 ;;
        --arch=*)   ARCH="${1#--arch=}"; shift ;;
        --arch)     ARCH="$2"; shift 2 ;;
        --binary=*) BINARY_OVERRIDE="${1#--binary=}"; shift ;;
        --binary)   BINARY_OVERRIDE="$2"; shift 2 ;;
        --ssh-key=*) SSH_PUB_KEY="${1#--ssh-key=}"; shift ;;
        --ssh-key)  SSH_PUB_KEY="$2"; shift 2 ;;
        --base-iso=*) BASE_ISO="${1#--base-iso=}"; shift ;;
        --base-iso)  BASE_ISO="$2"; shift 2 ;;
        stable|testing|next) STREAM="$1"; shift ;;
        *) echo "Unknown argument: $1" >&2; exit 1 ;;
    esac
done

case "$STREAM" in
    stable|testing|next) ;;
    *) echo "error: --stream must be stable, testing, or next (got '$STREAM')" >&2; exit 1 ;;
esac
case "$ARCH" in
    amd64|arm64) ;;
    *) echo "error: --arch must be amd64 or arm64 (got '$ARCH')" >&2; exit 1 ;;
esac

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BUILD_DIR="$ROOT_DIR/.ucore-iso-build"
OUTPUT_DIR="$ROOT_DIR/output"

# Source before calling anything from it — the dependency check below is defined
# in the lib, so sourcing has to come first or the script dies on
# "command not found" instead of the real error.
# shellcheck source=scripts/lib/coreos-iso.sh
source "$SCRIPT_DIR/lib/coreos-iso.sh"

require_coreos_installer

echo "note: the live medium is the Fedora CoreOS live ISO; uCore is installed"
echo "      onto the target disk and applied by a first-boot rebase."
echo ""

# A custom base ISO (e.g. one built with coreos-assembler that carries wireless
# firmware and nmtui) is used instead of downloading the stock FCOS ISO.
if [[ -n "$BASE_ISO" ]]; then
    export KNUCKLE_BASE_ISO="$BASE_ISO"
fi

build_coreos_iso "ucore" "$STREAM" "$ARCH" "$BINARY_OVERRIDE" "$SSH_PUB_KEY"
