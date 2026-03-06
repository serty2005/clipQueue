//go:build windows

package uihost

func NewPreferredUIHost(url string, initialState WindowState) UIHost {
	return NewFallbackUIHost(
		NewWebViewUIHost(url, initialState),
		NewBrowserUIHost(url),
	)
}
