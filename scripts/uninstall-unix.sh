#!/usr/bin/env bash
set -euo pipefail
[[ "$(id -u)" = 0 ]] || { echo 'Run with sudo.' >&2; exit 1; }
if [[ "$(uname -s)" = Darwin ]]; then
  launchctl bootout system/com.homefleet.agent 2>/dev/null || true
  rm -f /Library/LaunchDaemons/com.homefleet.agent.plist
else
  systemctl disable --now homefleet-agent 2>/dev/null || true
  rm -f /etc/systemd/system/homefleet-agent.service
  systemctl daemon-reload
fi
rm -f /usr/local/libexec/homefleet/homefleet-agent
echo 'Agent service and binary removed. Configuration and execution journals are retained.'
echo 'Revoke the device credential in the console. Running package transactions are not forcibly killed.'
