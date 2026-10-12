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
  if ($LASTEXITCODE -ne 0) { throw 'Windows resource compilation failed' }
  go build -ldflags "-H=windowsgui" -o (Join-Path $dist 'openconnectmulti.exe') .\cmd\openconnectmulti
  if ($LASTEXITCODE -ne 0) { throw 'Application build failed' }
  # stdio MCP must be a console binary without the GUI/admin manifest.
  go build -o (Join-Path $dist 'openconnectmulti-mcp.exe') .\cmd\openconnectmulti-mcp
  if ($LASTEXITCODE -ne 0) { throw 'MCP build failed' }
} finally {
  Pop-Location
}

if (-not (Test-Path (Join-Path $bundleSrc 'openconnect.exe'))) {
  throw "Bundled openconnect.exe not found at $bundleSrc"
}

New-Item -ItemType Directory -Force -Path $bundleDst | Out-Null
# Do not delete the installed runtime before copying: a locked DLL could leave
# an otherwise working installation incomplete. Skip unchanged bundled files.
Get-ChildItem -LiteralPath $bundleSrc -File | ForEach-Object {
  $destinationFile = Join-Path $bundleDst $_.Name
  if ((Test-Path -LiteralPath $destinationFile) -and
      ((Get-FileHash -LiteralPath $_.FullName).Hash -eq (Get-FileHash -LiteralPath $destinationFile).Hash)) {
    return
  }
  Copy-Item -LiteralPath $_.FullName -Destination $destinationFile -Force
}

Write-Host "Built $dist"
