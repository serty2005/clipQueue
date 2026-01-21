package windows

import (
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/serty2005/clipqueue/internal/config"
	"github.com/serty2005/clipqueue/internal/logger"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procCreateWindowEx   = user32.NewProc("CreateWindowExW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procGetMessage       = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessage  = user32.NewProc("DispatchMessageW")
	procRegisterClassEx  = user32.NewProc("RegisterClassExW")
	procUnregisterClass  = user32.NewProc("UnregisterClassW")
	procDefWindowProc    = user32.NewProc("DefWindowProcW")
)

type WNDCLASSEX struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type MSG struct {
	HWND    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

const (
	WM_HOTKEY          = 0x0312
	WM_CLIPBOARDUPDATE = 0x031D
	WM_QUIT            = 0x0012
	WM_RELOAD_CONFIG   = 0x0400 + 2 // WM_USER + 2
	WM_START_CAPTURE   = 0x0400 + 3 // WM_USER + 3
	WM_CAPTURE_DONE    = 0x0400 + 4 // WM_USER + 4
)

type Host struct {
	cfg               *config.SafeConfig
	hwnd              uintptr
	className         *uint16
	running           bool
	onToggleQueue     func()
	onPasteNext       func()
	onClipboardUpdate func()
	onTrayCommand     func(id uint32) // Callback for system tray menu commands
	hotkeys           *Hotkeys
	clipboardWatcher  *ClipboardWatcher
	tray              *Tray         // System tray icon
	done              chan struct{} // Channel to signal that host has stopped
	captureChan       chan string   // Channel for hotkey capture results
	hookHandle        uintptr       // Current hook handle
}

func NewHost(cfg *config.SafeConfig) (*Host, error) {
	host := &Host{
		cfg:               cfg,
		onToggleQueue:     func() {},
		onPasteNext:       func() {},
		onClipboardUpdate: func() {},
		onTrayCommand:     func(id uint32) {}, // Empty default callback
		done:              make(chan struct{}),
		captureChan:       make(chan string, 1), // Buffered to avoid blocking
		hookHandle:        0,
	}

	var err error
	host.hotkeys, err = NewHotkeys(host)
	if err != nil {
		return nil, err
	}

	host.clipboardWatcher, err = NewClipboardWatcher(host)
	if err != nil {
		return nil, err
	}

	return host, nil
}

// Wait waits for the host to stop
func (h *Host) Wait() {
	<-h.done
}

func (h *Host) OnHotkeyToggleQueue(callback func()) {
	h.onToggleQueue = callback
}

func (h *Host) OnHotkeyPasteNext(callback func()) {
	h.onPasteNext = callback
}

func (h *Host) OnClipboardUpdate(callback func()) {
	h.onClipboardUpdate = callback
}

// OnTrayCommand sets the callback for handling system tray menu commands
func (h *Host) OnTrayCommand(callback func(id uint32)) {
	h.onTrayCommand = callback
}

// UpdateTrayTooltip updates the tooltip text for the system tray icon
func (h *Host) UpdateTrayTooltip(text string) error {
	if h.tray != nil {
		return h.tray.UpdateTooltip(text)
	}
	return nil
}

// RegisterMacro registers a macro hotkey that sends text when pressed
func (h *Host) RegisterMacro(hotkey string, macro config.Macro) error {
	_, err := h.hotkeys.ParseAndRegister(hotkey, func() {
		logger.Debug("Macro hotkey pressed: %s", hotkey)
		if macro.Mode == "paste" {
			if err := PasteString(macro.Text); err != nil {
				logger.Error("Failed to paste text for macro %s: %v", hotkey, err)
			}
		} else { // default to "type" mode
			if err := TypeString(macro.Text); err != nil {
				logger.Error("Failed to type text for macro %s: %v", hotkey, err)
			}
		}
	})
	return err
}

func (h *Host) Start() error {
	// Create a channel to communicate errors from the goroutine that will lock the OS thread
	errChan := make(chan error)

	// Start the main thread-bound goroutine
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		// Register window class
		className, err := syscall.UTF16PtrFromString("ClipQueueWindowClass")
		if err != nil {
			errChan <- err
			return
		}
		h.className = className

		wc := WNDCLASSEX{
			Size:       uint32(unsafe.Sizeof(WNDCLASSEX{})),
			Style:      0,
			WndProc:    syscall.NewCallback(h.windowProc),
			ClsExtra:   0,
			WndExtra:   0,
			Instance:   0,
			Icon:       0,
			Cursor:     0,
			Background: 0,
			MenuName:   nil,
			ClassName:  h.className,
			IconSm:     0,
		}

		atom, _, err := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
		if atom == 0 {
			errChan <- err
			return
		}

		// Create hidden window
		ret, _, err := procCreateWindowEx.Call(
			0,
			uintptr(unsafe.Pointer(h.className)),
			0,
			0,
			0, 0, 0, 0,
			0, 0, 0, 0,
		)
		if ret == 0 {
			errChan <- err
			return
		}
		h.hwnd = ret

		// Register hotkeys
		if err := h.hotkeys.Register(); err != nil {
			errChan <- err
			return
		}

		// Register macros from config
		if cfgCopy := h.cfg.Get(); cfgCopy.Macros != nil {
			for hotkey, macro := range cfgCopy.Macros {
				if err := h.RegisterMacro(hotkey, macro); err != nil {
					logger.Error("Failed to register macro %s: %v", hotkey, err)
				}
			}
		}

		// Add clipboard format listener
		if err := h.clipboardWatcher.Start(); err != nil {
			errChan <- err
			return
		}

		// Initialize system tray
		h.tray = NewTray(h.hwnd)
		if err := h.tray.Setup(""); err != nil {
			logger.Error("Failed to initialize system tray: %v", err)
		}

		// Notify that initialization was successful
		close(errChan)

		// Run message loop
		h.messageLoop()

		// Cleanup after message loop exits
		h.clipboardWatcher.Stop()
		h.hotkeys.Unregister()
		if h.tray != nil {
			h.tray.Remove()
		}
		procDestroyWindow.Call(h.hwnd)
		procUnregisterClass.Call(uintptr(unsafe.Pointer(h.className)), 0)
	}()

	// Wait for initialization to complete
	err := <-errChan
	return err
}

func (h *Host) ReloadConfig() error {
	// Send WM_RELOAD_CONFIG message to the window to reload config from main thread
	procPostMessage := user32.NewProc("PostMessageW")
	ret, _, err := procPostMessage.Call(h.hwnd, uintptr(WM_RELOAD_CONFIG), 0, 0)
	if ret == 0 {
		logger.Error("PostMessage failed for WM_RELOAD_CONFIG: %v", err)
		return err
	}
	logger.Info("WM_RELOAD_CONFIG message sent successfully")
	return nil
}

func (h *Host) CaptureHotkey(timeout time.Duration) (string, error) {
	// Drain any old values from the channel
	select {
	case <-h.captureChan:
	default:
	}

	// Send message to start capture
	procPostMessage := user32.NewProc("PostMessageW")
	ret, _, err := procPostMessage.Call(h.hwnd, uintptr(WM_START_CAPTURE), 0, 0)
	if ret == 0 {
		logger.Error("PostMessage failed for WM_START_CAPTURE: %v", err)
		return "", err
	}

	// Wait for result or timeout
	select {
	case hotkey := <-h.captureChan:
		if hotkey == "" {
			return "", syscall.ETIMEDOUT
		}
		return hotkey, nil
	case <-time.After(timeout):
		// Send message to stop capture and unhook
		procPostMessage.Call(h.hwnd, uintptr(WM_CAPTURE_DONE), 0, 0)
		return "", syscall.ETIMEDOUT
	}
}

func (h *Host) Stop() error {
	// Use PostMessage to safely close the window from another goroutine
	const WM_CLOSE = 0x0010
	procPostMessage := user32.NewProc("PostMessageW")
	procPostMessage.Call(h.hwnd, uintptr(WM_CLOSE), 0, 0)
	return nil
}

func (h *Host) messageLoop() {
	var msg MSG
	for {
		ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if ret == 0 { // WM_QUIT
			break
		}
		if int32(ret) == -1 { // Error
			continue
		}

		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
	close(h.done) // Signal that host has stopped
}

func (h *Host) windowProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	const (
		WM_CLOSE     = 0x0010
		WM_DESTROY   = 0x0002
		WM_RBUTTONUP = 0x0205
		WM_LBUTTONUP = 0x0202
	)

	switch msg {
	case WM_START_CAPTURE:
		logger.Info("WM_START_CAPTURE received, starting hotkey capture")
		// Set hook directly in the main thread
		handle, err := SetHook(func(hotkey string) {
			logger.Info("Hotkey captured: %s", hotkey)
			h.captureChan <- hotkey
			// Send message to unhook
			procPostMessage := user32.NewProc("PostMessageW")
			procPostMessage.Call(h.hwnd, uintptr(WM_CAPTURE_DONE), 0, 0)
		})
		if err != nil {
			logger.Error("Failed to set hook: %v", err)
			h.captureChan <- ""
		} else {
			h.hookHandle = handle
		}
		return 0

	case WM_CAPTURE_DONE:
		logger.Info("WM_CAPTURE_DONE received, unhooking")
		if h.hookHandle != 0 {
			if err := Unhook(h.hookHandle); err != nil {
				logger.Error("Failed to unhook: %v", err)
			}
			h.hookHandle = 0
		}
		return 0

	case WM_TRAY_CALLBACK:
		switch lParam {
		case WM_RBUTTONUP, WM_LBUTTONUP:
			if h.tray != nil {
				selectedID := h.tray.ShowMenu()
				logger.Info("Menu item selected: %d", selectedID)
				if selectedID > 0 {
					h.onTrayCommand(selectedID)
				}
			}
		}
		return 0
	case WM_HOTKEY:
		logger.Info("WM_HOTKEY received, id=%d", wParam)
		callback, exists := h.hotkeys.GetCallback(uint32(wParam))
		if exists {
			callback()
		} else {
			logger.Warn("No callback for hotkey ID: %d", wParam)
		}
		return 0

	case WM_CLIPBOARDUPDATE:
		logger.Info("WM_CLIPBOARDUPDATE received")
		h.onClipboardUpdate()
		return 0

	case WM_RELOAD_CONFIG:
		logger.Info("WM_RELOAD_CONFIG received, reloading hotkeys...")
		// Unregister all existing hotkeys
		if err := h.hotkeys.Unregister(); err != nil {
			logger.Error("Failed to unregister hotkeys: %v", err)
		}
		// Re-register predefined hotkeys
		if err := h.hotkeys.Register(); err != nil {
			logger.Error("Failed to register predefined hotkeys: %v", err)
		}
		// Re-register macros from config
		if cfgCopy := h.cfg.Get(); cfgCopy.Macros != nil {
			for hotkey, text := range cfgCopy.Macros {
				if err := h.RegisterMacro(hotkey, text); err != nil {
					logger.Error("Failed to register macro %s: %v", hotkey, err)
				}
			}
		}
		logger.Info("Hotkeys reloaded successfully")
		return 0

	case WM_CLOSE:
		logger.Info("WM_CLOSE received, posting WM_QUIT")
		procPostQuitMessage := user32.NewProc("PostQuitMessage")
		procPostQuitMessage.Call(0)
		return 0

	case WM_DESTROY:
		logger.Info("WM_DESTROY received")
		return 0
	}

	ret, _, _ := procDefWindowProc.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
}
