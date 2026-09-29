#!/usr/bin/env bash
set -euo pipefail
hub='' download_url='' token='' run_user="${SUDO_USER:-}" ca='' read_only=0 check_downloads=0
while (($#)); do
  case "$1" in
    --hub|--download-url|--token|--run-user|--ca)
      (($# >= 2)) || { echo "Missing value for $1" >&2; exit 2; }
      case "$1" in
        --hub) hub="$2";; --download-url) download_url="$2";; --token) token="$2";;
        --run-user) run_user="$2";; --ca) ca="$2";;
      esac
      shift 2;;
    --read-only) read_only=1; shift;;
    --check-downloads) check_downloads=1; shift;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2;;
  esac
done
hub="${hub%/}"; download_url="${download_url:-$hub}"; download_url="${download_url%/}"
origin_pattern='^https://(\[[0-9a-fA-F:]+\]|[A-Za-z0-9.-]+)(:[0-9]{1,5})?$'
[[ "$hub" =~ $origin_pattern && "$download_url" =~ $origin_pattern ]] || {
  echo 'Use --hub https://DOMAIN_OR_IP[:PORT]; optional --download-url must also be an HTTPS origin.' >&2; exit 2;
}
if [[ -z "$run_user" ]]; then run_user="$(id -un)"; fi
if (( ! check_downloads )); then
  [[ "$run_user" =~ ^[a-zA-Z0-9_.-]+$ && "$(id -u "$run_user")" != 0 ]] || {
    echo 'Specify --run-user with an existing normal project account.' >&2; exit 2;
  }
fi
case "$(uname -s)/$(uname -m)" in
  Linux/x86_64|Linux/amd64) target=linux-amd64;;
  Linux/aarch64|Linux/arm64) target=linux-arm64;;
  Darwin/arm64) target=darwin-arm64;;
  *) echo 'Supported: Linux x86_64/ARM64 and Apple Silicon macOS.' >&2; exit 2;;
esac
command -v curl >/dev/null || { echo 'curl is required.' >&2; exit 1; }
if command -v sha256sum >/dev/null; then
  hash_command=(sha256sum)
elif command -v shasum >/dev/null; then
  hash_command=(shasum -a 256)
else echo 'sha256sum or shasum is required.' >&2; exit 1; fi
fetch_args=(--fail --silent --show-error --location --max-redirs 3 --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 10 --max-time 180 --retry 2)
if [[ -n "$ca" ]]; then
  [[ -r "$ca" ]] || { echo 'The specified CA file is not readable.' >&2; exit 1; }
  ca="$(cd "$(dirname "$ca")" && pwd)/$(basename "$ca")"
  fetch_args+=(--cacert "$ca")
fi
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
binary="homefleet-agent-$target"
for file in SHA256SUMS install-unix.sh "$binary"; do
  curl "${fetch_args[@]}" "$download_url/downloads/$file" --output "$stage/$file"
done
for file in install-unix.sh "$binary"; do
  expected="$(awk -v file="$file" '$2 == file {print $1}' "$stage/SHA256SUMS")"
  [[ "$expected" =~ ^[0-9a-fA-F]{64}$ ]] || { echo "Missing or ambiguous SHA-256 for $file" >&2; exit 1; }
  actual="$("${hash_command[@]}" "$stage/$file" | awk '{print $1}')"
  [[ "$actual" = "$expected" ]] || { echo "SHA-256 mismatch: $file; installation stopped." >&2; exit 1; }
done
echo "Verified installer and $binary from $download_url"
if ((check_downloads)); then echo 'Download check passed; no installation or enrollment performed.'; exit 0; fi
args=(--binary "$stage/$binary" --hub "$hub" --run-user "$run_user")
if [[ -n "$token" ]]; then args+=(--token "$token"); fi
if [[ -n "$ca" ]]; then args+=(--ca "$ca"); fi
if ((read_only)); then args+=(--read-only); fi
if [[ "$(id -u)" = 0 ]]; then
  bash "$stage/install-unix.sh" "${args[@]}"
else
  sudo bash "$stage/install-unix.sh" "${args[@]}"
fi
