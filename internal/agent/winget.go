package agent

// The PowerShell module calls machine scope "System"; the CLI calls it "machine".
// Selecting installed packages first avoids reinstalling already-present software.
func systemWinGetScript(operation, name string) string {
	base := "$ErrorActionPreference='Stop'; Import-Module Microsoft.WinGet.Client; "
	check := "function Assert-Result($r) { if($null -eq $r){throw 'WinGet returned no result'}; $r | ConvertTo-Json -Depth 6 -Compress; if($r.Status.ToString() -notin @('Ok','NoApplicableUpgrade')){throw ('WinGet: '+$r.Status)} }; "
	if operation == "upgrade_all" {
		return base + check + "$packages=@(Get-WinGetPackage | Where-Object {$_.IsUpdateAvailable}); foreach($p in $packages){$r=Update-WinGetPackage -PSCatalogPackage $p -Mode Silent -Scope System; Assert-Result $r}; if($packages.Count -eq 0){Write-Output '没有可用更新'}"
	}
	lookup := "$p=@(Get-WinGetPackage -Id '" + name + "' -MatchOption Equals); "
	if operation == "install" {
		return base + check + lookup + "if($p.Count -gt 0){Write-Output '已安装，无需变更'}else{$r=Install-WinGetPackage -Id '" + name + "' -MatchOption Equals -Source winget -Mode Silent -Scope System; Assert-Result $r}"
	}
	return base + check + lookup + "if($p.Count -eq 0){throw '软件尚未安装'}; $updates=@($p | Where-Object {$_.IsUpdateAvailable}); foreach($item in $updates){$r=Update-WinGetPackage -PSCatalogPackage $item -Mode Silent -Scope System; Assert-Result $r}; if($updates.Count -eq 0){Write-Output '已是最新版本，无需变更'}"
}
