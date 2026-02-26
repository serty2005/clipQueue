package uihost

import (
	"sync"

	"github.com/serty2005/clipqueue/platform/windows"
)

type BrowserUIHost struct {
	mu  sync.RWMutex
	url string
}

func NewBrowserUIHost(url string) *BrowserUIHost {
	return &BrowserUIHost{url: url}
}

func (h *BrowserUIHost) Show() error {
	h.mu.RLock()
	url := h.url
	h.mu.RUnlock()
	return windows.OpenBrowser(url)
}

func (h *BrowserUIHost) Hide() error  { return nil }
func (h *BrowserUIHost) Focus() error { return h.Show() }
func (h *BrowserUIHost) Close() error { return nil }

func (h *BrowserUIHost) Toggle() error {
	return h.Show()
}

func (h *BrowserUIHost) Navigate(url string) error {
	h.mu.Lock()
	h.url = url
	h.mu.Unlock()
	return nil
}
