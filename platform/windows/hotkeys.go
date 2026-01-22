package windows

import (
	"strings"

	"github.com/serty2005/clipqueue/internal/config"
	"github.com/serty2005/clipqueue/internal/logger"
)

const (
	hotkeyToggleQueueID = 1
	hotkeyPasteNextID   = 2
	hotkeyMacroStartID  = 3 // Starting ID for macros to avoid overlap with predefined hotkeys
)

const (
	MOD_ALT     = 0x0001
	MOD_CONTROL = 0x0002
	MOD_SHIFT   = 0x0004
	MOD_WIN     = 0x0008
)

var (
	procRegisterHotKey   = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey = user32.NewProc("UnregisterHotKey")
)

type Hotkeys struct {
	host      *Host
	cfg       *config.SafeConfig
	callbacks map[uint32]func() // Maps hotkey ID to callback function
	nextID    uint32            // Next available ID for dynamic hotkeys
}

func NewHotkeys(host *Host) (*Hotkeys, error) {
	return &Hotkeys{
		host:      host,
		cfg:       host.cfg,
		callbacks: make(map[uint32]func()),
		nextID:    hotkeyMacroStartID,
	}, nil
}

func (h *Hotkeys) Register() error {
	// Get configuration
	cfg := h.cfg.Get()

	// Parse and register ToggleQueue hotkey
	toggleQueueMod, toggleQueueVK, err := h.parseHotkey(cfg.Hotkeys.ToggleQueue)
	if err != nil {
		logger.Error("Failed to parse ToggleQueue hotkey: %v", err)
		return err
	}
	if err := h.registerHotkey(hotkeyToggleQueueID, toggleQueueMod, toggleQueueVK); err != nil {
		return err
	}
	h.callbacks[hotkeyToggleQueueID] = func() { h.host.onToggleQueue() }

	// Parse and register PasteNext hotkey
	pasteNextMod, pasteNextVK, err := h.parseHotkey(cfg.Hotkeys.PasteNext)
	if err != nil {
		logger.Error("Failed to parse PasteNext hotkey: %v", err)
		return err
	}
	if err := h.registerHotkey(hotkeyPasteNextID, pasteNextMod, pasteNextVK); err != nil {
		return err
	}
	h.callbacks[hotkeyPasteNextID] = func() { h.host.onPasteNext() }

	return nil
}

func (h *Hotkeys) Unregister() error {
	// Unregister all hotkeys
	for id := range h.callbacks {
		if err := h.unregisterHotkey(id); err != nil {
			logger.Error("Failed to unregister hotkey %d: %v", id, err)
		}
	}
	h.callbacks = make(map[uint32]func())
	h.nextID = hotkeyMacroStartID

	return nil
}

// parseHotkey parses a hotkey string into modifiers and virtual key code
func (h *Hotkeys) parseHotkey(hotkeyString string) (uint32, uint32, error) {
	var modifiers uint32
	var vk uint32
	foundKey := false

	// Split hotkey string into parts
	parts := strings.Split(strings.ToUpper(hotkeyString), "+")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		switch part {
		case "CTRL", "CONTROL":
			modifiers |= MOD_CONTROL
		case "ALT":
			modifiers |= MOD_ALT
		case "SHIFT":
			modifiers |= MOD_SHIFT
		case "WIN":
			modifiers |= MOD_WIN
		default:
			// Lookup key in keyMap
			if code, exists := keyMap[part]; exists {
				vk = code
				foundKey = true
			} else {
				logger.Error("Unknown key: %s", part)
				return 0, 0, nil
			}
		}
	}

	if !foundKey {
		logger.Error("No valid key found in hotkey: %s", hotkeyString)
		return 0, 0, nil
	}

	return modifiers, vk, nil
}

func (h *Hotkeys) ParseAndRegister(hotkeyString string, callback func()) (uint32, error) {
	modifiers, vk, err := h.parseHotkey(hotkeyString)
	if err != nil {
		return 0, err
	}

	id := h.nextID
	h.nextID++

	if err := h.registerHotkey(id, modifiers, vk); err != nil {
		logger.Error("Failed to register hotkey %s: %v", hotkeyString, err)
		return 0, err
	}

	h.callbacks[id] = callback
	logger.Info("Registered hotkey %s with ID %d", hotkeyString, id)

	return id, nil
}

func (h *Hotkeys) GetCallback(id uint32) (func(), bool) {
	callback, exists := h.callbacks[id]
	return callback, exists
}

func (h *Hotkeys) registerHotkey(id uint32, modifiers uint32, vk uint32) error {
	ret, _, err := procRegisterHotKey.Call(h.host.hwnd, uintptr(id), uintptr(modifiers), uintptr(vk))
	if ret == 0 {
		logger.Error("RegisterHotKey failed (err=%v)", err)
		return err
	}
	return nil
}

func (h *Hotkeys) unregisterHotkey(id uint32) error {
	ret, _, err := procUnregisterHotKey.Call(h.host.hwnd, uintptr(id))
	if ret == 0 {
		return err
	}
	return nil
}
