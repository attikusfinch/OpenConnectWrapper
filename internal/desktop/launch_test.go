package desktop

import "testing"

func TestReusePreservesDesktopMode(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		focus, browser, headless            bool
		wantFocus, wantDesktop, wantBrowser int
	}{
		{"restore desktop", true, false, false, 1, 0, 0},
		{"desktop shell for headless server", false, false, false, 1, 1, 0},
		{"explicit browser", true, true, false, 0, 0, 1},
		{"headless reuse", true, false, true, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			focus, desktop, browser := 0, 0, 0
			actions := launchActions{
				focusDesktop: func(dir string) bool {
					focus++
					if dir != "test-config" {
						t.Fatal(dir)
					}
					return tc.focus
				},
				openDesktop: func(url, dir string) error {
					desktop++
					if url != "http://127.0.0.1:1234" || dir != "test-config" {
						t.Fatal("wrong desktop target")
					}
					return nil
				},
				openBrowser: func(url string) error { browser++; return nil },
			}
			if err := showExistingUI("http://127.0.0.1:1234", "test-config", tc.browser, tc.headless, actions); err != nil {
				t.Fatal(err)
			}
			if focus != tc.wantFocus || desktop != tc.wantDesktop || browser != tc.wantBrowser {
				t.Fatalf("focus=%d desktop=%d browser=%d", focus, desktop, browser)
			}
		})
	}
}
