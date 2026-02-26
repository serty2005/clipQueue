//go:build windows

package uihost

func NewPreferredUIHost(url string) UIHost {
	return NewFallbackUIHost(
		NewWebViewUIHost(url),
		NewBrowserUIHost(url),
	)
}
