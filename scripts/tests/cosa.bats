#!/usr/bin/env bats
# Tests for scripts/cosa.sh: the podman invocation contract that the build script
# and `just` recipes depend on.
#
# Requires: bats-core (https://github.com/bats-core/bats-core)
#
# Run: bats scripts/tests/cosa.bats

bats_require_minimum_version 1.5.0

SCRIPT="$BATS_TEST_DIRNAME/../cosa.sh"
ORIGINAL_PATH="$PATH"
WORK=""

setup() {
  WORK=$(mktemp -d "${TMPDIR:-/tmp}/cosa.XXXXXX")
  mkdir -p "$WORK/bin" "$WORK/workdir"

  # podman stub: record the argv it was called with. Both the flag set and the
  # order matter, so a whole-line log is easier to assert on than a variable.
  cat >"$WORK/bin/podman" <<'STUB'
#!/usr/bin/env bash
echo "podman $*" >> "$PODMAN_LOG"
if [[ "$1" == "image" && "$2" == "exists" ]]; then exit 1; fi
exit 0
STUB
  chmod +x "$WORK/bin/podman"

  export PATH="$WORK/bin:$ORIGINAL_PATH"
  export PODMAN_LOG="$WORK/podman.log"
  : >"$PODMAN_LOG"
  export COSA_WORKDIR="$WORK/workdir"
}

teardown() {
  export PATH="$ORIGINAL_PATH"
  [ -n "$WORK" ] && rm -rf "$WORK"
}

# args_of <subcommand> — the recorded argv for the run() invocation.
args_of() {
  grep "^podman run " "$PODMAN_LOG" | tail -1
}

@test "invokes podman with the cosa subcommand" {
  run bash "$SCRIPT" build
  [ "$status" -eq 0 ]
  [[ "$(args_of)" == *"build"* ]]
}

@test "does not request a TTY when there is no terminal" {
  # Upstream's function hardcodes `podman run --rm -ti`, which fails outright in
  # a `just` recipe or CI job with "the input device is not a TTY". Bats runs
  # without a tty, which is exactly the case that has to work.
  run bash "$SCRIPT" build
  [ "$status" -eq 0 ]
  [[ "$(args_of)" != *"-ti"* ]]
  [[ "$(args_of)" != *" -t "* ]]
}

@test "still removes the container and keeps it privileged" {
  # The upstream flags that matter for the build itself: --rm, --privileged and
  # the label opt-out are what let an unprivileged user drive the assembler.
  run bash "$SCRIPT" osbuild live
  [ "$status" -eq 0 ]
  local args
  args="$(args_of)"
  [[ "$args" == *"--rm"* ]]
  [[ "$args" == *"--privileged"* ]]
  [[ "$args" == *"--security-opt=label=disable"* ]]
}

@test "binds the workdir at /srv" {
  run bash "$SCRIPT" build
  [ "$status" -eq 0 ]
  [[ "$(args_of)" == *"-v=$WORK/workdir:/srv/"* ]]
}

@test "mounts a local config git read-only when one is set" {
  # COREOS_ASSEMBLER_CONFIG_GIT is how the derived config repo replaces upstream.
  # Read-only matters: cosa's own docs mount it that way, and the build copies
  # src/config into a scratch dir before buildah ever sees it.
  export COREOS_ASSEMBLER_CONFIG_GIT="$WORK/config"
  run bash "$SCRIPT" init --force /dev/null
  [ "$status" -eq 0 ]
  [[ "$(args_of)" == *"-v=$WORK/config:/srv/src/config/:ro"* ]]
}

@test "omits the config git mount when it is unset" {
  run bash "$SCRIPT" build
  [ "$status" -eq 0 ]
  [[ "$(args_of)" != *"/srv/src/config/"* ]]
}

@test "passes the subcommand and its arguments through verbatim" {
  run bash "$SCRIPT" osbuild metal metal4k live
  [ "$status" -eq 0 ]
  local args
  args="$(args_of)"
  [[ "$args" == *"osbuild metal metal4k live"* ]]
}

@test "propagates a non-zero exit from podman" {
  # A build that fails must fail the recipe, not be swallowed by the wrapper.
  cat >"$WORK/bin/podman" <<'STUB'
#!/usr/bin/env bash
echo "podman $*" >> "$PODMAN_LOG"
if [[ "$1" == "image" && "$2" == "exists" ]]; then exit 1; fi
exit 42
STUB
  chmod +x "$WORK/bin/podman"

  run bash "$SCRIPT" build
  [ "$status" -eq 42 ]
}

@test "does not fail when podman is missing from PATH" {
  # The staleness probe must not be what breaks on a host without podman; the
  # caller should get podman's own "command not found" from the exec. The PATH
  # here holds only the few tools the wrapper needs, so podman is genuinely
  # absent rather than shadowed.
  mkdir -p "$WORK/min"
  local tool
  for tool in bash date awk env; do
    ln -s "$(command -v "$tool")" "$WORK/min/$tool" 2>/dev/null || true
  done

  # 127 is the expected status: bash reports the missing podman itself.
  PATH="$WORK/min" run -127 bash "$SCRIPT" build
  [[ "$output" == *"podman"* ]]
}
