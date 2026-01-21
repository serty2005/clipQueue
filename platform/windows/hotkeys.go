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

// keyMap maps string key representations to virtual key codes
var keyMap = map[string]uint32{
	// Letters
	"A": 0x41, "B": 0x42, "C": 0x43, "D": 0x44, "E": 0x45, "F": 0x46, "G": 0x47,
	"H": 0x48, "I": 0x49, "J": 0x4A, "K": 0x4B, "L": 0x4C, "M": 0x4D, "N": 0x4E,
	"O": 0x4F, "P": 0x50, "Q": 0x51, "R": 0x52, "S": 0x53, "T": 0x54, "U": 0x55,
	"V": 0x56, "W": 0x57, "X": 0x58, "Y": 0x59, "Z": 0x5A,

	// Numbers
	"0": 0x30, "1": 0x31, "2": 0x32, "3": 0x33, "4": 0x34,
	"5": 0x35, "6": 0x36, "7": 0x37, "8": 0x38, "9": 0x39,

	// Function keys
	"F1": 0x70, "F2": 0x71, "F3": 0x72, "F4": 0x73,
	"F5": 0x74, "F6": 0x75, "F7": 0x76, "F8": 0x77,
	"F9": 0x78, "F10": 0x79, "F11": 0x7A, "F12": 0x7B,

	// Media and volume keys
	"VOLUMEMUTE":        0xAD,
	"VOLUMEDOWN":        0xAE,
	"VOLUMEUP":          0xAF,
	"MEDIANEXTTRACK":    0xB0,
	"MEDIAPREVTRACK":    0xB1,
	"MEDIASTOP":         0xB2,
	"MEDIAPLAYPAUSE":    0xB3,
	"LAUNCHMAIL":        0xB4,
	"LAUNCHMEDIASELECT": 0xB5,
	"LAUNCHAPP1":        0xB6,
	"LAUNCHAPP2":        0xB7,

	// Aliases for JavaScript compatibility (AudioVolume* format)
	"AUDIOVOLUMEMUTE": 0xAD,
	"AUDIOVOLUMEDOWN": 0xAE,
	"AUDIOVOLUMEUP":   0xAF,
	"GRAVE": 0xC0,
	"TILDE": 0xC0,
}

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
