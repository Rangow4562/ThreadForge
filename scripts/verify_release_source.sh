#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${GITHUB_EVENT_NAME:-}" || -z "${GITHUB_REF:-}" || -z "${GITHUB_SHA:-}" ]]; then
  echo "Release source verification requires GITHUB_EVENT_NAME, GITHUB_REF, and GITHUB_SHA." >&2
  exit 2
fi

if [[ "$(git rev-parse HEAD)" != "$GITHUB_SHA" ]]; then
  echo "Checked-out release candidate does not match GITHUB_SHA." >&2
  exit 1
fi

case "$GITHUB_EVENT_NAME" in
  workflow_dispatch)
    if [[ "$GITHUB_REF" != "refs/heads/main" ]]; then
      echo "Release-pipeline rehearsals must be dispatched from refs/heads/main." >&2
      exit 1
    fi
    ;;
  push)
    if [[ "$GITHUB_REF" != refs/tags/v* ]]; then
      echo "Release candidate push must use a v-prefixed tag." >&2
      exit 1
    fi
    git fetch origin main --no-tags
    if ! git merge-base --is-ancestor "$GITHUB_SHA" FETCH_HEAD; then
      echo "Tagged release commit is not reachable from origin/main." >&2
      exit 1
    fi
    ;;
  *)
    echo "Unsupported release source event: $GITHUB_EVENT_NAME." >&2
    exit 2
    ;;
esac
