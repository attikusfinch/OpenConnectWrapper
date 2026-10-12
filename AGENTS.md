# OpenConnect Multi: VPN control for agents

When the user asks to connect, switch or disconnect VPN / ВПН ("переключи VPN", "свитчни VPN"), use MCP server **openconnect-vpn**. Discover `vpn_status`, `vpn_list_profiles`, `vpn_switch_profile`, `vpn_disconnect`, `vpn_open_app`.

1. Call `vpn_status`. If offline, call `vpn_open_app`.
2. If locked, ask the user to enter their PIN in the app window. Never ask for the PIN in chat or read the vault/device key.
3. Call `vpn_list_profiles`. Match an exact ID or unique name. Ask which profile if ambiguous; do not guess.
4. Call `vpn_switch_profile` with `{"profile":"exact ID or name"}` only on user request. It replaces the current VPN. For an explicit disconnect request, use `vpn_disconnect`.
5. Check `vpn_status`. Report connecting as connecting; a running process does not prove tunnel reachability. Switching VPN can interrupt this task's network.

If tools are not installed, run `scripts/install-mcp.ps1`, then reload the MCP client. The stdio binary is `dist/openconnectmulti-mcp.exe`; it must be next to `openconnectmulti.exe`. See `docs/MCP.md`.

Build and validation: `go test ./...`, `go vet ./...`, `scripts/build-windows.ps1`. Do not exercise a real VPN in automated tests; use a temporary vault and a fake VPN process.
