param(
  [Parameter(Mandatory=$true)][string]$Hub,
  [string]$DownloadUrl,
  [string]$Token,
  [string]$RunUser,
  [string]$CA,
  [switch]$ReadOnly,
  [switch]$CheckDownloads
)
$ErrorActionPreference = 'Stop'
$Hub = $Hub.TrimEnd('/')
if (-not $DownloadUrl) { $DownloadUrl = $Hub }
$DownloadUrl = $DownloadUrl.TrimEnd('/')
foreach ($address in @($Hub, $DownloadUrl)) {
  $uri = [Uri]$address
  if (-not $uri.IsAbsoluteUri -or $uri.Scheme -ne 'https' -or -not $uri.Host -or $uri.UserInfo -or $uri.Query -or $uri.Fragment -or $uri.AbsolutePath -ne '/') {
    throw 'Hub and DownloadUrl must be HTTPS origins: https://DOMAIN_OR_IP[:PORT].'
  }
}
if (-not $CheckDownloads) {
  $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = New-Object Security.Principal.WindowsPrincipal($identity)
  if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Open PowerShell as administrator using your project account.' }
  if (-not $RunUser) { $RunUser = $identity.Name }
}
$arch = $env:PROCESSOR_ARCHITEW6432
if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
if ($arch -ne 'AMD64') { throw 'This Windows release requires an x86_64 system.' }
$curl = (Get-Command curl.exe -ErrorAction Stop).Source
$fetchArgs = @('--fail','--silent','--show-error','--location','--max-redirs','3','--proto','=https','--proto-redir','=https','--tlsv1.2','--connect-timeout','10','--max-time','180','--retry','2')
if ($CA) {
  $CA = (Resolve-Path -LiteralPath $CA -ErrorAction Stop).Path
  $fetchArgs += @('--cacert', $CA)
}
$stage = Join-Path ([IO.Path]::GetTempPath()) ('homefleet-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $stage | Out-Null
try {
  $binary = 'homefleet-agent-windows-amd64.exe'
  foreach ($file in @('SHA256SUMS','install-windows.ps1',$binary)) {
    & $curl @fetchArgs "$DownloadUrl/downloads/$file" --output (Join-Path $stage $file)
    if ($LASTEXITCODE -ne 0) { throw "Download failed: $file" }
  }
  $manifest = Get-Content -LiteralPath (Join-Path $stage 'SHA256SUMS')
  foreach ($file in @('install-windows.ps1',$binary)) {
    $pattern = '^([0-9a-fA-F]{64})\s+' + [Regex]::Escape($file) + '$'
    $entries = @($manifest | Where-Object { $_ -match $pattern })
    if ($entries.Count -ne 1) { throw "Missing or ambiguous SHA-256 for $file" }
    $expected = [Regex]::Match($entries[0],$pattern).Groups[1].Value
    $actual = (Get-FileHash -LiteralPath (Join-Path $stage $file) -Algorithm SHA256).Hash
    if ($actual -ne $expected) { throw "SHA-256 mismatch: $file; installation stopped." }
  }
  Write-Host "Verified installer and $binary from $DownloadUrl"
  if ($CheckDownloads) { Write-Host 'Download check passed; no installation or enrollment performed.'; return }
  $installArgs = @{ Binary=(Join-Path $stage $binary); Hub=$Hub; RunUser=$RunUser }
  if ($Token) { $installArgs.Token=$Token }
  if ($CA) { $installArgs.CA=$CA }
  if ($ReadOnly) { $installArgs.ReadOnly=$true }
  & (Join-Path $stage 'install-windows.ps1') @installArgs
} finally {
  Remove-Item -LiteralPath $stage -Recurse -Force
}
