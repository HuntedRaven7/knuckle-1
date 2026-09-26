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

cd "${COSA_WORKDIR}"

exec podman run --rm -ti --security-opt=label=disable --privileged \
    --userns=keep-id:uid=1000,gid=1000 \
    -v="${PWD}:/srv/" --device=/dev/kvm --device=/dev/fuse \
    --tmpfs=/tmp -v=/var/tmp:/var/tmp --name=cosa \
    ${REGISTRY_AUTH_FILE:+-v=$REGISTRY_AUTH_FILE:$REGISTRY_AUTH_FILE:ro -e REGISTRY_AUTH_FILE} \
    ${COREOS_ASSEMBLER_CONFIG_GIT:+-v=$COREOS_ASSEMBLER_CONFIG_GIT:/srv/src/config/:ro} \
    ${COREOS_ASSEMBLER_GIT:+-v=$COREOS_ASSEMBLER_GIT/src/:/usr/lib/coreos-assembler/:ro} \
    ${COREOS_ASSEMBLER_ADD_CERTS:+-v=/etc/pki/ca-trust:/etc/pki/ca-trust:ro} \
    ${COREOS_ASSEMBLER_CONTAINER_RUNTIME_ARGS:-} \
    ${COREOS_ASSEMBLER_CONTAINER:-$COREOS_ASSEMBLER_CONTAINER_LATEST} "$@"
