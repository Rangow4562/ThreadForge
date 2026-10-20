#!/usr/bin/env bash
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     https://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
# Run the bundled Harbor Terminus-2 fork; model selection is set by Threadforge.
set -euo pipefail
REPO="${RRSI_CODING_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}"
THREADFORGE_ROOT="${THREADFORGE_ROOT:-$(cd "$REPO/../.." && pwd)}"

JOB_NAME="${1:?job name required}"
DATASET="${2:?dataset required}"
N_ATTEMPTS="${3:?attempt count required}"
N_CONCURRENT="${4:?concurrency required}"
shift 4

JOBS_DIR="$REPO/runs/jobs"
EXTRA=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --jobs-dir) JOBS_DIR="$2"; shift 2;;
    *) EXTRA+=("$1"); shift;;
  esac
done

VENV="${RRSI_CODING_VENV:?set RRSI_CODING_VENV to the isolated Harbor virtual environment}"
MODEL="${RRSI_POLICY_MODEL:?Threadforge must select a local model or explicitly opted-in remote model}"
HARBOR="$VENV/bin/harbor"
if [[ ! -x "$HARBOR" ]]; then
  echo "Harbor CLI is unavailable in RRSI_CODING_VENV" >&2
  exit 2
fi

export PYTHONPATH="$THREADFORGE_ROOT/third_party"
exec "$HARBOR" run \
  -d "$DATASET" \
  --agent harbor_terminus2:AgentHarness \
  -m "$MODEL" \
  --agent-kwarg 'llm_kwargs={"num_retries": 10}' \
  -k "$N_ATTEMPTS" \
  -n "$N_CONCURRENT" \
  --jobs-dir "$JOBS_DIR" \
  --job-name "$JOB_NAME" \
  "${EXTRA[@]}"
