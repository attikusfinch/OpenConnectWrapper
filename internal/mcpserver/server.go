// Package mcpserver implements the newline-delimited MCP stdio transport.
// It deliberately exposes only existing VPN profiles, never credentials or shell commands.
package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"openconnectmulti/internal/agentbridge"
)

const Instructions = "Use this OpenConnect Multi server whenever the user asks to connect, switch or disconnect a VPN (VPN, ВПН, переключи VPN, свитчни VPN). Start with vpn_status. If offline call vpn_open_app. If locked, ask the user to unlock the app window, never request their PIN in chat. Call vpn_list_profiles and select an exact ID or unique name; ask when ambiguous. vpn_switch_profile replaces the current VPN and can interrupt network access. Change VPN only when requested by the user. Call vpn_status after switching; process-running is not proof of tunnel reachability. Never read vault files, passwords or device.key."

type Backend interface {
	Call(context.Context, agentbridge.Request) (map[string]any, error)
	Open(context.Context) (map[string]any, error)
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func tools() []map[string]any {
	result := []map[string]any{}
	for _, spec := range []struct {
		name, description string
		readOnly          bool
	}{
		{"vpn_status", "Check OpenConnect Multi VPN status, app availability and whether the vault needs unlocking. Статус VPN / ВПН.", true},
		{"vpn_list_profiles", "List saved VPN profile IDs, names, servers and protocols, without passwords. Найти VPN-профиль.", true},
		{"vpn_switch_profile", "Connect or switch VPN to an exact profile ID or unique name. Replaces the current connection. Переключить / подключить VPN. Repeating the current profile does not reconnect.", false},
		{"vpn_disconnect", "Disconnect the current VPN. Отключить VPN. Only when requested by the user.", false},
		{"vpn_open_app", "Open OpenConnect Multi so the user can enter the PIN locally. Start the desktop app if offline; reuse it when running. No PIN or password arguments.", false},
	} {
		schema := map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
		if spec.name == "vpn_switch_profile" {
			schema["properties"] = map[string]any{"profile": map[string]any{"type": "string", "minLength": 1, "description": "Exact profile ID or unique name from vpn_list_profiles"}}
			schema["required"] = []string{"profile"}
		}
		result = append(result, map[string]any{"name": spec.name, "description": spec.description, "inputSchema": schema,
			"annotations": map[string]any{"readOnlyHint": spec.readOnly, "destructiveHint": spec.name == "vpn_switch_profile" || spec.name == "vpn_disconnect", "idempotentHint": true, "openWorldHint": false}})
	}
	return result
}

func Serve(ctx context.Context, input io.Reader, output io.Writer, backend Backend) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	encoder := json.NewEncoder(output)
	initialized := false
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var req request
		var result any
		var rpcError any
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			rpcError = map[string]any{"code": -32700, "message": "Parse error"}
		} else if req.JSONRPC != "2.0" || req.Method == "" {
			rpcError = map[string]any{"code": -32600, "message": "Invalid Request"}
		} else if len(req.ID) == 0 {
			// Notifications never receive responses (including initialized/cancelled).
			continue
		} else {
			switch req.Method {
			case "initialize":
				var params struct {
					ProtocolVersion string `json:"protocolVersion"`
				}
				if json.Unmarshal(req.Params, &params) != nil || params.ProtocolVersion == "" {
					rpcError = map[string]any{"code": -32602, "message": "protocolVersion is required"}
					break
				}
				version := params.ProtocolVersion
				if version != "2024-11-05" && version != "2025-03-26" && version != "2025-06-18" {
					version = "2025-06-18"
				}
				initialized = true
				result = map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "openconnect-vpn", "version": "1.0.0"}, "instructions": Instructions}
			case "ping":
				result = map[string]any{}
			case "tools/list", "tools/call":
				if !initialized {
					rpcError = map[string]any{"code": -32000, "message": "Initialize first"}
					break
				}
				if req.Method == "tools/list" {
					result = map[string]any{"tools": tools()}
					break
				}
				var params struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				}
				if json.Unmarshal(req.Params, &params) != nil || params.Name == "" {
					rpcError = map[string]any{"code": -32602, "message": "Invalid tool parameters"}
					break
				}
				known := false
				for _, tool := range tools() {
					if tool["name"] == params.Name {
						known = true
					}
				}
				if !known {
					rpcError = map[string]any{"code": -32602, "message": "Unknown tool"}
					break
				}
				data, err := callTool(ctx, backend, params.Name, params.Arguments)
				text := ""
				if err != nil {
					text = err.Error()
				} else {
					b, _ := json.Marshal(data)
					text = string(b)
				}
				result = map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": err != nil}
			default:
				rpcError = map[string]any{"code": -32601, "message": "Method not found"}
			}
		}
		id := req.ID
		if len(id) == 0 {
			id = json.RawMessage("null")
		}
		response := map[string]any{"jsonrpc": "2.0", "id": id}
		if rpcError != nil {
			response["error"] = rpcError
		} else {
			response["result"] = result
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func callTool(ctx context.Context, backend Backend, name string, args json.RawMessage) (map[string]any, error) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(args, &values) != nil || values == nil {
		return nil, errors.New("arguments must be an object")
	}
	var profile string
	for key, value := range values {
		if name != "vpn_switch_profile" || key != "profile" {
			return nil, errors.New("unexpected argument: " + key)
		}
		if bytes.Equal(value, []byte("null")) || json.Unmarshal(value, &profile) != nil {
			return nil, errors.New("profile must be a string")
		}
	}
	if name == "vpn_switch_profile" && len(bytes.TrimSpace([]byte(profile))) == 0 {
		return nil, errors.New("profile is required; call vpn_list_profiles first")
	}
	if name == "vpn_open_app" {
		return backend.Open(ctx)
	}
	action := map[string]string{"vpn_status": "status", "vpn_list_profiles": "profiles", "vpn_switch_profile": "switch", "vpn_disconnect": "disconnect"}[name]
	return backend.Call(ctx, agentbridge.Request{Action: action, Profile: profile})
}
