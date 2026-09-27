#!/usr/bin/env bash
# Materialise a derived coreos-assembler config repo that builds a WiFi-capable
# Fedora CoreOS live ISO. Sourced by scripts/build-wifi-base-iso.sh.
#
# # Why a derived config repo rather than flags
#
# There is no supported way to name extra packages on a COSA command line. The
# package set lives in the config repo's manifest treefile, so adding packages
# means supplying a config repo. coreos-assembler's own guidance
# (fedora-coreos-config/README.md, "Layout") is to keep fedora-coreos-config as
# a git submodule and symlink its components in, rather than fork it — which is
# what this file does, driven from cosa/manifest.yaml and cosa/image.yaml in
# this repository.
#
# # What the symlink farm has to contain
#
# Everything build-rootfs reads unguarded, derived from the upstream layout. The
# awkward one is overlay.d: on the Fedora-ID branch of
# manifests/shared-el.yaml the ostree-layers entries are *relative*
# ("overlay.d/05core"), not submodule-qualified, so the derived repo has to
# present those directories itself. A single directory symlink satisfies that.
#
# shellcheck shell=bash

# The upstream config repo. Pinned to a branch rather than a commit because the
# lockfiles have to track the branch to stay coherent with it; the resolved
# commit is recorded by the caller so a build is traceable after the fact.
#
# Overridable so tests can point this at a local fixture — the tests would
# otherwise clone the real repo, which is neither hermetic nor fast. Set
# COSA_UPSTREAM_CONFIG_REPO to override; do not clobber a caller's choice.
COSA_UPSTREAM_CONFIG_REPO="${COSA_UPSTREAM_CONFIG_REPO:-https://github.com/coreos/fedora-coreos-config.git}"

# Components of the upstream repo that the derived repo symlinks. Each is
# required: build-rootfs opens all of them without a fallback, so a missing one
# fails deep inside a buildah container with a FileNotFoundError rather than
# something actionable.
#
# overlay.d is a whole-directory symlink rather than one per layer, because the
# Fedora-ID manifest branch already refers to "overlay.d/<name>" relatively.
# manifests is needed because upstream's root manifest.yaml includes
# "manifests/fedora-coreos.yaml" relative to its own directory.
COSA_UPSTREAM_LINKS=(
    "build-args.conf"
    "Containerfile"
    "build-rootfs"
    "platforms.yaml"
    "versionary"
    "image-base.yaml"
    "fedora-coreos-pool.repo"
    "live"
    "manifests"
    "overlay.d"
)

# Lockfiles are globbed rather than listed: the arch-specific ones are named
# per basearch, and a stream may ship more than one.
COSA_UPSTREAM_LOCK_GLOBS=(
    "manifest-lock.*.json"
    "manifest-lock.overrides.*"
)

# cosa_config_clone <config_dir> <upstream_ref>
#
# Fetches fedora-coreos-config at <upstream_ref> into <config_dir>/fedora-coreos-config,
# echoing the resolved commit on stdout. A shallow single-branch clone: the repo
# is large and the build never needs history. Exits non-zero if git is missing or
# the fetch fails, so a caller cannot mistake an empty config dir for a good one.
#
# Every function here sends progress to stderr and only its return value to
# stdout, because callers capture stdout into a variable. Progress on stdout
# would be captured too and end up embedded in the commit sha.
cosa_config_clone() {
    local config_dir="$1" upstream_ref="$2"
    local clone_dir="$config_dir/fedora-coreos-config"

    if ! command -v git &>/dev/null; then
        echo "error: git not found — needed to fetch the upstream config repo" >&2
        return 1
    fi

    if [[ -d "$clone_dir/.git" ]]; then
        echo "  upstream config repo already present — refreshing to $upstream_ref" >&2
        git -C "$clone_dir" fetch --depth 1 origin "$upstream_ref" >/dev/null 2>&1 || {
            echo "error: failed to fetch $upstream_ref from $COSA_UPSTREAM_CONFIG_REPO" >&2
            return 1
        }
        git -C "$clone_dir" checkout -q FETCH_HEAD || {
            echo "error: failed to check out $upstream_ref" >&2
            return 1
        }
    else
        echo "  cloning $COSA_UPSTREAM_CONFIG_REPO ($upstream_ref)..." >&2
        git clone --depth 1 --branch "$upstream_ref" \
            "$COSA_UPSTREAM_CONFIG_REPO" "$clone_dir" >/dev/null 2>&1 || {
            echo "error: failed to clone $COSA_UPSTREAM_CONFIG_REPO at $upstream_ref" >&2
            echo "  is the branch name right? Upstream's default branch is testing-devel;" >&2
            echo "  the release branches are stable, testing and next." >&2
            return 1
        }
    fi

    git -C "$clone_dir" rev-parse HEAD
}

# cosa_config_link <config_dir> <path>
#
# Symlinks <config_dir>/<path> at the upstream copy. Idempotent: an existing
# link is replaced, a real file or directory is refused rather than clobbered, so
# a hand edit in the generated config dir is never silently destroyed.
cosa_config_link() {
    local config_dir="$1" name="$2"
    local link="$config_dir/$name"
    local target="fedora-coreos-config/$name"

    if [[ -e "$link" || -L "$link" ]]; then
        if [[ -L "$link" && "$(readlink "$link")" == "$target" ]]; then
            return 0
        fi
        if [[ -L "$link" ]]; then
            rm -f "$link"
        else
            echo "error: $link exists and is not a symlink to $target" >&2
            echo "  refusing to overwrite; remove it and re-run" >&2
            return 1
        fi
    fi
    ln -s "$target" "$link"
}

# cosa_config_install_local <config_dir> <source_file> <dest_name>
#
# Copies one of knuckle's own config files (cosa/manifest.yaml, cosa/image.yaml)
# into the derived repo. Copied rather than symlinked so the config dir mounted
# into the COSA container is self-contained.
cosa_config_install_local() {
    local config_dir="$1" source_file="$2" dest_name="$3"

    if [[ ! -f "$source_file" ]]; then
        echo "error: missing knuckle config file: $source_file" >&2
        return 1
    fi
    cp -f "$source_file" "$config_dir/$dest_name"
}

# cosa_config_materialize <config_dir> <upstream_ref> <cosa_src_dir>
#
# Builds the whole derived config repo at <config_dir>. Echoes the resolved
# upstream commit on stdout — and only that — so the caller can record which
# upstream the ISO was actually built from.
cosa_config_materialize() {
    local config_dir="$1" upstream_ref="$2" cosa_src_dir="$3"

    mkdir -p "$config_dir"

    local commit
    commit="$(cosa_config_clone "$config_dir" "$upstream_ref")" || return 1

    local name
    for name in "${COSA_UPSTREAM_LINKS[@]}"; do
        if [[ ! -e "$config_dir/fedora-coreos-config/$name" ]]; then
            echo "error: upstream $upstream_ref has no '$name'." >&2
            echo "  The derived-config layout tracks upstream, so a stream that" >&2
            echo "  renamed a component needs this script updated too." >&2
            return 1
        fi
        cosa_config_link "$config_dir" "$name" || return 1
    done

    # The lockfiles are optional in principle — an unlocked build resolves
    # versions from the repos — but upstream always ships them, and inheriting
    # them keeps every package we did *not* add pinned to the tested versions.
    local glob
    for glob in "${COSA_UPSTREAM_LOCK_GLOBS[@]}"; do
        local found=0 path
        for path in "$config_dir"/fedora-coreos-config/$glob; do
            [[ -e "$path" ]] || continue
            found=1
            cosa_config_link "$config_dir" "$(basename "$path")" || return 1
        done
        if [[ "$found" -eq 0 ]]; then
            echo "note: upstream has no $glob — that build will be unpinned" >&2
        fi
    done

    cosa_config_install_local "$config_dir" "$cosa_src_dir/manifest.yaml" manifest.yaml || return 1
    cosa_config_install_local "$config_dir" "$cosa_src_dir/image.yaml" image.yaml || return 1

    echo "$commit"
}

# cosa_find_live_iso <workdir>
#
# Prints the path to the live ISO produced by the most recent build.
#
# cosa records the build it just made in builds/latest (a symlink to a
# build-id directory), and names the artifact
# fedora-coreos-<build>-live-iso.<arch>.iso. Matching on the name rather than a
# hardcoded build id is what lets this survive upstream renaming the release.
#
# Falls back to scanning builds/ when builds/latest is absent, so a workdir that
# was imported by hand is still usable. Ties are broken by mtime: several builds
# can coexist, and silently picking an older one would produce an ISO that
# matches no input the caller thinks it used.
cosa_find_live_iso() {
    local workdir="$1"
    local latest="$workdir/builds/latest"

    local dir=""
    if [[ -L "$latest" ]]; then
        dir="$latest"
    fi
    if [[ -z "$dir" ]]; then
        dir="$(find "$workdir/builds" -mindepth 1 -maxdepth 1 -type d \
            -not -name latest -printf '%T@ %p\n' 2>/dev/null | sort -rn | head -1 | cut -d' ' -f2-)"
    fi
    if [[ -z "$dir" ]]; then
        echo "error: no build found under $workdir/builds" >&2
        return 1
    fi

    local iso
    iso="$(find -L "$dir" -name '*-live-iso.*.iso' -printf '%T@ %p\n' 2>/dev/null \
        | sort -rn | head -1 | cut -d' ' -f2-)"
    if [[ -z "$iso" ]]; then
        echo "error: no live ISO in $dir" >&2
        echo "  a build only produces one if 'cosa osbuild live' ran" >&2
        return 1
    fi
    echo "$iso"
}
