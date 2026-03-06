//go:build windows

package uihost

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"github.com/serty2005/clipqueue/internal/logger"
	"github.com/serty2005/clipqueue/platform/windows"
)

const (
	wsPopup            = 0x80000000
	wsVisible          = 0x10000000
	wsThickFrame       = 0x00040000
	wsOverlappedWindow = 0x00CF0000

	swHide    = 0
	swShow    = 5
	swRestore = 9

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020

	gwlpWndProc = -4

	wmClose        = 0x0010
	wmNCHitTest    = 0x0084
	wmExitSizeMove = 0x0232

	htClient      = 1
	htCaption     = 2
	htLeft        = 10
	htRight       = 11
	htTop         = 12
	htTopLeft     = 13
	htTopRight    = 14
	htBottom      = 15
	htBottomLeft  = 16
	htBottomRight = 17
)

var (
	user32UIHost            = syscall.NewLazyDLL("user32.dll")
	procGetWindowLongPtrW   = user32UIHost.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW   = user32UIHost.NewProc("SetWindowLongPtrW")
	procCallWindowProcW     = user32UIHost.NewProc("CallWindowProcW")
	procSetWindowPos        = user32UIHost.NewProc("SetWindowPos")
	procShowWindowUIHost    = user32UIHost.NewProc("ShowWindow")
	procSetForegroundWindow = user32UIHost.NewProc("SetForegroundWindow")
	procSetFocusUIHost      = user32UIHost.NewProc("SetFocus")
	procIsWindowVisible     = user32UIHost.NewProc("IsWindowVisible")
	procGetWindowRect       = user32UIHost.NewProc("GetWindowRect")
	procEnumChildWindows    = user32UIHost.NewProc("EnumChildWindows")

	webViewHostWndProcOnce sync.Once
	webViewHostWndProcPtr  uintptr
	webViewHostWndProcMu   sync.RWMutex
	webViewHostWndProcMap  = map[uintptr]*WebViewUIHost{}
)

type WebViewUIHost struct {
	mu                 sync.RWMutex
	title              string
	url                string
	width              int
	height             int
	initialState       WindowState
	windowStateHandler func(WindowState)
	readyOnce          sync.Once
	readyDone          chan struct{}
	readyErr           error

	wv         webview2.WebView
	hwnd       uintptr
	subclassed map[uintptr]uintptr // hwnd -> previous wndproc
	visible    bool
	destroying bool
	closed     bool
	started    bool

	bridge *NativeBridge
}

type rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

func NewWebViewUIHost(url string, initialState WindowState) *WebViewUIHost {
	width := initialState.Width
	height := initialState.Height
	if width <= 0 {
		width = 500
	}
	if height <= 0 {
		height = 250
	}
	return &WebViewUIHost{
		title:        "ClipQueue",
		url:          url,
		width:        width,
		height:       height,
		initialState: initialState,
		readyDone:    make(chan struct{}),
		subclassed:   make(map[uintptr]uintptr),
	}
}

func (h *WebViewUIHost) SetNativeBridge(bridge *NativeBridge) {
	h.mu.Lock()
	h.bridge = bridge
	h.mu.Unlock()
}

func (h *WebViewUIHost) SetWindowStateHandler(handler func(WindowState)) {
	h.mu.Lock()
	h.windowStateHandler = handler
	h.mu.Unlock()
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
	windows.ApplyCurrentThreadHighDPIAwareness()

	h.mu.RLock()
	title := h.title
	width := h.width
	height := h.height
	url := h.url
	initialState := h.initialState
	h.mu.RUnlock()

	wv := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  title,
			Width:  uint(width),
			Height: uint(height),
			Center: !initialState.HasBounds,
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
	h.applyStoredBounds(hwnd, initialState)
	h.bindNativeBridge(wv)
	h.installWindowSubclasses(hwnd)
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
	h.subclassed = map[uintptr]uintptr{}
	h.visible = false
	h.destroying = false
	h.closed = true
	h.mu.Unlock()

	h.unregisterSubclasses()

	logger.Info("WebView UI host stopped")
}

func (h *WebViewUIHost) applyStoredBounds(hwnd uintptr, state WindowState) {
	if !state.HasBounds || state.Width <= 0 || state.Height <= 0 {
		return
	}
	procSetWindowPos.Call(
		hwnd,
		0,
		uintptr(state.X),
		uintptr(state.Y),
		uintptr(state.Width),
		uintptr(state.Height),
		swpNoZOrder|swpNoActivate,
	)
}

func (h *WebViewUIHost) applyFramelessStyle(hwnd uintptr) {
	styleIndex := ^uintptr(15) // GWL_STYLE == -16
	style, _, _ := procGetWindowLongPtrW.Call(hwnd, styleIndex)
	style = (style &^ wsOverlappedWindow) | wsPopup | wsVisible | wsThickFrame
	procSetWindowLongPtrW.Call(hwnd, styleIndex, style)
	procSetWindowPos.Call(
		hwnd,
		0,
		0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged,
	)
}

func (h *WebViewUIHost) readWindowState(hwnd uintptr, visible bool) WindowState {
	state := WindowState{
		Visible: visible,
	}

	var r rect
	if ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok != 0 {
		state.X = int(r.Left)
		state.Y = int(r.Top)
		state.Width = int(r.Right - r.Left)
		state.Height = int(r.Bottom - r.Top)
		state.HasBounds = state.Width > 0 && state.Height > 0
	}

	if !state.HasBounds {
		h.mu.RLock()
		state.HasBounds = h.initialState.HasBounds
		state.X = h.initialState.X
		state.Y = h.initialState.Y
		state.Width = h.width
		state.Height = h.height
		h.mu.RUnlock()
	}

	return state
}

func (h *WebViewUIHost) notifyWindowState(state WindowState) {
	h.mu.Lock()
	h.initialState = state
	if state.Width > 0 {
		h.width = state.Width
	}
	if state.Height > 0 {
		h.height = state.Height
	}
	handler := h.windowStateHandler
	h.mu.Unlock()

	if handler != nil {
		handler(state)
	}
}

func ensureWebViewHostWndProc() uintptr {
	webViewHostWndProcOnce.Do(func() {
		webViewHostWndProcPtr = syscall.NewCallback(webViewHostWndProc)
	})
	return webViewHostWndProcPtr
}

func (h *WebViewUIHost) installWindowSubclasses(rootHWND uintptr) {
	h.installSingleSubclass(rootHWND)

	enumCB := syscall.NewCallback(func(childHWND, lParam uintptr) uintptr {
		host := (*WebViewUIHost)(unsafe.Pointer(lParam))
		host.installSingleSubclass(childHWND)
		return 1
	})
	procEnumChildWindows.Call(rootHWND, enumCB, uintptr(unsafe.Pointer(h)))
}

func (h *WebViewUIHost) installSingleSubclass(hwnd uintptr) {
	h.mu.RLock()
	_, exists := h.subclassed[hwnd]
	h.mu.RUnlock()
	if exists {
		return
	}

	wndProcIndex := ^uintptr(3) // GWLP_WNDPROC == -4
	prev, _, _ := procSetWindowLongPtrW.Call(hwnd, wndProcIndex, ensureWebViewHostWndProc())

	h.mu.Lock()
	if h.subclassed == nil {
		h.subclassed = make(map[uintptr]uintptr)
	}
	h.subclassed[hwnd] = prev
	h.mu.Unlock()

	webViewHostWndProcMu.Lock()
	webViewHostWndProcMap[hwnd] = h
	webViewHostWndProcMu.Unlock()
}

func (h *WebViewUIHost) unregisterSubclasses() {
	h.mu.RLock()
	prevMap := make(map[uintptr]uintptr, len(h.subclassed))
	for hwnd, prev := range h.subclassed {
		prevMap[hwnd] = prev
	}
	h.mu.RUnlock()

	wndProcIndex := ^uintptr(3) // GWLP_WNDPROC == -4
	for hwnd, prev := range prevMap {
		if prev != 0 {
			procSetWindowLongPtrW.Call(hwnd, wndProcIndex, prev)
		}
		webViewHostWndProcMu.Lock()
		delete(webViewHostWndProcMap, hwnd)
		webViewHostWndProcMu.Unlock()
	}
}

func webViewHostWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	webViewHostWndProcMu.RLock()
	h := webViewHostWndProcMap[hwnd]
	webViewHostWndProcMu.RUnlock()
	if h == nil {
		return 0
	}

	h.mu.RLock()
	prev := h.subclassed[hwnd]
	rootHWND := h.hwnd
	destroying := h.destroying
	visible := h.visible
	h.mu.RUnlock()
	if msg == wmClose && hwnd == rootHWND && rootHWND != 0 && !destroying {
		_, _, _ = procShowWindowUIHost.Call(rootHWND, swHide)
		h.mu.Lock()
		h.visible = false
		h.mu.Unlock()
		h.notifyWindowState(h.readWindowState(rootHWND, false))
		return 0
	}
	if msg == wmExitSizeMove && hwnd == rootHWND && rootHWND != 0 {
		if r, _, _ := procIsWindowVisible.Call(rootHWND); r == 0 {
			visible = false
		}
		h.notifyWindowState(h.readWindowState(rootHWND, visible))
	}
	if msg == wmNCHitTest && rootHWND != 0 {
		if hit, ok := h.hitTest(rootHWND, lParam); ok {
			return hit
		}
	}
	if prev != 0 {
		ret, _, _ := procCallWindowProcW.Call(prev, hwnd, uintptr(msg), wParam, lParam)
		return ret
	}
	return 0
}

func (h *WebViewUIHost) hitTest(hwnd uintptr, lParam uintptr) (uintptr, bool) {
	const (
		resizeBorderPx = int32(8)
		dragCaptionPx  = int32(8)
	)

	var r rect
	if ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return 0, false
	}

	x := int32(int16(lParam & 0xFFFF))
	y := int32(int16((lParam >> 16) & 0xFFFF))
	width := r.Right - r.Left
	height := r.Bottom - r.Top
	if width <= 0 || height <= 0 {
		return htClient, true
	}

	left := x < r.Left+resizeBorderPx
	right := x >= r.Right-resizeBorderPx
	top := y < r.Top+resizeBorderPx
	bottom := y >= r.Bottom-resizeBorderPx

	switch {
	case top && left:
		return htTopLeft, true
	case top && right:
		return htTopRight, true
	case bottom && left:
		return htBottomLeft, true
	case bottom && right:
		return htBottomRight, true
	case left:
		return htLeft, true
	case right:
		return htRight, true
	case bottom:
		return htBottom, true
	}

	if y < r.Top+dragCaptionPx {
		return htCaption, true
	}
	return htClient, true
}

func (h *WebViewUIHost) bindNativeBridge(wv webview2.WebView) {
	h.mu.RLock()
	bridge := h.bridge
	h.mu.RUnlock()
	if bridge == nil {
		return
	}

	wv.Init(`window.__clipQueueNativePush = window.__clipQueueNativePush || function(){}; window.__clipQueueNativeEvent = window.__clipQueueNativeEvent || function(){};`)

	mustBind := func(name string, fn interface{}) {
		if err := wv.Bind(name, fn); err != nil {
			logger.Warn("Не удалось зарегистрировать native bridge метод %s: %v", name, err)
		}
	}

	mustBind("cqNativeAvailable", func() bool { return true })
	mustBind("cqNativeGetConfig", func() (interface{}, error) {
		if bridge.GetConfig == nil {
			return nil, fmt.Errorf("native get config bridge not configured")
		}
		return bridge.GetConfig()
	})
	mustBind("cqNativeSaveConfig", func(cfg map[string]interface{}) (interface{}, error) {
		if bridge.SaveConfig == nil {
			return nil, fmt.Errorf("native save config bridge not configured")
		}
		return bridge.SaveConfig(cfg)
	})
	mustBind("cqNativeCaptureHotkey", func() (interface{}, error) {
		if bridge.CaptureHotkey == nil {
			return nil, fmt.Errorf("native capture hotkey bridge not configured")
		}
		return bridge.CaptureHotkey()
	})
	mustBind("cqNativeGetUISnapshot", func() (interface{}, error) {
		if bridge.GetUISnapshot == nil {
			return nil, fmt.Errorf("native snapshot bridge not configured")
		}
		return bridge.GetUISnapshot()
	})
	mustBind("cqNativeToggleQueue", func() (interface{}, error) {
		if bridge.ToggleQueue == nil {
			return nil, fmt.Errorf("native toggle queue bridge not configured")
		}
		return bridge.ToggleQueue()
	})
	mustBind("cqNativeToggleQueueOrder", func() (interface{}, error) {
		if bridge.ToggleQueueOrder == nil {
			return nil, fmt.Errorf("native toggle queue order bridge not configured")
		}
		return bridge.ToggleQueueOrder()
	})
	mustBind("cqNativeClearQueue", func() (interface{}, error) {
		if bridge.ClearQueue == nil {
			return nil, fmt.Errorf("native clear queue bridge not configured")
		}
		return bridge.ClearQueue()
	})
	mustBind("cqNativeCopyHistoryItem", func(id string) (interface{}, error) {
		if bridge.CopyHistoryItem == nil {
			return nil, fmt.Errorf("native copy history bridge not configured")
		}
		return bridge.CopyHistoryItem(id)
	})
	mustBind("cqNativeGetHistory", func() (interface{}, error) {
		if bridge.GetHistory == nil {
			return nil, fmt.Errorf("native get history bridge not configured")
		}
		return bridge.GetHistory()
	})
	mustBind("cqNativeGetQueueState", func() (interface{}, error) {
		if bridge.GetQueueState == nil {
			return nil, fmt.Errorf("native get queue state bridge not configured")
		}
		return bridge.GetQueueState()
	})
	mustBind("cqNativeRemoveQueueItem", func(index int) (interface{}, error) {
		if bridge.RemoveQueueItem == nil {
			return nil, fmt.Errorf("native remove queue item bridge not configured")
		}
		return bridge.RemoveQueueItem(index)
	})
	mustBind("cqNativeParseLab", func(command string) (interface{}, error) {
		if bridge.ParseLab == nil {
			return nil, fmt.Errorf("native parse lab bridge not configured")
		}
		return bridge.ParseLab(command)
	})
	mustBind("cqNativeBuildLab", func(steps []map[string]interface{}) (interface{}, error) {
		if bridge.BuildLab == nil {
			return nil, fmt.Errorf("native build lab bridge not configured")
		}
		return bridge.BuildLab(steps)
	})
	mustBind("cqNativeStartSequenceRecording", func() (interface{}, error) {
		if bridge.StartSequence == nil {
			return nil, fmt.Errorf("native start sequence bridge not configured")
		}
		return bridge.StartSequence()
	})
	mustBind("cqNativeStopSequenceRecording", func() (interface{}, error) {
		if bridge.StopSequence == nil {
			return nil, fmt.Errorf("native stop sequence bridge not configured")
		}
		return bridge.StopSequence()
	})
	mustBind("cqNativeGetSequenceStatus", func(last int) (interface{}, error) {
		if bridge.GetSequenceStatus == nil {
			return nil, fmt.Errorf("native get sequence status bridge not configured")
		}
		return bridge.GetSequenceStatus(last)
	})
}

func (h *WebViewUIHost) NotifyNativeStateChanged() {
	h.mu.RLock()
	bridge := h.bridge
	ready := h.wv != nil && h.hwnd != 0 && !h.closed
	h.mu.RUnlock()
	if bridge == nil || bridge.GetUISnapshot == nil || !ready {
		return
	}

	if err := h.dispatch(func(wv webview2.WebView, hwnd uintptr) {
		snapshot, err := bridge.GetUISnapshot()
		if err != nil {
			logger.Warn("Ошибка получения native snapshot: %v", err)
			return
		}
		payload, err := json.Marshal(snapshot)
		if err != nil {
			logger.Warn("Ошибка сериализации native snapshot: %v", err)
			return
		}
		wv.Eval(`window.__clipQueueNativePush && window.__clipQueueNativePush(` + string(payload) + `);`)
	}); err != nil {
		logger.Debug("NotifyNativeStateChanged skipped: %v", err)
	}
}

func (h *WebViewUIHost) NotifyNativeMacroInvoke(name string, done bool) {
	h.mu.RLock()
	ready := h.wv != nil && h.hwnd != 0 && !h.closed
	h.mu.RUnlock()
	if !ready {
		return
	}
	payload, err := json.Marshal(map[string]interface{}{
		"type": "macroInvoke",
		"name": name,
		"done": done,
	})
	if err != nil {
		logger.Warn("Ошибка сериализации события macroInvoke: %v", err)
		return
	}
	if err := h.dispatch(func(wv webview2.WebView, hwnd uintptr) {
		wv.Eval(`window.__clipQueueNativeEvent && window.__clipQueueNativeEvent(` + string(payload) + `);`)
	}); err != nil {
		logger.Debug("NotifyNativeMacroInvoke skipped: %v", err)
	}
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
		h.installWindowSubclasses(hwnd)
		_, _, _ = procShowWindowUIHost.Call(hwnd, swRestore)
		_, _, _ = procShowWindowUIHost.Call(hwnd, swShow)
		_, _, _ = procSetForegroundWindow.Call(hwnd)
		_, _, _ = procSetFocusUIHost.Call(hwnd)
		h.mu.Lock()
		h.visible = true
		h.mu.Unlock()
		h.notifyWindowState(h.readWindowState(hwnd, true))
	})
}

func (h *WebViewUIHost) Hide() error {
	return h.dispatch(func(wv webview2.WebView, hwnd uintptr) {
		_, _, _ = procShowWindowUIHost.Call(hwnd, swHide)
		h.mu.Lock()
		h.visible = false
		h.mu.Unlock()
		h.notifyWindowState(h.readWindowState(hwnd, false))
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
		_, _, _ = procShowWindowUIHost.Call(hwnd, swRestore)
		_, _, _ = procShowWindowUIHost.Call(hwnd, swShow)
		_, _, _ = procSetForegroundWindow.Call(hwnd)
		_, _, _ = procSetFocusUIHost.Call(hwnd)
		h.mu.Lock()
		h.visible = true
		h.mu.Unlock()
		h.notifyWindowState(h.readWindowState(hwnd, true))
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
		h.mu.Lock()
		h.destroying = true
		visible := h.visible
		h.mu.Unlock()
		h.notifyWindowState(h.readWindowState(hwnd, visible))
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
