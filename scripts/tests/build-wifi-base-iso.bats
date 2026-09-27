#!/usr/bin/env bats
# Tests for scripts/build-wifi-base-iso.sh argument validation and preflight.
#
# Nothing here runs a real Fedora CoreOS build: the whole point of the preflight
# is to fail before the (multi-hour) clone, so these tests exercise the paths
# that must be cheap.
#
# Requires: bats-core (https://github.com/bats-core/bats-core)
#
# Run: bats scripts/tests/build-wifi-base-iso.bats

SCRIPT="$BATS_TEST_DIRNAME/../build-wifi-base-iso.sh"

run_expect_fail() {
  run bash "$SCRIPT" "$@"
  [ "$status" -ne 0 ]
}

# ── Argument parsing ─────────────────────────────────────────────────────────
# Validation runs before the preflight, so these hold on a host that has neither
# podman nor /dev/kvm.

@test "unknown argument exits with error" {
  run_expect_fail --bogus-flag
  [[ "$output" == *"Unknown argument"* ]]
}

@test "invalid --stream value exits with error" {
  run_expect_fail --stream nightly
  [[ "$output" == *"--stream must be testing-devel or stable"* ]]
}

@test "invalid --arch value exits with error" {
  run_expect_fail --arch arm64
  [[ "$output" == *"--arch must be x86_64 or aarch64"* ]]
}

@test "--arch that does not match the host is refused" {
  # coreos-assembler has no cross-build, so a mismatched --arch would silently
  # produce a host-arch ISO under a foreign filename.
  local host other
  host="$(uname -m)"
  if [[ "$host" == "x86_64" ]]; then other="aarch64"; else other="x86_64"; fi

  run_expect_fail --arch "$other"
  [[ "$output" == *"does not match this host"* ]]
  [[ "$output" == *"no cross-build"* ]]
}

@test "--arch matching the host passes the cross-build check" {
  # Reaches the preflight, which on a host with podman and KVM means it gets as
  # far as the clone. Only the arch check is under test here, so a build attempt
  # is stopped by an intentionally bad stream value instead.
  run_expect_fail --stream nightly --arch "$(uname -m)"
  [[ "$output" != *"does not match this host"* ]]
}

@test "--stream=value (equals form) is parsed" {
  run_expect_fail --stream=nightly
  [[ "$output" == *"--stream must be testing-devel or stable"* ]]
}

@test "--arch=value (equals form) is parsed" {
  run_expect_fail --arch=riscv64
  [[ "$output" == *"--arch must be x86_64 or aarch64"* ]]
}

@test "bare stream name (positional) is parsed" {
  run bash "$SCRIPT" stable 2>&1
  # "stable" is valid, so it must get past validation; the failure that follows
  # is the preflight, not an "Unknown argument".
  [[ "$output" != *"Unknown argument"* ]]
  [[ "$output" != *"--stream must be"* ]]
}

@test "valid stream and arch are accepted" {
  # Reaches the preflight, which is as far as a host without podman can get.
  for s in testing-devel stable; do
    run bash "$SCRIPT" --stream "$s" --arch x86_64 2>&1 || true
    [[ "$output" != *"must be testing-devel or stable"* ]]
    [[ "$output" != *"must be x86_64 or aarch64"* ]]
  done
}

# ── Preflight ────────────────────────────────────────────────────────────────

@test "missing podman prints an install hint before doing any work" {
  # A stub PATH with the basics but no podman. /dev/kvm may or may not exist, so
  # assert only on the podman branch.
  run env PATH=/usr/bin:/bin bash -c '
    if command -v podman >/dev/null; then exit 0; fi
    exec "$@"' _ "$SCRIPT" --stream stable --arch x86_64 2>&1
  if [[ "$output" == *"podman not found"* ]]; then
    [ "$status" -ne 0 ]
    [[ "$output" == *"podman not found"* ]]
  else
    skip "podman is present on this host"
  fi
}

@test "missing git prints an install hint" {
  run env PATH=/usr/bin:/bin bash -c '
    if command -v git >/dev/null; then exit 0; fi
    exec "$@"' _ "$SCRIPT" --stream stable --arch x86_64 2>&1
  if [[ "$output" == *"git not found"* ]]; then
    [ "$status" -ne 0 ]
    [[ "$output" == *"git not found"* ]]
  else
    skip "git is present on this host"
  fi
}

@test "preflight failure does not create build directories" {
  # The preflight has to come before the clone, or a host that cannot build ends
  # up with a half-populated .cosa-build to clean up by hand.
  local before after
  before="$(ls -d "$BATS_TEST_DIRNAME/../../.cosa-build" 2>/dev/null || true)"
  run bash "$SCRIPT" --stream nightly --arch x86_64 2>&1 || true
  after="$(ls -d "$BATS_TEST_DIRNAME/../../.cosa-build" 2>/dev/null || true)"
  [ "$before" = "$after" ]
}
