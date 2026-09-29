#Requires -RunAsAdministrator
param(
  [Parameter(Mandatory=$true)][string]$Binary,
  [Parameter(Mandatory=$true)][string]$Hub,
  [Parameter(Mandatory=$true)][string]$RunUser,
  [string]$Token,
  [string]$CA,
  [switch]$ReadOnly
)
$ErrorActionPreference = 'Stop'
if (-not $Hub.StartsWith('https://')) { throw 'A HTTPS hub URL is required.' }
$current = [System.Security.Principal.WindowsIdentity]::GetCurrent()
$targetAccount = New-Object System.Security.Principal.NTAccount($RunUser)
$targetSID = $targetAccount.Translate([System.Security.Principal.SecurityIdentifier])
if ($targetSID.Value -ne $current.User.Value) { throw 'Run this installer elevated as the same user that will run project tasks.' }
$root = Join-Path $env:ProgramData 'HomeFleet'
$binDir = Join-Path $env:ProgramFiles 'HomeFleet'
$userDir = Join-Path $env:LOCALAPPDATA 'HomeFleet'
New-Item -ItemType Directory -Force -Path $root,$binDir,$userDir | Out-Null
& icacls.exe $root /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'Could not protect the system directory ACL.' }
$exe = Join-Path $binDir 'homefleet-agent.exe'
$service = Get-Service HomeFleetAgent -ErrorAction SilentlyContinue
if ($service) { Stop-Service HomeFleetAgent }
Stop-ScheduledTask -TaskName HomeFleetUserWorker -ErrorAction SilentlyContinue
Copy-Item -Force $Binary $exe
$workerToken = Join-Path $userDir 'worker.token'
if (-not (Test-Path $workerToken)) {
  $bytes = New-Object byte[] 32
  $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
  $rng.GetBytes($bytes); $rng.Dispose()
  [System.IO.File]::WriteAllText($workerToken, [Convert]::ToBase64String($bytes))
}
& icacls.exe $userDir /inheritance:r /grant:r ('*'+$targetSID.Value + ':(OI)(CI)F') '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'Could not protect the user executor credential.' }
$config = Join-Path $root 'agent.json'
$caArgs = @()
if ($CA) { Copy-Item -Force $CA (Join-Path $root 'ca.crt'); $caArgs = @('--ca',(Join-Path $root 'ca.crt')) }
if (-not (Test-Path $config)) {
  if (-not $Token) { throw 'A one-time token is required for first installation.' }
  $arguments = @('enroll','--hub',$Hub,'--token',$Token,'--run-user',$RunUser,'--config',$config,'--data',(Join-Path $root 'state'),'--worker-token-file',$workerToken) + $caArgs
  if ($ReadOnly) { $arguments += '--read-only' }
  & $exe @arguments
  if ($LASTEXITCODE -ne 0) { throw 'Agent enrollment failed.' }
}
& $exe mark-service --config $config
if ($LASTEXITCODE -ne 0) { throw 'Agent service registration failed.' }
if (-not $service) {
  New-Service -Name HomeFleetAgent -DisplayName 'HomeFleet Agent' -BinaryPathName ('"'+$exe+'" run --config "'+$config+'"') -StartupType Automatic | Out-Null
}
& sc.exe failure HomeFleetAgent reset= 86400 actions= restart/10000/restart/30000/restart/60000 | Out-Null
$action = New-ScheduledTaskAction -Execute $exe -Argument ('user-worker --worker-token-file "'+$workerToken+'"')
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $RunUser
$principal = New-ScheduledTaskPrincipal -UserId $RunUser -LogonType Interactive -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
Register-ScheduledTask -TaskName HomeFleetUserWorker -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
Start-ScheduledTask -TaskName HomeFleetUserWorker
Start-Service HomeFleetAgent
Write-Host 'HomeFleet installed. Machine-wide WinGet operations require Microsoft.WinGet.Client (see documentation).'
