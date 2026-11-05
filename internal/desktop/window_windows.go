//go:build windows

package desktop

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	webview "github.com/jchv/go-webview2"
)

var windowUser32 = syscall.NewLazyDLL("user32.dll")
var windowSetProp = windowUser32.NewProc("SetPropW")
var windowGetProp = windowUser32.NewProc("GetPropW")
var windowRemoveProp = windowUser32.NewProc("RemovePropW")
var windowEnum = windowUser32.NewProc("EnumWindows")
var windowIsIconic = windowUser32.NewProc("IsIconic")
var windowShow = windowUser32.NewProc("ShowWindow")
var windowForeground = windowUser32.NewProc("SetForegroundWindow")

func windowProperty(dataDir string) *uint16 {
	if absolute, err := filepath.Abs(dataDir); err == nil {
		dataDir = absolute
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(dataDir))))
	name, _ := syscall.UTF16PtrFromString(fmt.Sprintf("OpenConnectMulti.MainWindow.%x", sum[:16]))
	return name
}

// The per-config property avoids activating a window belonging to a different
// isolated instance, and works when the main window is minimized.
func ActivateExisting(dataDir string) bool {
	name := windowProperty(dataDir)
	var found uintptr
	callback := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		value, _, _ := windowGetProp.Call(hwnd, uintptr(unsafe.Pointer(name)))
		if value == 1 {
			found = hwnd
			return 0
		}
		return 1
	})
	_, _, _ = windowEnum.Call(callback, 0)
	if found == 0 {
		return false
	}
	showMode := uintptr(5) // SW_SHOW, preserving a maximized window.
	if iconic, _, _ := windowIsIconic.Call(found); iconic != 0 {
		showMode = 9
	} // SW_RESTORE
	_, _, _ = windowShow.Call(found, showMode)
	_, _, _ = windowForeground.Call(found)
	return true
}

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
	property := windowProperty(dataDir)
	hwnd := uintptr(view.Window())
	_, _, _ = windowSetProp.Call(hwnd, uintptr(unsafe.Pointer(property)), 1)
	defer windowRemoveProp.Call(hwnd, uintptr(unsafe.Pointer(property)))

	view.Navigate(url)
	view.Run()
	return nil
}
