#!/usr/bin/env bash
# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.
set -euo pipefail

output=""
url=""
while (($#)); do
  case "$1" in
    -o)
      output="$2"
      shift 2
      ;;
    -w | --max-filesize | --proto | --proto-redir)
      shift 2
      ;;
    -* )
      shift
      ;;
    *)
      url="$1"
      shift
      ;;
  esac
done

case "$url" in
  https://github.com/Rangow4562/ThreadForge/releases/latest)
    printf '%s' "$THREADFORGE_TEST_LATEST_URL"
    ;;
  */threadforge_0.4.0_linux_amd64.tar.gz)
    [[ -n "$output" ]] || exit 2
    cp "${THREADFORGE_TEST_FIXTURE_DIR}/archive.tar.gz" "$output"
    ;;
  */checksums.txt)
    [[ -n "$output" ]] || exit 2
    cp "${THREADFORGE_TEST_FIXTURE_DIR}/checksums.txt" "$output"
    ;;
  *)
    printf 'unexpected mock curl URL: %s\n' "$url" >&2
    exit 90
    ;;
esac
