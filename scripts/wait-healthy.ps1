param(
    [int]$TimeoutSeconds = 60
)

$deadline = (Get-Date).AddSeconds($TimeoutSeconds)
while ((Get-Date) -lt $deadline) {
    $raw = podman compose ps --format json 2>$null
    if ($LASTEXITCODE -eq 0 -and $raw) {
        try { $services = $raw | ConvertFrom-Json } catch { $services = @() }
        if ($services -isnot [array]) { $services = @($services) }
        if ($services.Count -ge 2) {
            $healthy = $services | Where-Object { $_.Health -eq 'healthy' }
            if ($healthy.Count -eq $services.Count) {
                Write-Host 'all services healthy'
                exit 0
            }
        }
    }
    Start-Sleep -Seconds 2
}
Write-Error 'timed out waiting for compose services to become healthy'
exit 1
