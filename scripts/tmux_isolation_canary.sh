#!/usr/bin/env bash
# tmux_isolation_canary.sh — prove that no tmux-touching test package can kill
# the ambient/default tmux server (ntm-6c7of).
#
# The repository has lost the operator's whole tmux server repeatedly while
# tests ran (2026-10-30 ~01:30 UTC with `go test -count=1 ./internal/pipeline/...`
# in flight; at least twice more before that). The guard in internal/tmux now
# refuses `kill-server` against an ambient/default socket, and every known
# real-tmux package self-isolates via TestMain — but both are properties of the
# current tree, not of the tests that will be written tomorrow. This harness is
# the mechanical check that the property still holds.
#
# How it works:
#   1. Start a SENTINEL tmux server under a private short socket root
#      ($ROOT/tmux-$UID/default) and create one sentinel session.
#   2. Run the selected `go test` packages with an ambient-shaped environment
#      pointing at the sentinel:
#          TMUX=$ROOT/tmux-$UID/default,<pid>,<sid>
#          TMUX_PANE=<sentinel pane>
#          TMUX_TMPDIR=$ROOT
#      A package that reaches "the ambient server" (an unisolated DefaultClient
#      call, a raw `tmux kill-server`, a forgotten TestMain) reaches the
#      sentinel — never the operator's real sessions.
#   3. After the run, fail unless the sentinel server and session are still
#      alive. That is the canary: if it dies, the walls leaked.
#
# No run can touch the operator's default server even when the walls leak,
# because the child environment redirects the default socket into $ROOT. The
# one residual exposure is a test that both clears TMUX_TMPDIR and shells out
# to raw `tmux kill-server` with no isolation; no such path exists in the
# current tree (see ntm-6c7of audit), and NTM_ALLOW_KILL_SERVER is stripped
# from the child environment so the internal guard stays armed.
#
# Usage:
#   bash scripts/tmux_isolation_canary.sh                 # focused package set
#   bash scripts/tmux_isolation_canary.sh --all           # go test -short ./...
#   bash scripts/tmux_isolation_canary.sh --strict        # test failures also fail
#   bash scripts/tmux_isolation_canary.sh --self-test     # prove the detector fires
#   bash scripts/tmux_isolation_canary.sh PKG [PKG...]    # explicit package patterns
#
# Exit status:
#   0  the sentinel survived. --self-test additionally proves the detector
#      fires, and --strict additionally requires the go test run to pass.
#   1  the sentinel died (the canary property) or --self-test's detector did
#      not fire — never tolerated; --strict also exits 1 on go test failure.
#      Without --strict a test failure is a loud warning because baseline test
#      failures are not the ambient-kill property this harness measures.
#
# Environment:
#   NTM_TMUX_CANARY_TIMEOUT   go test -timeout value (default 20m)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

SENTINEL_SESSION="ntm-canary-sentinel"
GO_TEST_TIMEOUT="${NTM_TMUX_CANARY_TIMEOUT:-20m}"

# Every package whose default (untagged, -short) run may touch a real tmux
# server, lowest cost first. This is the pre-commit/pre-release gate; --all
# sweeps the whole module instead.
DEFAULT_PACKAGES=(
  ./tests/testutil
  ./internal/tmux
  ./internal/status
  ./internal/checkpoint
  ./internal/completion
  ./internal/session
  ./internal/tui/dashboard
  ./internal/robot
  ./internal/cli
  ./internal/pipeline
)

ROOT=""
SENTINEL_SOCKET=""
SENTINEL_TMUX=""
SENTINEL_PANE=""

log() { printf 'tmux-canary: %s\n' "$*"; }
fail() { printf 'tmux-canary: FAIL: %s\n' "$*" >&2; exit 1; }

usage() {
  # Print the header block: skip the shebang, strip leading '# ', stop at the
  # first non-comment line so the implementation never leaks into --help.
  awk 'NR==1 { next } /^#/ { sub(/^# ?/, ""); print; next } { exit }' "${BASH_SOURCE[0]}"
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || fail "required tool '$1' is not on PATH"
}

# run_sentinel_tmux runs tmux against the sentinel root only: the ambient TMUX
# and the operator opt-in are stripped, and TMUX_TMPDIR pins the default socket
# under $ROOT. Callers must have set ROOT.
run_sentinel_tmux() {
  env -u TMUX -u TMUX_PANE -u NTM_ALLOW_KILL_SERVER \
    TMUX_TMPDIR="$ROOT" tmux "$@"
}

cleanup() {
  if [[ -n "$ROOT" && -d "$ROOT" ]]; then
    run_sentinel_tmux kill-server >/dev/null 2>&1 || true
    # The path is a mktemp-created directory owned by this invocation; refuse
    # to touch anything that does not carry the harness's fixed prefix.
    if [[ "$ROOT" == */ntm-tmux-canary-* ]]; then
      rm -r -- "$ROOT" 2>/dev/null || true
    fi
  fi
}
trap cleanup EXIT

sentinel_start() {
  local base="${TMPDIR:-/tmp}"
  base="${base%/}"

  ROOT="$(mktemp -d "$base/ntm-tmux-canary-XXXXXXXX")" || fail "create sentinel socket root"

  # Safety rail: never let the computed default socket coincide with the
  # operator's real one, even if TMPDIR is misconfigured to /tmp/...
  local real_default="/tmp/tmux-$(id -u)/default"
  local expected_socket="$ROOT/tmux-$(id -u)/default"
  if [[ "$expected_socket" == "$real_default" ]]; then
    fail "sentinel socket root $ROOT resolves to the operator's real default socket; refusing to run"
  fi

  run_sentinel_tmux new-session -d -s "$SENTINEL_SESSION" -x 80 -y 24 \
    || fail "start sentinel tmux server under $ROOT"

  SENTINEL_SOCKET="$(run_sentinel_tmux display-message -p '#{socket_path}')"
  local server_pid
  server_pid="$(run_sentinel_tmux display-message -p '#{pid}')"
  SENTINEL_PANE="$(run_sentinel_tmux list-panes -t "$SENTINEL_SESSION" -F '#{pane_id}' | head -n 1)"

  [[ "$SENTINEL_SOCKET" == "$expected_socket" ]] \
    || fail "sentinel socket resolved to $SENTINEL_SOCKET, expected $expected_socket"
  [[ "$server_pid" =~ ^[0-9]+$ ]] || fail "sentinel server pid '$server_pid' is not numeric"
  [[ "$SENTINEL_PANE" == %* ]] || fail "sentinel pane '$SENTINEL_PANE' is not a tmux pane id"

  # The TMUX value a process inside the sentinel server would inherit.
  SENTINEL_TMUX="$SENTINEL_SOCKET,$server_pid,0"
  log "sentinel server pid $server_pid on $SENTINEL_SOCKET (session $SENTINEL_SESSION, pane $SENTINEL_PANE)"
}

# sentinel_state prints "alive", "server-dead", or "session-missing: ..." and
# returns non-zero unless the server and the sentinel session are both present.
sentinel_state() {
  local sessions
  if ! sessions="$(run_sentinel_tmux list-sessions 2>/dev/null)"; then
    printf 'server-dead'
    return 1
  fi
  if grep -q "^${SENTINEL_SESSION}:" <<<"$sessions"; then
    printf 'alive'
    return 0
  fi
  printf 'session-missing (server sessions: %s)' "$(tr '\n' ' ' <<<"$sessions")"
  return 1
}

run_tests() {
  local -a packages=("$@")
  local -a test_env=(
    -u NTM_ALLOW_KILL_SERVER
    -u NTM_TEST_TMUX_ENV_OWNED
    -u NTM_TMUX_BINARY
    -u NTM_TMUX_TEST_TMPDIR
    "TMUX=$SENTINEL_TMUX"
    "TMUX_PANE=$SENTINEL_PANE"
    "TMUX_TMPDIR=$ROOT"
  )

  log "running: go test -short -count=1 -timeout=$GO_TEST_TIMEOUT ${packages[*]}"
  log "ambient-shaped child env: TMUX=$SENTINEL_TMUX TMUX_TMPDIR=$ROOT"

  local logfile="$ROOT/go-test.log"
  local test_status=0
  set +e
  env "${test_env[@]}" go test -short -count=1 -timeout "$GO_TEST_TIMEOUT" "${packages[@]}" 2>&1 \
    | tee "$logfile"
  test_status=${PIPESTATUS[0]}
  set -e

  local state
  local sentinel_status=0
  state="$(sentinel_state)" || sentinel_status=$?

  # Preserve the full output outside $ROOT, which cleanup removes, so a failing
  # run leaves something to inspect.
  local keep_log="$(dirname "$ROOT")/$(basename "$ROOT").go-test.log"
  if (( test_status != 0 || sentinel_status != 0 )); then
    cp "$logfile" "$keep_log" 2>/dev/null && log "full go test output preserved at $keep_log"
  fi

  if (( sentinel_status != 0 )); then
    log "go test exited $test_status; sentinel state: $state"
    fail "the sentinel tmux server died during the run — a package under test reached the ambient server (ntm-6c7of); log: $keep_log"
  fi

  if (( test_status != 0 )); then
    if [[ "$strict" == true ]]; then
      fail "go test failed (exit $test_status) but the sentinel survived; --strict was requested (log: $keep_log)"
    fi
    log "------------------------------------------------------------"
    log "WARNING: go test FAILED (exit $test_status) while the sentinel survived."
    log "The ambient-kill canary property held; the test failure is separate."
    log "Inspect: $keep_log"
    log "------------------------------------------------------------"
  fi

  log "CANARY OK: ${packages[*]} completed; the sentinel survived"
}

self_test() {
  sentinel_start

  local state
  if ! state="$(sentinel_state)"; then
    fail "self-test precondition: sentinel is not alive after start ($state)"
  fi

  # Bypass the internal guard entirely: raw tmux, ambient-shaped env rooted at
  # the sentinel. The detector must notice this death or it is a placebo.
  run_sentinel_tmux kill-server
  if state="$(sentinel_state)"; then
    fail "DETECTOR BROKEN: sentinel still alive after a raw kill-server"
  fi

  log "OK: self-test — the detector observed the sentinel die ($state)"
}

mode="tests"
run_all=false
strict=false
packages=()

for arg in "$@"; do
  case "$arg" in
    --self-test)
      mode="self-test"
      ;;
    --all)
      run_all=true
      ;;
    --strict)
      strict=true
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --*)
      fail "unknown option: $arg (see --help)"
      ;;
    *)
      packages+=("$arg")
      ;;
  esac
done

if [[ "$mode" == "self-test" ]]; then
  [[ "$run_all" == false && "$strict" == false && "${#packages[@]}" -eq 0 ]] \
    || fail "--self-test cannot be combined with package patterns, --all, or --strict"
  require_tool tmux
  self_test
  exit 0
fi

require_tool tmux
require_tool go

if [[ "$run_all" == true ]]; then
  [[ "${#packages[@]}" -eq 0 ]] || fail "--all cannot be combined with explicit package patterns"
  packages=("./...")
elif [[ "${#packages[@]}" -eq 0 ]]; then
  packages=("${DEFAULT_PACKAGES[@]}")
fi

sentinel_start
run_tests "${packages[@]}"