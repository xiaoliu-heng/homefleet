#Requires -RunAsAdministrator
$ErrorActionPreference = 'Stop'
Stop-Service HomeFleetAgent -ErrorAction SilentlyContinue
Stop-ScheduledTask -TaskName HomeFleetUserWorker -ErrorAction SilentlyContinue
Unregister-ScheduledTask -TaskName HomeFleetUserWorker -Confirm:$false -ErrorAction SilentlyContinue
& sc.exe delete HomeFleetAgent | Out-Null
Remove-Item -Force (Join-Path $env:ProgramFiles 'HomeFleet/homefleet-agent.exe') -ErrorAction SilentlyContinue
Write-Host 'Service and binary removed; configuration and execution journals are retained.'
Write-Host 'Revoke this device in the console. Verify any in-flight installer before retrying its task.'
