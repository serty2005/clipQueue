package uihost

import (
	"sync"

	"github.com/serty2005/clipqueue/internal/logger"
)

type FallbackUIHost struct {
	mu          sync.RWMutex
	primary     UIHost
	fallback    UIHost
	useFallback bool
}

func NewFallbackUIHost(primary UIHost, fallback UIHost) *FallbackUIHost {
	return &FallbackUIHost{
		primary:  primary,
		fallback: fallback,
	}
}

func (h *FallbackUIHost) active() UIHost {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.useFallback {
		return h.fallback
	}
	return h.primary
}

func (h *FallbackUIHost) switchToFallback(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.useFallback {
		return
	}
	h.useFallback = true
	logger.Warn("UIHost fallback activated (browser): %v", err)
}

func (h *FallbackUIHost) Show() error {
	active := h.active()
	if err := active.Show(); err != nil && h.fallback != nil && active == h.primary {
		h.switchToFallback(err)
		return h.fallback.Show()
	}
	return nil
}

func (h *FallbackUIHost) Hide() error {
	return h.active().Hide()
}

func (h *FallbackUIHost) Toggle() error {
	active := h.active()
	if err := active.Toggle(); err != nil && h.fallback != nil && active == h.primary {
		h.switchToFallback(err)
		return h.fallback.Toggle()
	}
	return nil
}

func (h *FallbackUIHost) Focus() error {
	active := h.active()
	if err := active.Focus(); err != nil && h.fallback != nil && active == h.primary {
		h.switchToFallback(err)
		return h.fallback.Focus()
	}
	return nil
}

func (h *FallbackUIHost) Close() error {
	var firstErr error
	if h.primary != nil {
		if err := h.primary.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if h.fallback != nil {
		if err := h.fallback.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (h *FallbackUIHost) Navigate(url string) error {
	if h.primary != nil {
		if err := h.primary.Navigate(url); err != nil {
			h.switchToFallback(err)
		}
	}
	if h.fallback != nil {
		if err := h.fallback.Navigate(url); err != nil {
			return err
		}
	}
	return nil
}

func (h *FallbackUIHost) SetNativeBridge(bridge *NativeBridge) {
	if cap, ok := h.primary.(NativeBridgeCapable); ok {
		cap.SetNativeBridge(bridge)
	}
	if cap, ok := h.fallback.(NativeBridgeCapable); ok {
		cap.SetNativeBridge(bridge)
	}
}

func (h *FallbackUIHost) NotifyNativeStateChanged() {
	if cap, ok := h.active().(NativeBridgeCapable); ok {
		cap.NotifyNativeStateChanged()
	}
}

func (h *FallbackUIHost) NotifyNativeMacroInvoke(name string, done bool) {
	if cap, ok := h.active().(NativeBridgeCapable); ok {
		cap.NotifyNativeMacroInvoke(name, done)
	}
}
