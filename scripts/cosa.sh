#!/usr/bin/env bash
# cosa() — run the coreos-assembler container, verbatim from
# https://coreos.github.io/coreos-assembler/building-fcos/ ("Define a bash
# alias to run cosa").
#
# This is a standalone script rather than a shell alias so it works from
# non-interactive contexts (make, CI, just recipes). Must be run as non-root.
#
# Usage:  scripts/cosa.sh <cosa-subcommand> [args...]
# e.g.    scripts/cosa.sh init https://github.com/coreos/fedora-coreos-config
#         scripts/cosa.sh build
#         scripts/cosa.sh osbuild metal live
set -euo pipefail

# The build directory is the directory cosa operates on, mounted at /srv/.
# coreos-assembler treats it much like git treats a repo, so default it to the
# current directory and let the caller override.
COSA_WORKDIR="${COSA_WORKDIR:-$PWD}"

# The pinned container. Kept in a variable so the staleness check and the run
# agree, matching upstream's function.
COREOS_ASSEMBLER_CONTAINER_LATEST="quay.io/coreos-assembler/coreos-assembler:latest"

# Upstream hardcodes `podman run -ti`, which assumes an interactive terminal.
# This script also runs from `just` recipes and CI, where there is no TTY and
# podman fails outright with "the input device is not a TTY". So the interactive
# flags are added only when there is actually a terminal to attach to; -i is
# harmless without -t, but keeping both conditional keeps the non-interactive
# invocation honest.
COSA_TTY_FLAGS=()
if [[ -t 0 && -t 1 ]]; then
    COSA_TTY_FLAGS=(-ti)
fi

# The staleness warning is a nicety, so it must not be what breaks the run on a
# host without podman — the caller gets the real "command not found" from the
# exec below instead of a confusing one from here.
if command -v podman &>/dev/null; then
    # shellcheck disable=SC2091  # upstream's function uses `$(podman image exists …)`
    # as a boolean test; the substitution's *output* is deliberately discarded and
    # only its exit status is used. Kept verbatim from upstream.
    if [[ -z ${COREOS_ASSEMBLER_CONTAINER:-} ]] && $(podman image exists "${COREOS_ASSEMBLER_CONTAINER_LATEST}"); then
        cosa_build_date_str="$(podman inspect -f "{{.Created}}" "${COREOS_ASSEMBLER_CONTAINER_LATEST}" | awk '{print $1}')"
        cosa_build_date="$(date -d "${cosa_build_date_str}" +%s)"
        if [[ $(date +%s) -ge $((cosa_build_date + 60 * 60 * 24 * 7)) ]]; then
            echo -e "\e[0;33m----" >&2
            echo "The COSA container image is more than a week old and likely outdated." >&2
            echo "You should pull the latest version with:" >&2
            echo "podman pull ${COREOS_ASSEMBLER_CONTAINER_LATEST}" >&2
            echo -e "----\e[0m" >&2
        fi
    fi
fi

cd "${COSA_WORKDIR}"

# /dev/kvm is only bound when it exists. COSA refuses to build without it, but
# the refusal comes with a clear message from cosa itself, which is better than
# podman failing to start the container on a host that was never going to
# succeed anyway.
COSA_KVM_DEVICE=()
[[ -e /dev/kvm ]] && COSA_KVM_DEVICE=(--device=/dev/kvm)

exec podman run --rm "${COSA_TTY_FLAGS[@]}" --security-opt=label=disable --privileged \
    --userns=keep-id:uid=1000,gid=1000 \
    -v="${PWD}:/srv/" "${COSA_KVM_DEVICE[@]}" --device=/dev/fuse \
    --tmpfs=/tmp -v=/var/tmp:/var/tmp --name=cosa \
    ${REGISTRY_AUTH_FILE:+-v=$REGISTRY_AUTH_FILE:$REGISTRY_AUTH_FILE:ro -e REGISTRY_AUTH_FILE} \
    ${COREOS_ASSEMBLER_CONFIG_GIT:+-v=$COREOS_ASSEMBLER_CONFIG_GIT:/srv/src/config/:ro} \
    ${COREOS_ASSEMBLER_GIT:+-v=$COREOS_ASSEMBLER_GIT/src/:/usr/lib/coreos-assembler/:ro} \
    ${COREOS_ASSEMBLER_ADD_CERTS:+-v=/etc/pki/ca-trust:/etc/pki/ca-trust:ro} \
    ${COREOS_ASSEMBLER_CONTAINER_RUNTIME_ARGS:-} \
    ${COREOS_ASSEMBLER_CONTAINER:-$COREOS_ASSEMBLER_CONTAINER_LATEST} "$@"
