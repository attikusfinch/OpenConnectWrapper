package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"openconnectmulti/internal/agentbridge"
	"strings"
	"testing"
)

type fakeBackend struct {
	requests []agentbridge.Request
	opens    int
}

func (f *fakeBackend) Call(_ context.Context, r agentbridge.Request) (map[string]any, error) {
	f.requests = append(f.requests, r)
	return map[string]any{"ok": true}, nil
}
func (f *fakeBackend) Open(context.Context) (map[string]any, error) {
	f.opens++
	return map[string]any{"ok": true}, nil
}

func TestProtocolAndArguments(t *testing.T) {
	b := &fakeBackend{}
	input := `not json
{"jsonrpc":"2.0","id":1,"method":"tools/list"}
{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"future"}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"vpn_switch_profile","arguments":{"profile":"Office","pin":"1234"}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"vpn_switch_profile","arguments":{"profile":null}}}
{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"vpn_switch_profile","arguments":{"profile":"Office"}}}
{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"vpn_open_app"}}
{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"shell","arguments":{}}}
{"jsonrpc":"2.0","id":8,"method":"ping"}
`
	var output bytes.Buffer
	if err := Serve(context.Background(), strings.NewReader(input), &output, b); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 9 {
		t.Fatalf("responses: %d", len(lines))
	}
	for _, line := range lines {
		var response map[string]any
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.requests) != 1 || b.requests[0].Profile != "Office" || b.requests[0].Action != "switch" || b.opens != 1 {
		t.Fatalf("invalid calls reached backend: %+v", b)
	}
	for i, expected := range map[int]string{0: `"code":-32700`, 1: `"code":-32000`, 2: `"protocolVersion":"2025-06-18"`, 3: `"isError":true`, 4: `"isError":true`, 7: `"code":-32602`} {
		if !strings.Contains(lines[i], expected) {
			t.Fatalf("response %d: %s", i, lines[i])
		}
	}
}
