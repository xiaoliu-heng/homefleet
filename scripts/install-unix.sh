#!/usr/bin/env bash
set -euo pipefail
hub='' token='' run_user='' binary='' ca='' readonly_arg=()
while (($#)); do
  case "$1" in
    --hub) hub="$2"; shift 2;;
    --token) token="$2"; shift 2;;
    --run-user) run_user="$2"; shift 2;;
    --binary) binary="$2"; shift 2;;
    --ca) ca="$2"; shift 2;;
    --read-only) readonly_arg=(--read-only); shift;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2;;
  esac
done
[[ "$(id -u)" = 0 ]] || { echo 'Run this installer with sudo.' >&2; exit 1; }
[[ -n "$binary" && -f "$binary" && -n "$hub" && -n "$run_user" ]] || { echo 'Required: --binary PATH --hub HTTPS_URL --run-user USER [--token TOKEN] [--ca PATH]' >&2; exit 2; }
[[ "$run_user" =~ ^[a-zA-Z0-9_.-]+$ ]] || { echo 'Invalid user.' >&2; exit 2; }
[[ "$(id -u "$run_user")" != 0 ]] || { echo 'Project account must be a normal user.' >&2; exit 1; }
[[ "$hub" = https://* ]] || { echo 'A HTTPS hub URL is required.' >&2; exit 1; }
os="$(uname -s)"
if [[ "$os" = Darwin ]]; then
  config_dir='/Library/Application Support/HomeFleet'
  bin_dir='/usr/local/libexec/homefleet'
  data_dir='/Library/Application Support/HomeFleet/state'
elif [[ "$os" = Linux ]]; then
  config_dir='/etc/homefleet'
  bin_dir='/usr/local/libexec/homefleet'
  data_dir='/var/lib/homefleet'
else echo 'Only Linux and macOS are supported.' >&2; exit 1; fi
install -d -m 700 "$config_dir" "$data_dir"
install -d -m 755 "$bin_dir"
if [[ "$os" = Darwin ]]; then
  launchctl bootout system/com.homefleet.agent 2>/dev/null || true
  for ((attempt=0; attempt<30; attempt++)); do
    if ! launchctl print system/com.homefleet.agent >/dev/null 2>&1; then break; fi
    sleep 1
  done
  if launchctl print system/com.homefleet.agent >/dev/null 2>&1; then echo 'Agent has not stopped; installation aborted.' >&2; exit 1; fi
else
  if systemctl cat homefleet-agent.service >/dev/null 2>&1; then systemctl stop homefleet-agent.service; fi
fi
stage="$(mktemp "$bin_dir/.homefleet-install-XXXXXX")"
trap 'rm -f "$stage"' EXIT
install -m 755 "$binary" "$stage"
mv -f "$stage" "$bin_dir/homefleet-agent"
ca_arg=()
if [[ -n "$ca" ]]; then
  install -m 644 "$ca" "$config_dir/ca.crt"
  ca_arg=(--ca "$config_dir/ca.crt")
fi
if [[ ! -f "$config_dir/agent.json" ]]; then
  [[ -n "$token" ]] || { echo 'A one-time token is required for first installation.' >&2; exit 1; }
  enroll_args=(enroll --hub "$hub" --token "$token" --run-user "$run_user" --config "$config_dir/agent.json" --data "$data_dir")
  if [[ -n "$ca" ]]; then enroll_args+=(--ca "$config_dir/ca.crt"); fi
  if ((${#readonly_arg[@]})); then enroll_args+=(--read-only); fi
  "$bin_dir/homefleet-agent" "${enroll_args[@]}"
else
  echo 'Preserving the registered identity, credentials, and existing configuration.'
fi
"$bin_dir/homefleet-agent" mark-service --config "$config_dir/agent.json"
if [[ "$os" = Darwin ]]; then
  cat > /Library/LaunchDaemons/com.homefleet.agent.plist <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.homefleet.agent</string>
<key>ProgramArguments</key><array><string>/usr/local/libexec/homefleet/homefleet-agent</string><string>run</string><string>--config</string><string>/Library/Application Support/HomeFleet/agent.json</string></array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>StandardOutPath</key><string>/Library/Application Support/HomeFleet/agent.log</string>
<key>StandardErrorPath</key><string>/Library/Application Support/HomeFleet/agent.log</string>
</dict></plist>
PLIST
  chmod 644 /Library/LaunchDaemons/com.homefleet.agent.plist
  chown root:wheel /Library/LaunchDaemons/com.homefleet.agent.plist
  launchctl bootstrap system /Library/LaunchDaemons/com.homefleet.agent.plist
else
  cat > /etc/systemd/system/homefleet-agent.service <<'UNIT'
[Unit]
Description=HomeFleet device agent
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
ExecStart=/usr/local/libexec/homefleet/homefleet-agent run --config /etc/homefleet/agent.json
Restart=on-failure
RestartSec=10
UMask=0077
# Do not kill an in-flight package manager on agent restart.
KillMode=process
[Install]
WantedBy=multi-user.target
UNIT
  systemctl daemon-reload
  systemctl enable --now homefleet-agent
fi
echo 'HomeFleet Agent installed. Check device capabilities in the console.'
