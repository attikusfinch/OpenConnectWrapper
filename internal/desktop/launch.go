package desktop

type launchActions struct {
	focusDesktop func(string) bool
	openDesktop  func(string, string) error
	openBrowser  func(string) error
}

func ShowExisting(url, dataDir string, useBrowser, headless bool, browserOpener func(string) error) error {
	return showExistingUI(url, dataDir, useBrowser, headless, launchActions{ActivateExisting, Open, browserOpener})
}

// Reusing the API server preserves the user's choice of desktop/browser UI.
func showExistingUI(url, dataDir string, useBrowser, headless bool, actions launchActions) error {
	if headless {
		return nil
	}
	if useBrowser {
		return actions.openBrowser(url)
	}
	if actions.focusDesktop(dataDir) {
		return nil
	}
	return actions.openDesktop(url, dataDir)
}
