package windows

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/serty2005/clipqueue/internal/config"
	"github.com/serty2005/clipqueue/internal/logger"
)

type MacroExecutor interface {
	ExecuteMacro(macro config.Macro) error
}

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
	controller        MacroExecutor
	hwnd              uintptr
	className         *uint16
	running           bool
	onToggleQueue     func()
	onPasteNext       func()
	onClipboardUpdate func()
	onTrayCommand     func(id uint32) // Callback for system tray menu commands
	inputListener     *InputListener
	clipboardWatcher  *ClipboardWatcher
	tray              *Tray         // System tray icon
	done              chan struct{} // Channel to signal that host has stopped
	captureChan       chan string   // Channel for hotkey capture results (legacy)
}

func NewHost(cfg *config.SafeConfig, controller MacroExecutor) (*Host, error) {

	host := &Host{
		cfg:               cfg,
		controller:        controller,
		onToggleQueue:     func() {},
		onPasteNext:       func() {},
		onClipboardUpdate: func() {},
		onTrayCommand:     func(id uint32) {}, // Empty default callback
		done:              make(chan struct{}),
		captureChan:       make(chan string, 1), // Buffered to avoid blocking
	}

	host.inputListener = NewInputListener(0) // hwnd will be set later

	var err error
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

// registerConfiguredHotkeys регистрирует хоткеи из конфига
func (h *Host) registerConfiguredHotkeys() {
	cfg := h.cfg.Get()
	matcher := h.inputListener.GetMatcher()

	// ToggleQueue
	hotkeyStr := cfg.Hotkeys.ToggleQueue
	sig := h.parseHotkeyToSignature(hotkeyStr)
	if sig == nil {
		hotkeyStr = "Alt+C"
		sig = h.parseHotkeyToSignature(hotkeyStr)
	}
	if sig != nil {
		matcher.Register(*sig, "toggle_queue", func() {
			h.onToggleQueue()
		})
		logger.Info("Успешная регистрация хоткея ToggleQueue: %s", hotkeyStr)
	} else {
		logger.Error("Не удалось зарегистрировать хоткей ToggleQueue: %s", cfg.Hotkeys.ToggleQueue)
	}

	// PasteNext
	hotkeyStr = cfg.Hotkeys.PasteNext
	sig = h.parseHotkeyToSignature(hotkeyStr)
	if sig == nil {
		hotkeyStr = "Alt+V"
		sig = h.parseHotkeyToSignature(hotkeyStr)
	}
	if sig != nil {
		matcher.Register(*sig, "paste_next", func() {
			h.onPasteNext()
		})
		logger.Info("Успешная регистрация хоткея PasteNext: %s", hotkeyStr)
	} else {
		logger.Error("Не удалось зарегистрировать хоткей PasteNext: %s", cfg.Hotkeys.PasteNext)
	}

	// Макросы
	for _, macro := range cfg.Macros {
		m := macro
		hotkeyStr := macro.Signature
		sig := h.parseHotkeyToSignature(hotkeyStr)
		if macro.Signature == "" || sig == nil {
			hotkeyStr = macro.Hotkey
			sig = h.parseHotkeyToSignature(hotkeyStr)
		}
		if sig != nil {
			matcher.Register(*sig, "macro:"+hotkeyStr, func() {
				h.controller.ExecuteMacro(m)
			})
			logger.Info("Успешная регистрация макроса %s: %s", macro.Name, hotkeyStr)
		} else {
			logger.Error("Не удалось зарегистрировать макрос %s: Signature='%s', Hotkey='%s'", macro.Name, macro.Signature, macro.Hotkey)
		}
	}
}

// parseHotkeyToSignature конвертирует строку хоткея в сигнатуру
func (h *Host) parseHotkeyToSignature(hotkeyStr string) *InputSignature {
	// Новый формат: "sig:..."
	if strings.HasPrefix(hotkeyStr, "sig:") {
		sig, err := SignatureFromBase64(strings.TrimPrefix(hotkeyStr, "sig:"))
		if err != nil {
			logger.Error("Failed to parse signature: %v", err)
			return nil
		}
		return sig
	}

	// Старый формат: "CTRL+ALT+C" - конвертируем
	var mods uint8
	var vk uint32

	parts := strings.Split(strings.ToUpper(hotkeyStr), "+")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		switch part {
		case "CTRL", "CONTROL":
			mods |= ModCtrl
		case "ALT":
			mods |= ModAlt
		case "SHIFT":
			mods |= ModShift
		case "WIN":
			mods |= ModWin
		default:
			if code, ok := keyMap[part]; ok {
				vk = code
			}
		}
	}

	if vk == 0 {
		logger.Error("Unknown key in hotkey: %s", hotkeyStr)
		return nil
	}

	rawData := make([]byte, 10)
	binary.LittleEndian.PutUint16(rawData[0:2], uint16(vk))

	sig := NewInputSignature(SourceKeyboard, rawData, mods)
	return &sig
}

// ParseHotkeyToSignature экспортированный метод для конвертации строки хоткея в сигнатуру
func (h *Host) ParseHotkeyToSignature(hotkeyStr string) *InputSignature {
	return h.parseHotkeyToSignature(hotkeyStr)
}

// CaptureHotkeyWithDisplay захватывает и возвращает ID и отображаемое имя
func (h *Host) CaptureHotkeyWithDisplay(timeout time.Duration) (id string, display string, err error) {
	h.inputListener.StartCapture()

	sig, err := h.inputListener.WaitForCapture(timeout)
	if err != nil {
		return "", "", err
	}

	return "sig:" + sig.ToBase64(), sig.DisplayHint, nil
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
	if sig := h.parseHotkeyToSignature(hotkey); sig != nil {
		h.inputListener.GetMatcher().Register(*sig, "macro:"+hotkey, func() {
			logger.Debug("Macro hotkey pressed: %s", hotkey)
			if err := h.controller.ExecuteMacro(macro); err != nil {
				logger.Error("Failed to execute macro %s: %v", hotkey, err)
			}
		})
		return nil
	}
	return fmt.Errorf("failed to parse hotkey: %s", hotkey)
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

		// Set hwnd for input listener
		h.inputListener = NewInputListener(h.hwnd)

		// Start input listener
		if err := h.inputListener.Start(); err != nil {
			errChan <- err
			return
		}

		// Register configured hotkeys
		h.registerConfiguredHotkeys()

		// Add clipboard format listener
		if err := h.clipboardWatcher.Start(); err != nil {
			errChan <- err
			return
		}

		// Initialize system tray if not in silent mode
		if !h.cfg.Get().App.Silent {
			h.tray = NewTray(h.hwnd)
			if err := h.tray.Setup(""); err != nil {
				logger.Error("Failed to initialize system tray: %v", err)
			}
		}

		// Notify that initialization was successful
		close(errChan)

		// Run message loop
		h.messageLoop()

		// Cleanup after message loop exits
		h.clipboardWatcher.Stop()
		h.inputListener.Stop()
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
	id, _, err := h.CaptureHotkeyWithDisplay(timeout)
	return id, err
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

	case WM_CLIPBOARDUPDATE:
		logger.Info("WM_CLIPBOARDUPDATE received")
		h.onClipboardUpdate()
		return 0

	case WM_RELOAD_CONFIG:
		logger.Info("WM_RELOAD_CONFIG received, reloading hotkeys...")
		// Unregister all existing signatures
		h.inputListener.GetMatcher().UnregisterAll()
		// Re-register configured hotkeys
		h.registerConfiguredHotkeys()
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
