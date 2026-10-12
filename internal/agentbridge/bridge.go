// Package agentbridge connects a stdio MCP process to the running desktop app.
package agentbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

const DiscoveryFile = "agent-endpoint.json"

type Endpoint struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

type Request struct {
	Action  string `json:"action"`
	Profile string `json:"profile,omitempty"`
}

func Read(dir string) (Endpoint, error) {
	var endpoint Endpoint
	b, err := os.ReadFile(filepath.Join(dir, DiscoveryFile))
	if err != nil {
		return endpoint, errors.New("OpenConnect Multi is not running. Call vpn_open_app, then unlock it in its window")
	}
	if err := json.Unmarshal(b, &endpoint); err != nil {
		return endpoint, errors.New("invalid app discovery file; restart OpenConnect Multi")
	}
	u, err := url.Parse(endpoint.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(endpoint.Token) < 32 {
		return Endpoint{}, errors.New("invalid local app endpoint; restart OpenConnect Multi")
	}
	return endpoint, nil
}

func Publish(dir string, endpoint Endpoint) (func(), error) {
	b, err := json.Marshal(endpoint)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, DiscoveryFile)
	file, err := os.CreateTemp(dir, "agent-endpoint-*.tmp")
	if err != nil {
		return nil, err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(b)
	}
	closeErr := file.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	return func() {
		// A later instance must not lose its discovery file.
		current, err := Read(dir)
		if err == nil && current.Token == endpoint.Token {
			_ = os.Remove(path)
		}
	}, nil
}

func Call(ctx context.Context, dir string, input Request) (map[string]any, error) {
	endpoint, err := Read(dir)
	if err != nil {
		return nil, err
	}
	return endpoint.Call(ctx, input)
}

func (endpoint Endpoint) Call(ctx context.Context, input Request) (map[string]any, error) {
	b, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL+"/api/agent", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+endpoint.Token)
	req.Header.Set("Content-Type", "application/json")
	// Never send the local capability token through proxies or redirects.
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("cannot reach OpenConnect Multi; call vpn_open_app or restart the app")
	}
	defer response.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return nil, errors.New("invalid app response; restart the updated application")
	}
	if response.StatusCode != http.StatusOK {
		if message, ok := result["error"].(string); ok {
			return nil, errors.New(message)
		}
		return nil, fmt.Errorf("app returned HTTP %d", response.StatusCode)
	}
	return result, nil
}
