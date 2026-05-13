$hostsPath = "C:\Windows\System32\drivers\etc\hosts"
$entry = "`r`n# StackKits integration test domains`r`n127.0.0.1 auth.test.local id.test.local dash.test.local whoami.test.local logs.test.local kuma.test.local dokploy.test.local"
$content = Get-Content $hostsPath -Raw
if ($content -notmatch "test\.local") {
    Add-Content -Path $hostsPath -Value $entry
    Write-Host "Added test.local entries to hosts file"
} else {
    Write-Host "test.local entries already exist"
}
