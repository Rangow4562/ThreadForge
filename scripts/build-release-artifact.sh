#!/usr/bin/env bash
# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.
set -euo pipefail

usage() {
  echo "usage: build-release-artifact.sh VERSION GOOS GOARCH OUTPUT_DIR" >&2
  exit 2
}

[[ $# -eq 4 ]] || usage

version="$1"
goos="$2"
goarch="$3"
output_dir="$4"
[[ -n "$output_dir" && "$output_dir" != "/" ]] || usage

if [[ ! "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "release version must be stable semantic version without a v prefix" >&2
  exit 2
fi

case "${goos}/${goarch}" in
  linux/amd64|darwin/amd64|darwin/arm64|windows/amd64) ;;
  *)
    echo "unsupported release target: ${goos}/${goarch}" >&2
    exit 2
    ;;
esac

source_date_epoch="${SOURCE_DATE_EPOCH:-$(git show -s --format=%ct HEAD)}"
if [[ ! "$source_date_epoch" =~ ^[0-9]+$ ]] || (( source_date_epoch < 315532800 )); then
  echo "SOURCE_DATE_EPOCH must be a Unix timestamp on or after 1980-01-01" >&2
  exit 2
fi

mkdir -p "$output_dir/.staging"
output_dir="$(cd "$output_dir" && pwd -P)"
binary_name=threadforge
archive_extension=tar.gz
if [[ "$goos" == windows ]]; then
  binary_name=threadforge.exe
  archive_extension=zip
fi

stem="threadforge_${version}_${goos}_${goarch}"
binary_path="$output_dir/.staging/${goos}-${goarch}/${binary_name}"
archive_path="$output_dir/${stem}.${archive_extension}"
sbom_path="$output_dir/${stem}.spdx.json"
mkdir -p "$(dirname "$binary_path")"

env CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOFLAGS= GOTOOLCHAIN=local GOWORK=off \
  go build -trimpath -buildvcs=false \
    -ldflags="-s -w -buildid= -X github.com/threadforge-ai/threadforge/internal/cli.version=${version}" \
    -o "$binary_path" ./cmd/threadforge
chmod 0755 "$binary_path"

touch -d "@${source_date_epoch}" "$binary_path"

if [[ "$archive_extension" == tar.gz ]]; then
  tar --sort=name --mtime="@${source_date_epoch}" --owner=0 --group=0 --numeric-owner \
    --format=gnu -cf - -C "$(dirname "$binary_path")" "$binary_name" |
    gzip -n -9 > "$archive_path"
  archive_entries="$(tar -tzf "$archive_path")"
  if [[ "$archive_entries" != "$binary_name" ]]; then
    echo "release archive must contain exactly ${binary_name}" >&2
    exit 1
  fi
else
  python3 scripts/package_release_zip.py "$archive_path" "$binary_path" "$binary_name" "$source_date_epoch"
fi

if [[ "$goos/$goarch" == "linux/amd64" && "$(go env GOOS)/$(go env GOARCH)" == "linux/amd64" ]]; then
  actual_version="$("$binary_path" version)"
  if [[ "$actual_version" != "threadforge ${version}" ]]; then
    echo "built CLI reports ${actual_version}; expected threadforge ${version}" >&2
    exit 1
  fi
fi

if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  workspace="${GITHUB_WORKSPACE:-$(git rev-parse --show-toplevel)}"
  {
    echo "binary_path=$(realpath --relative-to="$workspace" "$binary_path")"
    echo "archive_path=$(realpath --relative-to="$workspace" "$archive_path")"
    echo "sbom_path=$(realpath --relative-to="$workspace" "$sbom_path")"
  } >> "$GITHUB_OUTPUT"
fi

printf 'Built %s (embedded version %s)\n' "$archive_path" "$version"
