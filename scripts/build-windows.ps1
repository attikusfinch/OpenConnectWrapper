$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..')
$dist = Join-Path $root 'dist'
$bundleSrc = Join-Path $root 'third_party\openconnect\windows-amd64'
$bundleDst = Join-Path $dist 'openconnect\windows-amd64'

New-Item -ItemType Directory -Force -Path $dist | Out-Null
Push-Location $root
try {
  & (Join-Path $root 'scripts\generate-icon.ps1') | Out-Host
  windres -O coff -F pe-x86-64 -i (Join-Path $root 'assets\app-icon.rc') -o (Join-Path $root 'cmd\openconnectmulti\app.syso')
  go build -ldflags "-H=windowsgui" -o (Join-Path $dist 'openconnectmulti.exe') .\cmd\openconnectmulti
} finally {
  Pop-Location
}

if (-not (Test-Path (Join-Path $bundleSrc 'openconnect.exe'))) {
  throw "Bundled openconnect.exe not found at $bundleSrc"
}

if (Test-Path $bundleDst) {
  $resolved = Resolve-Path $bundleDst
  if (-not $resolved.Path.StartsWith($dist, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing to remove unexpected path: $($resolved.Path)"
  }
  Remove-Item -LiteralPath $resolved.Path -Recurse -Force
}

New-Item -ItemType Directory -Force -Path $bundleDst | Out-Null
Copy-Item -Path (Join-Path $bundleSrc '*') -Destination $bundleDst -Recurse -Force

Write-Host "Built $dist"
