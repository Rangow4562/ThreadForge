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
# Modified by Threadforge: the driver owns per-task loopback gateways and uses
# a local-default policy model; this wrapper never enables Vertex or web access.
set -euo pipefail
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ -d "$REPO/../archipelago/harness_workspace" ]]; then
  ARCHIPELAGO="$REPO/../archipelago"
else
  ARCHIPELAGO="$REPO/../../third_party/archipelago"
fi
VENV="${VENV:-${RRSI_AGENT_PYTHON:?set RRSI_AGENT_PYTHON}}"
label="${1:?label}"
tasks="${2:?tasks json}"
shift 2

if [[ ! "$label" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}/s[0-9]{1,2}$ ]]; then
  echo "invalid workspace run label" >&2
  exit 2
fi
if [[ "${THREADFORGE_BENCHMARK_EXECUTION_OPT_IN:-false}" != "true" ]]; then
  echo "benchmark execution requires Threadforge benchmark_execution_opt_in=true" >&2
  exit 2
fi
if [[ "${THREADFORGE_EVALUATION_BACKEND:-local}" == "remote" && "${THREADFORGE_RRSI_REMOTE_INFERENCE_OPT_IN:-false}" != "true" ]]; then
  echo "remote policy inference requires remote_inference_opt_in=true" >&2
  exit 2
fi
if [[ ! -x "$VENV" || ! -f "$tasks" || ! -d "${HARVEY_LAB_ROOT:?set HARVEY_LAB_ROOT}" ]]; then
  echo "workspace runner prerequisites are unavailable" >&2
  exit 2
fi
if [[ ! -f "$ARCHIPELAGO/harness_workspace/main.py" ]]; then
  echo "vendored Archipelago workspace harness is unavailable" >&2
  exit 2
fi
"$VENV" -m compileall -q "$ARCHIPELAGO/harness_workspace"
MODEL_LABEL="$label" "$VENV" "$REPO/workspace_driver.py" --subset "$tasks" "$@"
