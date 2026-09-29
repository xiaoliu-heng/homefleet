export function installationOrigin(value: string): string | null {
  try {
    const url = new URL(value.trim());
    if (url.protocol !== "https:" || url.username || url.password || url.pathname !== "/" || url.search || url.hash) return null;
    if (!/^(\[[0-9a-f:]+\]|[a-z0-9.-]+)$/i.test(url.hostname)) return null;
    return url.origin;
  } catch {
    return null;
  }
}

export function installationCommand(options: {
  windows: boolean;
  address: string;
  token: string;
  username: string;
  caFile?: string;
}): string {
  const origin = installationOrigin(options.address);
  if (!origin) return "";
  const quote = (value: string) => "'" + value.replaceAll("'", options.windows ? "''" : "'\"'\"'") + "'";
  if (options.windows) {
    return [
      "& {",
      "  $ErrorActionPreference = 'Stop'",
      `  $HF_URL = ${quote(origin)}`,
      `  $HF_TOKEN = ${quote(options.token)}`,
      ...(options.caFile ? [`  $HF_CA = ${quote(options.caFile)}`] : []),
      "  $HF_TMP = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString('N'))",
      "  New-Item -ItemType Directory -Path $HF_TMP | Out-Null",
      "  try {",
      '    & curl.exe --fail --silent --show-error --location --proto "=https" --proto-redir "=https" --tlsv1.2 --connect-timeout 10 --max-time 180' + (options.caFile ? ' --cacert $HF_CA' : '') + ' "$HF_URL/install.ps1" --output "$HF_TMP/install.ps1"',
      "    if ($LASTEXITCODE -ne 0) { throw 'HomeFleet installer download failed.' }",
      '    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File "$HF_TMP/install.ps1" -Hub $HF_URL -Token $HF_TOKEN' + (options.username.trim() ? " -RunUser " + quote(options.username.trim()) : "") + (options.caFile ? " -CA $HF_CA" : ""),
      "    if ($LASTEXITCODE -ne 0) { throw 'HomeFleet installation failed.' }",
      "  } finally { Remove-Item -LiteralPath $HF_TMP -Recurse -Force }",
      "}",
    ].join("\n");
  }
  return [
    "(",
    "  set -e",
    `  HF_URL=${quote(origin)}`,
    `  HF_TOKEN=${quote(options.token)}`,
    ...(options.caFile ? [`  HF_CA=${quote(options.caFile)}`] : []),
    '  HF_TMP="$(mktemp -d)"',
    '  trap \'rm -rf "$HF_TMP"\' EXIT',
    '  curl --fail --silent --show-error --location --proto "=https" --proto-redir "=https" --tlsv1.2 --connect-timeout 10 --max-time 180' + (options.caFile ? ' --cacert "$HF_CA"' : '') + ' "$HF_URL/install.sh" --output "$HF_TMP/install.sh"',
    '  bash "$HF_TMP/install.sh" --hub "$HF_URL" --token "$HF_TOKEN"' + (options.username.trim() ? " --run-user " + quote(options.username.trim()) : "") + (options.caFile ? ' --ca "$HF_CA"' : ""),
    ")",
  ].join("\n");
}
