#!/usr/bin/env bash
set -Eeuo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: test-elasticsearch-stack.sh <threadforge-binary> <new-pack-directory>" >&2
  exit 2
fi

threadforge="$1"
pack_dir="$2"
if [[ ! -x "$threadforge" ]]; then
  echo "Threadforge binary must exist and be executable" >&2
  exit 2
fi
pack_dir="$(python3 -c 'import os,sys; print(os.path.abspath(sys.argv[1]))' "$pack_dir")"
if [[ -e "$pack_dir" || -L "$pack_dir" ]]; then
  echo "pack directory must not already exist" >&2
  exit 2
fi
mkdir -p "$(dirname "$pack_dir")"

"$threadforge" endpoint elasticsearch install-pack \
  --endpoint https://elasticsearch.example:9200 \
  --allow-remote-metadata-forwarding \
  --log-path "${RUNNER_TEMP:-/tmp}/threadforge-not-used-runtime.jsonl" \
  --output "$pack_dir"

cleanup() {
  "$threadforge" endpoint elasticsearch down --pack-dir "$pack_dir" >/dev/null 2>&1 || true
}
trap cleanup EXIT

"$threadforge" endpoint elasticsearch up \
  --pack-dir "$pack_dir" \
  --allow-image-pulls

docker_context="$(docker context show)"
docker_endpoint="$(docker context inspect --format '{{json .Endpoints.docker}}' "$docker_context" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin).get("Host", ""))')"
case "${docker_endpoint,,}" in
  unix://*|npipe://*) ;;
  *)
    echo "Elasticsearch integration probe refuses non-local Docker context endpoint: $docker_endpoint" >&2
    exit 1
    ;;
esac

project_name="$(python3 - "$pack_dir" <<'PY'
import hashlib
import os
import sys

pack_dir = os.path.normpath(os.path.abspath(sys.argv[1]))
default_dir = os.path.normpath(os.path.abspath("threadforge-elasticsearch-pack"))
if pack_dir == default_dir:
    print("threadforge-elasticsearch-local")
else:
    digest = hashlib.sha256(pack_dir.encode("utf-8")).hexdigest()[:16]
    print(f"threadforge-elasticsearch-{digest}")
PY
)"

# Match the CLI's explicit local context and prevent ambient Docker/Compose
# overrides from redirecting the assertion container or changing its project.
unset DOCKER_HOST DOCKER_CONTEXT DOCKER_TLS_VERIFY DOCKER_CERT_PATH
unset COMPOSE_FILE COMPOSE_PROJECT_NAME COMPOSE_PROFILES COMPOSE_ENV_FILES
unset COMPOSE_PATH_SEPARATOR COMPOSE_DISABLE_ENV_FILE

compose_probe() {
  entrypoint="$1"
  shift
  docker --context "$docker_context" compose \
    --project-name "$project_name" \
    --project-directory "$pack_dir" \
    --env-file "$pack_dir/docker-compose.env" \
    --file "$pack_dir/docker-compose.yml" \
    run --rm --no-deps --no-TTY --entrypoint "$entrypoint" setup "$@"
}

verify_document='import json,sys
result=json.load(sys.stdin)
hits=result.get("hits",{}).get("hits",[])
if not hits: raise SystemExit(1)
source=hits[0].get("_source",{})
if set(source)!={"@timestamp","severity","threadforge_export"}: raise SystemExit(1)
if not isinstance(source.get("@timestamp"),str) or source["severity"] not in {"info","low","medium","high","critical","unknown"}: raise SystemExit(1)
marker=source.get("threadforge_export",{})
if set(marker)!={"schema_version","raw_content"} or marker!={"schema_version":"threadforge.metadata-export.v1","raw_content":False}: raise SystemExit(1)'

wait_for_document="$(cat <<'SH'
attempt=0
while [ "$attempt" -lt 90 ]; do
  response="$(curl --fail --silent --show-error --max-time 5 \
    -H "Content-Type: application/json" \
    --data-binary '{"size":1,"query":{"term":{"threadforge_export.schema_version":"threadforge.metadata-export.v1"}}}' \
    http://elasticsearch:9200/logs-threadforge.endpoint-default/_search 2>/dev/null)" || response=""
  case "$response" in
    *'"_source"'*) printf '%s\n' "$response"; exit 0 ;;
  esac
  attempt=$((attempt+1))
  sleep 5
done
echo "timed out waiting for the synthetic Elasticsearch document" >&2
exit 1
SH
)"

response="$(compose_probe /bin/sh -eu -c "$wait_for_document")" || {
  echo "The synthetic fixture did not arrive with the exact metadata-only document shape" >&2
  exit 1
}
if ! python3 -c "$verify_document" <<<"$response"; then
  echo "The synthetic fixture did not arrive with the exact metadata-only document shape" >&2
  exit 1
fi

compose_probe curl --fail --silent --show-error --max-time 10 \
  -H 'kbn-xsrf: threadforge' \
  http://kibana:5601/api/saved_objects/index-pattern/threadforge-metadata-stream \
  | python3 -c 'import json,sys; value=json.load(sys.stdin); assert value.get("id")=="threadforge-metadata-stream" and value.get("type")=="index-pattern"'

echo "Elasticsearch received only the synthetic metadata fixture; strict field shape and Kibana data-view setup verified from the isolated Compose network."
