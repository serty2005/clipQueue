//go:build windows

package uihost

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"

	webview2 "github.com/jchv/go-webview2"
	"github.com/serty2005/clipqueue/internal/logger"
)

const (
	wsPopup            = 0x80000000
	wsVisible          = 0x10000000
	wsOverlappedWindow = 0x00CF0000

	swHide    = 0
	swShow    = 5
	swRestore = 9

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020
)

var (
	user32UIHost            = syscall.NewLazyDLL("user32.dll")
	procGetWindowLongPtrW   = user32UIHost.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW   = user32UIHost.NewProc("SetWindowLongPtrW")
	procSetWindowPos        = user32UIHost.NewProc("SetWindowPos")
	procShowWindowUIHost    = user32UIHost.NewProc("ShowWindow")
	procSetForegroundWindow = user32UIHost.NewProc("SetForegroundWindow")
	procSetFocusUIHost      = user32UIHost.NewProc("SetFocus")
	procIsWindowVisible     = user32UIHost.NewProc("IsWindowVisible")
)

type WebViewUIHost struct {
	mu        sync.RWMutex
	title     string
	url       string
	width     int
	height    int
	readyOnce sync.Once
	readyDone chan struct{}
	readyErr  error

	wv      webview2.WebView
	hwnd    uintptr
	visible bool
	closed  bool
	started bool
}

func NewWebViewUIHost(url string) *WebViewUIHost {
	return &WebViewUIHost{
		title:     "ClipQueue",
		url:       url,
		width:     980,
		height:    760,
		readyDone: make(chan struct{}),
	}
}

func (h *WebViewUIHost) ensureReady() error {
	h.readyOnce.Do(func() {
		h.mu.Lock()
		h.started = true
		h.mu.Unlock()
		go h.runUIThread()
	})
	<-h.readyDone
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.readyErr
}

func (h *WebViewUIHost) runUIThread() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	h.mu.RLock()
	title := h.title
	width := h.width
	height := h.height
	url := h.url
	h.mu.RUnlock()

	wv := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  title,
			Width:  uint(width),
			Height: uint(height),
			Center: true,
		},
	})
	if wv == nil {
		h.mu.Lock()
		h.readyErr = fmt.Errorf("не удалось создать WebView2 окно")
		h.closed = true
		h.mu.Unlock()
		close(h.readyDone)
		return
	}

	hwnd := uintptr(wv.Window())
	if hwnd == 0 {
		h.mu.Lock()
		h.readyErr = fmt.Errorf("WebView2 вернул пустой HWND")
		h.closed = true
		h.mu.Unlock()
		close(h.readyDone)
		return
	}

	h.applyFramelessStyle(hwnd)
	_, _, _ = procShowWindowUIHost.Call(hwnd, swHide)
	if url != "" {
		wv.Navigate(url)
	}

	h.mu.Lock()
	h.wv = wv
	h.hwnd = hwnd
	h.visible = false
	h.readyErr = nil
	h.mu.Unlock()

	close(h.readyDone)

	logger.Info("WebView UI host initialized (HWND=%d)", hwnd)
	wv.Run()

	h.mu.Lock()
	h.wv = nil
	h.hwnd = 0
	h.visible = false
	h.closed = true
	h.mu.Unlock()

	logger.Info("WebView UI host stopped")
}

func (h *WebViewUIHost) applyFramelessStyle(hwnd uintptr) {
	styleIndex := ^uintptr(15) // GWL_STYLE == -16
	style, _, _ := procGetWindowLongPtrW.Call(hwnd, styleIndex)
	style = (style &^ wsOverlappedWindow) | wsPopup | wsVisible
	procSetWindowLongPtrW.Call(hwnd, styleIndex, style)
	procSetWindowPos.Call(
		hwnd,
		0,
		0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged,
	)
}

func (h *WebViewUIHost) dispatch(action func(wv webview2.WebView, hwnd uintptr)) error {
	if err := h.ensureReady(); err != nil {
		return err
	}

	h.mu.RLock()
	if h.closed || h.wv == nil || h.hwnd == 0 {
		h.mu.RUnlock()
		return fmt.Errorf("WebView UI host закрыт")
	}
	wv := h.wv
	hwnd := h.hwnd
	h.mu.RUnlock()

	wv.Dispatch(func() {
		action(wv, hwnd)
	})
	return nil
}

func (h *WebViewUIHost) Show() error {
	return h.dispatch(func(wv webview2.WebView, hwnd uintptr) {
		_, _, _ = procShowWindowUIHost.Call(hwnd, swRestore)
		_, _, _ = procShowWindowUIHost.Call(hwnd, swShow)
		_, _, _ = procSetForegroundWindow.Call(hwnd)
		_, _, _ = procSetFocusUIHost.Call(hwnd)
		h.mu.Lock()
		h.visible = true
		h.mu.Unlock()
	})
}

func (h *WebViewUIHost) Hide() error {
	return h.dispatch(func(wv webview2.WebView, hwnd uintptr) {
		_, _, _ = procShowWindowUIHost.Call(hwnd, swHide)
		h.mu.Lock()
		h.visible = false
		h.mu.Unlock()
	})
}

func (h *WebViewUIHost) Toggle() error {
	if err := h.ensureReady(); err != nil {
		return err
	}

	h.mu.RLock()
	visible := h.visible
	hwnd := h.hwnd
	h.mu.RUnlock()

	if hwnd != 0 {
		if r, _, _ := procIsWindowVisible.Call(hwnd); r == 0 {
			visible = false
		}
	}

	if visible {
		return h.Hide()
	}
	return h.Show()
}

func (h *WebViewUIHost) Focus() error {
	return h.dispatch(func(wv webview2.WebView, hwnd uintptr) {
		_, _, _ = procSetForegroundWindow.Call(hwnd)
		_, _, _ = procSetFocusUIHost.Call(hwnd)
		h.mu.Lock()
		h.visible = true
		h.mu.Unlock()
	})
}

func (h *WebViewUIHost) Close() error {
	h.mu.RLock()
	started := h.started
	alreadyClosed := h.closed
	h.mu.RUnlock()
	if !started || alreadyClosed {
		return nil
	}

	if err := h.ensureReady(); err != nil {
		return err
	}

	return h.dispatch(func(wv webview2.WebView, hwnd uintptr) {
		wv.Destroy()
	})
}

func (h *WebViewUIHost) Navigate(url string) error {
	h.mu.Lock()
	h.url = url
	h.mu.Unlock()

	return h.dispatch(func(wv webview2.WebView, hwnd uintptr) {
		wv.Navigate(url)
	})
}
