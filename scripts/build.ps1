param([string]$Version = '0.1.0')
$ErrorActionPreference = 'Stop'
$priorOS = $env:GOOS
$priorArch = $env:GOARCH
$priorCGO = $env:CGO_ENABLED
New-Item -ItemType Directory -Force -Path dist | Out-Null
try {
    $env:CGO_ENABLED = '0'
    foreach ($target in @('linux/amd64','linux/arm64','darwin/amd64','darwin/arm64','windows/amd64')) {
        $parts = $target.Split('/')
        $env:GOOS = $parts[0]
        $env:GOARCH = $parts[1]
        $suffix = if ($env:GOOS -eq 'windows') { '.exe' } else { '' }
        $output = "dist/coldflow-$($parts[0])-$($parts[1])$suffix"
        Write-Output "Building $target"
        go build -trimpath -ldflags "-s -w -X main.version=$Version" -o $output ./cmd/coldflow
        if ($LASTEXITCODE -ne 0) { throw "Build failed: $target" }
    }
    $checksums = Get-ChildItem -LiteralPath dist -File -Filter 'coldflow-*' | ForEach-Object {
        $hash = Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256
        "$($hash.Hash.ToLower())  $($_.Name)"
    }
    [System.IO.File]::WriteAllLines((Join-Path (Get-Location).Path 'dist/SHA256SUMS'), [string[]]$checksums, [System.Text.UTF8Encoding]::new($false))
} finally {
    $env:GOOS = $priorOS
    $env:GOARCH = $priorArch
    $env:CGO_ENABLED = $priorCGO
}
