#!/usr/bin/env bash
set -euo pipefail

mode="${THREADFORGE_ACTION_MODE:-}"
case "$mode" in
  start|finish) ;;
  *) echo "mode must be start or finish" >&2; exit 2 ;;
esac

action_path_shell="${THREADFORGE_ACTION_PATH:-}"
if [[ -z "$action_path_shell" ]]; then
  echo "THREADFORGE_ACTION_PATH is required" >&2
  exit 2
fi
if [[ "${RUNNER_OS:-}" == Windows ]]; then
  if ! command -v cygpath >/dev/null 2>&1; then
    echo "cygpath is required for the Windows bash runner" >&2
    exit 2
  fi
  action_path_shell="$(cygpath -u "$action_path_shell")"
fi

minimum="${THREADFORGE_ACTION_MIN_EVENTS:-inherit}"
required="${THREADFORGE_ACTION_REQUIRE_TELEMETRY:-inherit}"
security_fail_on="${THREADFORGE_ACTION_SECURITY_FAIL_ON:-}"
security_fail_on_valid=true
case "$security_fail_on" in
  ''|info|low|medium|high|critical) ;;
  *) security_fail_on_valid=false ;;
esac
if [[ "$required" != inherit && "$required" != true && "$required" != false ]]; then
  echo "require-telemetry must be true, false, or inherit" >&2
  exit 2
fi

binary="${THREADFORGE_ACTION_CI_BINARY_PATH:-}"
if [[ "$mode" == start || -z "$binary" || ! -f "$binary" ]]; then
  if [[ "$mode" == finish ]]; then
    echo "the Threadforge CI action must start a session before finishing it" >&2
    exit 2
  fi
  if [[ -z "${RUNNER_TEMP:-}" || -z "${GITHUB_RUN_ID:-}" || -z "${GITHUB_ENV:-}" ]]; then
    echo "start requires RUNNER_TEMP, GITHUB_RUN_ID, and GITHUB_ENV" >&2
    exit 2
  fi
  runner_temp_shell="$RUNNER_TEMP"
  github_env_shell="$GITHUB_ENV"
  if [[ "${RUNNER_OS:-}" == Windows ]]; then
    runner_temp_shell="$(cygpath -u "$runner_temp_shell")"
    github_env_shell="$(cygpath -u "$github_env_shell")"
  fi
  build_root="$runner_temp_shell/threadforge-ci-action"
  mkdir -p "$build_root"
  build_dir="$(mktemp -d "$build_root/run.XXXXXX")"
  binary="$build_dir/threadforge"
  if [[ "${RUNNER_OS:-}" == Windows ]]; then
    binary="$binary.exe"
  fi
  action_root="$(cd "$action_path_shell/../../.." && pwd)"
  (
    cd "$action_root"
    GOWORK=off go build -trimpath -o "$binary" ./cmd/threadforge
  )
  printf 'THREADFORGE_ACTION_CI_BINARY_PATH=%s\n' "$binary" >> "$github_env_shell"
fi

if [[ ! -f "$binary" ]]; then
  echo "the Threadforge CI action binary is unavailable" >&2
  exit 2
fi

args=()
if [[ "$mode" == start ]]; then
  [[ "$minimum" == inherit ]] && minimum=1
  if [[ ! "$minimum" =~ ^[1-9][0-9]*$ ]]; then
    echo "min-events must be a positive integer for start" >&2
    exit 2
  fi
  [[ "$required" == inherit ]] && required=true
  args=(ci start --harness "${THREADFORGE_ACTION_HARNESS:-claude-code}" --min-events "$minimum" "--require-telemetry=$required")
  if [[ -n "${THREADFORGE_ACTION_OUTPUT_DIR:-}" ]]; then
    args+=(--output-dir "$THREADFORGE_ACTION_OUTPUT_DIR")
  fi
  if [[ -n "${THREADFORGE_ACTION_STATE_PATH:-}" ]]; then
    args+=(--state-path "$THREADFORGE_ACTION_STATE_PATH")
  fi
else
  args=(ci finish --json)
  if [[ "$minimum" != inherit ]]; then
    if [[ ! "$minimum" =~ ^[1-9][0-9]*$ ]]; then
      echo "min-events must be a positive integer or inherit for finish" >&2
      exit 2
    fi
    args+=(--min-events "$minimum")
  fi
  if [[ "$required" != inherit ]]; then
    args+=("--require-telemetry=$required")
  fi
  if [[ -n "${THREADFORGE_ACTION_STATE_PATH:-}" ]]; then
    args+=(--state-path "$THREADFORGE_ACTION_STATE_PATH")
  fi
fi

"$binary" "${args[@]}"

if [[ "$mode" == finish ]]; then
  if [[ "$security_fail_on_valid" != true ]]; then
    echo "security-fail-on must be empty, info, low, medium, high, or critical" >&2
    exit 2
  fi
  if [[ -n "$security_fail_on" ]]; then
    artifact_dir="${THREADFORGE_CI_ARTIFACT_DIR:-}"
    if [[ "${RUNNER_OS:-}" == Windows && -n "$artifact_dir" ]]; then
      artifact_dir="$(cygpath -u "$artifact_dir")"
    fi
    if [[ -z "$artifact_dir" || ! -f "$artifact_dir/runtime.jsonl" ]]; then
      echo "CI artifact log is unavailable for the requested security gate" >&2
      exit 2
    fi
    "$binary" ci gate --log-path "$artifact_dir/runtime.jsonl" \
      --harness "${THREADFORGE_ACTION_HARNESS:-claude-code}" \
      --min-events 1 --fail-on "$security_fail_on" --json
  fi
fi
