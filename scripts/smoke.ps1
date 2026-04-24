param([string]$Binary = '.\bin\coldflow.exe', [int]$ObserveSeconds = 15)
$ErrorActionPreference = 'Stop'
$binaryPath = (Resolve-Path -LiteralPath $Binary).Path
$tempBase = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
$smokeDir = Join-Path $tempBase ('coldflow-smoke-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $smokeDir | Out-Null
$service = $null
function Wait-Snapshot {
    for ($attempt = 0; $attempt -lt 40; $attempt++) {
        try { return Invoke-RestMethod -Uri $script:baseURL/api/snapshot -Headers $script:headers } catch { Start-Sleep -Milliseconds 250 }
    }
    throw 'Service did not start'
}
try {
    & $binaryPath --data $smokeDir init --demo --non-interactive
    if ($LASTEXITCODE -ne 0) { throw 'Init failed' }
    $configPath = Join-Path $smokeDir 'config.json'
    $settings = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    $probe = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    $probe.Start()
    $port = $probe.LocalEndpoint.Port
    $probe.Stop()
    $settings.listen = "127.0.0.1:$port"
    [System.IO.File]::WriteAllText($configPath, ($settings | ConvertTo-Json -Depth 10), [System.Text.UTF8Encoding]::new($false))
    $script:baseURL = "http://127.0.0.1:$port"
    $script:headers = @{ Authorization = 'Bearer ' + (Get-Content -LiteralPath (Join-Path $smokeDir 'api-token') -Raw).Trim() }
    $processArgs = @('--data', ('"' + $smokeDir + '"'), 'serve')
    $service = Start-Process -FilePath $binaryPath -ArgumentList $processArgs -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $smokeDir 'stdout.log') -RedirectStandardError (Join-Path $smokeDir 'stderr.log')
    $snapshot = Wait-Snapshot
    if ($snapshot.agents.Count -ne 8 -or $snapshot.drafts.Count -ne 3) { throw 'Unexpected demo fixture' }
    $draft = $snapshot.drafts[0]
    $body = @{kind='approve';id=$draft.id;revision=$draft.revision} | ConvertTo-Json
    Invoke-RestMethod -Method Post -Uri $script:baseURL/api/action -Headers $script:headers -ContentType application/json -Body $body | Out-Null
    Start-Sleep -Seconds 3
    $snapshot = Wait-Snapshot
    if (($snapshot.drafts | Where-Object id -eq $draft.id).status -ne 'sent') { throw 'Simulated send failed' }
    $body = @{kind='chat';agent='Scout';text='remember Prioritize efficient systems'} | ConvertTo-Json
    Invoke-RestMethod -Method Post -Uri $script:baseURL/api/action -Headers $script:headers -ContentType application/json -Body $body | Out-Null
    Start-Sleep -Seconds 3
    $snapshot = Wait-Snapshot
    if (-not ($snapshot.memories | Where-Object agent -eq 'Scout')) { throw 'Bot memory failed' }
    Stop-Process -Id $service.Id
    $service.WaitForExit()
    $service = Start-Process -FilePath $binaryPath -ArgumentList $processArgs -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $smokeDir 'restart-stdout.log') -RedirectStandardError (Join-Path $smokeDir 'restart-stderr.log')
    $snapshot = Wait-Snapshot
    if (($snapshot.drafts | Where-Object id -eq $draft.id).status -ne 'sent') { throw 'Delivery did not survive restart' }
    $peak = 0
    for ($second = 0; $second -lt $ObserveSeconds; $second++) {
        $service.Refresh()
        $peak = [Math]::Max($peak, $service.WorkingSet64)
        Start-Sleep -Seconds 1
    }
    "PASS: 8 bots, draft delivery, chat memory, restart persistence; observed Windows peak RSS $([Math]::Round($peak/1MB,1)) MiB"
} finally {
    if ($service -and -not $service.HasExited) { Stop-Process -Id $service.Id; $service.WaitForExit() }
    $resolved = [System.IO.Path]::GetFullPath($smokeDir)
    if (-not $resolved.StartsWith($tempBase, [System.StringComparison]::OrdinalIgnoreCase) -or (Split-Path -Leaf $resolved) -notlike 'coldflow-smoke-*') { throw 'Refusing cleanup outside smoke directory' }
    Remove-Item -LiteralPath $resolved -Recurse -Force
}
