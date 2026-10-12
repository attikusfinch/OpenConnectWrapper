param([string]$ConfigDir = (Join-Path $env:APPDATA 'OpenConnectMulti'))
$ErrorActionPreference = 'Stop'
$projectRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$mcpBinary = Join-Path $projectRoot 'dist\openconnectmulti-mcp.exe'
if (-not (Test-Path -LiteralPath $mcpBinary)) { throw 'Build first: scripts/build-windows.ps1' }
& codex mcp add openconnect-vpn -- $mcpBinary --config-dir $ConfigDir
if ($LASTEXITCODE -ne 0) { throw 'MCP registration failed' }
$codexDirectory = if ($env:CODEX_HOME) { $env:CODEX_HOME } else { Join-Path $env:USERPROFILE '.codex' }
$instructionsPath = Join-Path $codexDirectory 'AGENTS.md'
$marker = '<!-- openconnect-vpn -->'
$existing = if (Test-Path -LiteralPath $instructionsPath) { [IO.File]::ReadAllText($instructionsPath) } else { '' }
if (-not $existing.Contains($marker)) {
  $instructions = @'

<!-- openconnect-vpn -->
## Local VPN: OpenConnect Multi
When the user asks to connect, switch or disconnect VPN / ВПН ("переключи VPN", "свитчни VPN"), discover tools from MCP server `openconnect-vpn`: `vpn_status`, `vpn_open_app`, `vpn_list_profiles`, `vpn_switch_profile`, `vpn_disconnect`.
Check status first; open the app if offline. If locked, ask the user to enter the PIN in the app window, never in chat. List profiles and use an exact ID or unique name; ask if ambiguous. Change the VPN only on user request. Check status afterward; a running process does not prove tunnel reachability. Do not read vault/device.key or extract credentials.
<!-- /openconnect-vpn -->
'@
  [IO.File]::AppendAllText($instructionsPath, "`n" + $instructions + "`n", [Text.UTF8Encoding]::new($false))
}
Write-Host 'Registered openconnect-vpn. Reload the MCP client, then ask it to switch VPN.'
