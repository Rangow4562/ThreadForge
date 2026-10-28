#!/usr/bin/env bash
# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/threadforge-installer-test.XXXXXXXX")"
trap 'rm -rf -- "$tmp"' EXIT

fixture="${tmp}/fixture"
mock_bin="${tmp}/mock-bin"
payload="${tmp}/payload"
mkdir -p "$fixture" "$mock_bin" "$payload"
printf '#!/bin/sh\n[ "${1:-}" = version ] || exit 9\nprintf "threadforge version 0.4.0\\n"\n' > "${payload}/threadforge"
chmod 0755 "${payload}/threadforge"
tar -czf "${fixture}/archive.tar.gz" -C "$payload" threadforge
archive="threadforge_0.4.0_linux_amd64.tar.gz"
checksum="$(sha256sum "${fixture}/archive.tar.gz" | awk '{print $1}')"
printf '%s  %s\r\n%s  unrelated.txt\r\n' "$checksum" "$archive" "$(printf '0%.0s' {1..64})" > "${fixture}/checksums.txt"
cp "${root}/packaging/linux/testdata/mock-curl.sh" "${mock_bin}/curl"
chmod 0755 "${mock_bin}/curl"
export THREADFORGE_TEST_FIXTURE_DIR="$fixture"
export THREADFORGE_TEST_LATEST_URL='https://github.com/Rangow4562/ThreadForge/releases/tag/v0.4.0'
export PATH="${mock_bin}:${PATH}"

THREADFORGE_VERSION=0.4.0 THREADFORGE_INSTALL_DIR="${tmp}/pinned-bin" \
  bash "${root}/packaging/linux/install.sh" > "${tmp}/pinned-output.txt"
[[ -x "${tmp}/pinned-bin/threadforge" ]]
grep -q 'threadforge version 0.4.0' "${tmp}/pinned-output.txt"

env -u THREADFORGE_VERSION THREADFORGE_INSTALL_DIR="${tmp}/latest-bin" \
  bash "${root}/packaging/linux/install.sh" > "${tmp}/latest-output.txt"
[[ -x "${tmp}/latest-bin/threadforge" ]]
grep -q 'threadforge version 0.4.0' "${tmp}/latest-output.txt"

if THREADFORGE_VERSION='../0.4.0' THREADFORGE_INSTALL_DIR="${tmp}/invalid-version" \
  bash "${root}/packaging/linux/install.sh" > /dev/null 2>&1; then
  echo 'installer accepted an invalid version' >&2
  exit 1
fi
[[ ! -e "${tmp}/invalid-version" ]]

printf '%s  %s\r\n' "$(printf '0%.0s' {1..64})" "$archive" > "${fixture}/checksums.txt"
if THREADFORGE_VERSION=0.4.0 THREADFORGE_INSTALL_DIR="${tmp}/bad-checksum" \
  bash "${root}/packaging/linux/install.sh" > /dev/null 2>&1; then
  echo 'installer accepted a checksum mismatch' >&2
  exit 1
fi
[[ ! -e "${tmp}/bad-checksum" ]]

printf 'unexpected payload\n' > "${payload}/extra.txt"
tar -czf "${fixture}/archive.tar.gz" -C "$payload" threadforge extra.txt
checksum="$(sha256sum "${fixture}/archive.tar.gz" | awk '{print $1}')"
printf '%s  %s\r\n' "$checksum" "$archive" > "${fixture}/checksums.txt"
if THREADFORGE_VERSION=0.4.0 THREADFORGE_INSTALL_DIR="${tmp}/bad-archive" \
  bash "${root}/packaging/linux/install.sh" > /dev/null 2>&1; then
  echo 'installer accepted an archive with an unexpected member' >&2
  exit 1
fi
[[ ! -e "${tmp}/bad-archive" ]]

rm -f "${payload}/extra.txt"
printf '#!/bin/sh\nexit 9\n' > "${payload}/threadforge"
tar -czf "${fixture}/archive.tar.gz" -C "$payload" threadforge
checksum="$(sha256sum "${fixture}/archive.tar.gz" | awk '{print $1}')"
printf '%s  %s\r\n' "$checksum" "$archive" > "${fixture}/checksums.txt"
mkdir -p "${tmp}/rollback-bin"
printf '#!/bin/sh\necho preserved-existing-binary\n' > "${tmp}/rollback-bin/threadforge"
chmod 0755 "${tmp}/rollback-bin/threadforge"
if THREADFORGE_VERSION=0.4.0 THREADFORGE_INSTALL_DIR="${tmp}/rollback-bin" \
  bash "${root}/packaging/linux/install.sh" > /dev/null 2>&1; then
  echo 'installer accepted a binary that failed the version check' >&2
  exit 1
fi
grep -q 'preserved-existing-binary' "${tmp}/rollback-bin/threadforge"

echo 'Linux installer fixture tests passed (pinned/latest install, version validation, checksum/archive rejection, atomic rollback).'
