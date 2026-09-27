#!/usr/bin/env bats
# Tests for scripts/lib/cosa-config.sh: materialising the derived
# coreos-assembler config repo (symlink farm, local config files) and locating
# the live ISO a build produced.
#
# No network, podman or KVM: COSA_UPSTREAM_CONFIG_REPO is pointed at a local git
# repo built in setup(), so the whole materialisation path is exercised offline.
#
# Requires: bats-core (https://github.com/bats-core/bats-core)
#
# Run: bats scripts/tests/cosa-config.bats

LIB="$BATS_TEST_DIRNAME/../lib/cosa-config.sh"
ORIGINAL_PATH="$PATH"
REPO_ROOT="$BATS_TEST_DIRNAME/../.."
WORK=""

# Sourced so the tests can iterate COSA_UPSTREAM_LINKS. Without this the loops
# below silently expand to nothing and pass without asserting anything.
# shellcheck source=scripts/lib/cosa-config.sh
source "$LIB"

# make_fake_upstream builds a git repo that looks enough like fedora-coreos-config
# for the materialiser: every entry in COSA_UPSTREAM_LINKS, plus a lockfile.
#
# The list of required components is the thing under test — build-rootfs opens
# each of them with no fallback, so a missing one has to be caught here rather
# than hours into a buildah build.
make_fake_upstream() {
    local dir="$WORK/upstream"
    mkdir -p "$dir"

    # Files and directories the derived repo symlinks. Each directory gets a
    # placeholder file: git does not track empty directories, so a bare
    # mkdir would leave the component absent from every clone.
    local name
    for name in "${COSA_UPSTREAM_LINKS[@]}"; do
        if [[ "$name" == "live" || "$name" == "manifests" || "$name" == "overlay.d" ]]; then
            mkdir -p "$dir/$name"
            echo "stub" > "$dir/$name/.keep"
        else
            echo "stub $name" > "$dir/$name"
        fi
    done
    # A representative overlay layer, since the Fedora-ID manifest branch refers
    # to "overlay.d/<name>" relatively and the symlink has to resolve through it.
    mkdir -p "$dir/overlay.d/05core"
    echo "stub" > "$dir/overlay.d/05core/.keep"
    echo "packages: []" > "$dir/manifests/fedora-coreos.yaml"

    echo '{"packages":{}}' > "$dir/manifest-lock.x86_64.json"
    echo '{"packages":{}}' > "$dir/manifest-lock.aarch64.json"

    git -C "$dir" init -q
    git -C "$dir" -c user.email=t@t -c user.name=t add -A
    git -C "$dir" -c user.email=t@t -c user.name=t commit -qm init
    git -C "$dir" branch -f stable HEAD >/dev/null 2>&1

    echo "$dir"
}

setup() {
    WORK=$(mktemp -d "${TMPDIR:-/tmp}/cosa-config.XXXXXX")
    FAKE_UPSTREAM="$(make_fake_upstream)"

    # The materialiser reads the repo URL from this variable, so overriding it
    # keeps the test hermetic.
    export COSA_UPSTREAM_CONFIG_REPO="$FAKE_UPSTREAM"
}

teardown() {
    export PATH="$ORIGINAL_PATH"
    [ -n "$WORK" ] && rm -rf "$WORK"
}

# run_lib — source the lib and run a body, so `return 1` becomes an exit status.
run_lib() {
    run bash -c 'source "$1"; shift; "$@"' _ "$LIB" "$@"
}

# ── symlink farm ─────────────────────────────────────────────────────────────

@test "cosa_config_link creates a relative symlink into the upstream clone" {
    mkdir -p "$WORK/config/fedora-coreos-config"
    run_lib cosa_config_link "$WORK/config" Containerfile
    [ "$status" -eq 0 ]
    [ -L "$WORK/config/Containerfile" ]
    [ "$(readlink "$WORK/config/Containerfile")" = "fedora-coreos-config/Containerfile" ]
}

@test "cosa_config_link is idempotent" {
    mkdir -p "$WORK/config/fedora-coreos-config"
    run_lib cosa_config_link "$WORK/config" Containerfile
    run_lib cosa_config_link "$WORK/config" Containerfile
    [ "$status" -eq 0 ]
    [ -L "$WORK/config/Containerfile" ]
}

@test "cosa_config_link replaces a stale symlink" {
    mkdir -p "$WORK/config/fedora-coreos-config"
    ln -s somewhere/else "$WORK/config/Containerfile"
    run_lib cosa_config_link "$WORK/config" Containerfile
    [ "$status" -eq 0 ]
    [ "$(readlink "$WORK/config/Containerfile")" = "fedora-coreos-config/Containerfile" ]
}

@test "cosa_config_link refuses to clobber a real file" {
    # A hand edit in the generated config dir must never be silently destroyed.
    mkdir -p "$WORK/config/fedora-coreos-config"
    echo "hand edited" > "$WORK/config/Containerfile"
    run_lib cosa_config_link "$WORK/config" Containerfile
    [ "$status" -ne 0 ]
    [[ "$output" == *"refusing to overwrite"* ]]
    [ "$(cat "$WORK/config/Containerfile")" = "hand edited" ]
}

@test "cosa_config_link refuses to clobber a real directory" {
    mkdir -p "$WORK/config/fedora-coreos-config" "$WORK/config/live/keepme"
    run_lib cosa_config_link "$WORK/config" live
    [ "$status" -ne 0 ]
    [ -f "$WORK/config/live/keepme" ] || [ -d "$WORK/config/live/keepme" ]
}

# ── materialise ──────────────────────────────────────────────────────────────

@test "cosa_config_materialize returns only the commit sha on stdout" {
    # Progress goes to stderr; anything else on stdout would be captured by the
    # caller as part of the sha and corrupt the build provenance it records.
    out="$(bash -c 'source "$1"; cosa_config_materialize "$2" stable "$3"' \
        _ "$LIB" "$WORK/config" "$REPO_ROOT/cosa" 2>/dev/null)"
    [ -n "$out" ]
    [[ "$out" =~ ^[0-9a-f]{40}$ ]]
}

@test "cosa_config_materialize creates every required symlink" {
    run_lib cosa_config_materialize "$WORK/config" stable "$REPO_ROOT/cosa"
    [ "$status" -eq 0 ]
    local name
    for name in "${COSA_UPSTREAM_LINKS[@]}"; do
        [ -L "$WORK/config/$name" ] || {
            echo "missing symlink for $name" >&2
            return 1
        }
    done
}

@test "cosa_config_materialize symlinks the arch lockfiles" {
    run_lib cosa_config_materialize "$WORK/config" stable "$REPO_ROOT/cosa"
    [ "$status" -eq 0 ]
    [ -L "$WORK/config/manifest-lock.x86_64.json" ]
    [ -L "$WORK/config/manifest-lock.aarch64.json" ]
}

@test "cosa_config_materialize installs knuckle's manifest.yaml and image.yaml" {
    # These are copies, not symlinks: the config dir is bind-mounted read-only
    # into the COSA container and must be self-contained.
    run_lib cosa_config_materialize "$WORK/config" stable "$REPO_ROOT/cosa"
    [ "$status" -eq 0 ]
    [ -f "$WORK/config/manifest.yaml" ]
    [ ! -L "$WORK/config/manifest.yaml" ]
    [ -f "$WORK/config/image.yaml" ]
    grep -q "NetworkManager-wifi" "$WORK/config/manifest.yaml"
}

@test "cosa_config_materialize keeps upstream manifest.yaml as the base" {
    run_lib cosa_config_materialize "$WORK/config" stable "$REPO_ROOT/cosa"
    [ "$status" -eq 0 ]
    grep -q "fedora-coreos-config/manifest.yaml" "$WORK/config/manifest.yaml"
}

@test "cosa_config_materialize points container-imgref away from the official repo" {
    # Upstream aims this at quay.io/fedora/fedora-coreos. This image is FCOS plus
    # packages the FCOS project excludes, so a push must not look official. The
    # check is on the value line only: the file's own comment quotes the upstream
    # value in order to explain what it is replacing.
    run_lib cosa_config_materialize "$WORK/config" stable "$REPO_ROOT/cosa"
    [ "$status" -eq 0 ]
    grep -qE '^[[:space:]]*container-imgref:' "$WORK/config/image.yaml"
    ! grep -qE '^[[:space:]]*container-imgref:.*quay\.io/fedora/fedora-coreos' "$WORK/config/image.yaml"
}

@test "cosa_config_materialize fails when upstream lacks a required component" {
    local dir="$WORK/broken"
    git clone -q "$FAKE_UPSTREAM" "$dir"
    rm -rf "$dir/overlay.d"
    git -C "$dir" -c user.email=t@t -c user.name=t add -A
    git -C "$dir" -c user.email=t@t -c user.name=t commit -qm drop
    git -C "$dir" branch -f broken HEAD

    export COSA_UPSTREAM_CONFIG_REPO="$dir"
    run bash -c 'source "$1"; cosa_config_materialize "$2" broken "$3"' \
        _ "$LIB" "$WORK/config2" "$REPO_ROOT/cosa"
    [ "$status" -ne 0 ]
    [[ "$output" == *"has no 'overlay.d'"* ]]
}

@test "cosa_config_materialize fails on an unknown upstream ref" {
    run bash -c 'source "$1"; cosa_config_materialize "$2" no-such-ref "$3"' \
        _ "$LIB" "$WORK/config3" "$REPO_ROOT/cosa"
    [ "$status" -ne 0 ]
    [[ "$output" == *"failed to clone"* ]]
}

# ── ISO discovery ────────────────────────────────────────────────────────────

@test "cosa_find_live_iso follows the builds/latest symlink" {
    mkdir -p "$WORK/work/builds/44.1.2.3/x86_64"
    ln -s 44.1.2.3 "$WORK/work/builds/latest"
    touch "$WORK/work/builds/44.1.2.3/x86_64/fedora-coreos-44.1.2.3-live-iso.x86_64.iso"

    run_lib cosa_find_live_iso "$WORK/work"
    [ "$status" -eq 0 ]
    [[ "$output" == *"fedora-coreos-44.1.2.3-live-iso.x86_64.iso" ]]
}

@test "cosa_find_live_iso handles the aarch64 artifact name" {
    mkdir -p "$WORK/work/builds/44.1.2.3/aarch64"
    ln -s 44.1.2.3 "$WORK/work/builds/latest"
    touch "$WORK/work/builds/44.1.2.3/aarch64/fedora-coreos-44.1.2.3-live-iso.aarch64.iso"

    run_lib cosa_find_live_iso "$WORK/work"
    [ "$status" -eq 0 ]
    [[ "$output" == *".aarch64.iso" ]]
}

@test "cosa_find_live_iso falls back to scanning when builds/latest is absent" {
    # A workdir imported by hand has no symlink, and picking no ISO would be
    # worse than picking the newest one.
    mkdir -p "$WORK/work/builds/44.1.2.3/x86_64"
    touch "$WORK/work/builds/44.1.2.3/x86_64/fedora-coreos-44.1.2.3-live-iso.x86_64.iso"

    run_lib cosa_find_live_iso "$WORK/work"
    [ "$status" -eq 0 ]
    [[ "$output" == *"-live-iso.x86_64.iso" ]]
}

@test "cosa_find_live_iso ignores non-live artifacts" {
    # A metal raw image is not a live ISO; matching it would produce a medium
    # that does not boot.
    mkdir -p "$WORK/work/builds/44.1.2.3/x86_64"
    ln -s 44.1.2.3 "$WORK/work/builds/latest"
    touch "$WORK/work/builds/44.1.2.3/x86_64/fedora-coreos-44.1.2.3-metal.x86_64.raw"

    run_lib cosa_find_live_iso "$WORK/work"
    [ "$status" -ne 0 ]
    [[ "$output" == *"no live ISO"* ]]
}

@test "cosa_find_live_iso errors when there are no builds at all" {
    mkdir -p "$WORK/work/builds"
    run_lib cosa_find_live_iso "$WORK/work"
    [ "$status" -ne 0 ]
    [[ "$output" == *"no build found"* ]]
}
