package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"openconnectmulti/internal/agentbridge"
	"openconnectmulti/internal/mcpserver"
	"openconnectmulti/internal/vault"
)

type backend struct{ dir, app string }

func (b backend) Call(ctx context.Context, req agentbridge.Request) (map[string]any, error) {
	data, err := agentbridge.Call(ctx, b.dir, req)
	if err != nil && req.Action == "status" {
		return map[string]any{"running": false, "next_step": err.Error()}, nil
	}
	return data, err
}

func (b backend) Open(ctx context.Context) (map[string]any, error) {
	endpoint, err := agentbridge.Read(b.dir)
	if err == nil {
		if _, err := endpoint.Call(ctx, agentbridge.Request{Action: "status"}); err == nil {
			if err := openURL(endpoint.URL); err != nil {
				return nil, err
			}
			return map[string]any{"running": true, "next_step": "The app is open in your browser. Enter the PIN there if locked, then call vpn_status."}, nil
		}
	}
	if err := launchApp(b.app, b.dir); err != nil {
		return nil, err
	}
	return map[string]any{"launch_requested": true, "next_step": "Accept the Windows app launch if prompted, unlock the app window, then call vpn_status. Do not put the PIN in chat."}, nil
}

func main() {
	configDir := flag.String("config-dir", "", "same vault directory as the desktop app")
	app := flag.String("app", "", "desktop executable (default: sibling openconnectmulti)")
	flag.Parse()
	log.SetOutput(os.Stderr)
	var err error
	if *configDir == "" {
		*configDir, err = vault.DefaultDir()
		if err != nil {
			log.Fatal(err)
		}
	}
	if *app == "" {
		executable, err := os.Executable()
		if err != nil {
			log.Fatal(err)
		}
		name := "openconnectmulti"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		*app = filepath.Join(filepath.Dir(executable), name)
	}
	if err := mcpserver.Serve(context.Background(), os.Stdin, os.Stdout, backend{*configDir, *app}); err != nil {
		log.Fatal(err)
	}
}
