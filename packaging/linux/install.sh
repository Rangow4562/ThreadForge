#!/usr/bin/env bash
# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.
set -euo pipefail

repo="Rangow4562/ThreadForge"
release_base="https://github.com/${repo}/releases"

fail() {
  printf 'threadforge install: %s\n' "$*" >&2
  exit 1
}

[[ "$(uname -s)" == Linux ]] || fail "Linux only; see docs/linux-install.md for other installation paths."
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  *) fail "no official Linux release asset for $(uname -m); build from source or use a verified package." ;;
esac

for program in curl awk sha256sum tar mktemp install mv mkdir rm; do
  command -v "$program" >/dev/null 2>&1 || fail "required command not found: $program"
done

if [[ -n "${THREADFORGE_INSTALL_DIR:-}" ]]; then
  install_dir="$THREADFORGE_INSTALL_DIR"
else
  [[ -n "${HOME:-}" ]] || fail 'HOME is unset; set THREADFORGE_INSTALL_DIR to an absolute path.'
  install_dir="$HOME/.local/bin"
fi
[[ "$install_dir" == /* ]] || fail 'THREADFORGE_INSTALL_DIR must be an absolute path.'
case "/$install_dir/" in
  */../*) fail 'THREADFORGE_INSTALL_DIR must not contain parent-directory components.' ;;
esac

version="${THREADFORGE_VERSION:-}"
if [[ -z "$version" ]]; then
  latest_url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
    --proto '=https' --proto-redir '=https' "${release_base}/latest")" ||
    fail 'could not resolve the latest GitHub release.'
  version="${latest_url##*/}"
fi
version="${version#v}"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]] ||
  fail 'THREADFORGE_VERSION must be a release version such as 0.4.0 or v0.4.0.'

archive="threadforge_${version}_linux_${arch}.tar.gz"
base="${release_base}/download/v${version}"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/threadforge-install.XXXXXXXX")" || fail 'could not create a temporary directory.'
staged=""
cleanup() {
  if [[ -n "$staged" ]]; then rm -f -- "$staged"; fi
  rm -rf -- "$tmp"
}
trap cleanup EXIT

curl -fsSL --max-filesize 268435456 --proto '=https' --proto-redir '=https' \
  "${base}/${archive}" -o "${tmp}/${archive}" || fail "could not download ${archive}."
curl -fsSL --max-filesize 1048576 --proto '=https' --proto-redir '=https' \
  "${base}/checksums.txt" -o "${tmp}/checksums.txt" || fail 'could not download release checksums.'

checksum="$(awk -v name="$archive" \
  '{ filename = $2; sub(/\r$/, "", filename); if (filename == name) { count++; value = $1 } } \
  END { if (count == 1) print value; else exit 1 }' \
  "${tmp}/checksums.txt")" || fail "checksums.txt must contain exactly one entry for ${archive}."
[[ "$checksum" =~ ^[[:xdigit:]]{64}$ ]] || fail "invalid SHA-256 entry for ${archive}."
printf '%s  %s\n' "$checksum" "$archive" |
  (cd "$tmp" && sha256sum --check --status -) || fail "SHA-256 verification failed for ${archive}."

mapfile -t members < <(tar -tzf "${tmp}/${archive}")
[[ "${#members[@]}" -eq 1 && "${members[0]}" == threadforge ]] ||
  fail 'release archive must contain exactly one top-level threadforge binary.'
tar -xzf "${tmp}/${archive}" -C "$tmp" --no-same-owner -- threadforge ||
  fail 'could not extract the verified CLI binary.'
[[ -f "${tmp}/threadforge" && ! -L "${tmp}/threadforge" ]] || fail 'release did not contain a regular CLI binary.'

if [[ -e "$install_dir/threadforge" && -d "$install_dir/threadforge" ]]; then
  fail "${install_dir}/threadforge is a directory."
fi
mkdir -p -- "$install_dir" || fail "could not create install directory ${install_dir}."
[[ -d "$install_dir" && ! -L "$install_dir" ]] || fail 'install directory must be a real directory, not a symlink.'
staged="$(mktemp "${install_dir}/.threadforge.XXXXXXXX")" || fail "install directory is not writable: ${install_dir}."
install -m 0755 -- "${tmp}/threadforge" "$staged" || fail 'could not stage the CLI binary.'
"$staged" version >/dev/null || fail 'the downloaded CLI failed its version check.'
mv -f -- "$staged" "${install_dir}/threadforge" || fail 'could not atomically install the CLI binary.'
staged=""

printf 'Installed Threadforge CLI to %s/threadforge\n' "$install_dir"
"${install_dir}/threadforge" version
case ":${PATH:-}:" in
  *":${install_dir}:"*) ;;
  *) printf 'Add this directory to PATH if it is not already present: %s\n' "$install_dir" ;;
esac
