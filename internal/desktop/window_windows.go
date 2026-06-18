//go:build windows

package desktop

import (
	"path/filepath"

	webview "github.com/jchv/go-webview2"
)

func Open(url string, dataDir string) error {
	view := webview.NewWithOptions(webview.WebViewOptions{
		Debug:     false,
		DataPath:  filepath.Join(dataDir, "webview2"),
		AutoFocus: true,
		WindowOptions: webview.WindowOptions{
			Title:  "OpenConnect Multi",
			Width:  1180,
			Height: 820,
			IconId: 1,
			Center: true,
		},
	})
	defer view.Destroy()

	view.Navigate(url)
	view.Run()
	return nil
}
