package windows

import (
	"sync/atomic"
	"syscall"
	"unsafe"
)

var (
	procSetWindowsHookEx    = user32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procGetAsyncKeyState    = user32.NewProc("GetAsyncKeyState")
)

const (
	WH_KEYBOARD_LL = 13

	WM_KEYDOWN    = 0x0100
	WM_SYSKEYDOWN = 0x0104

	VK_LCONTROL = 0xA2
	VK_RCONTROL = 0xA3
	VK_LMENU    = 0xA4
	VK_RMENU    = 0xA5
	VK_LSHIFT   = 0xA0
	VK_RSHIFT   = 0xA1
	VK_LWIN     = 0x5B
	VK_RWIN     = 0x5C

	VK_VOLUME_MUTE         = 0xAD
	VK_VOLUME_DOWN         = 0xAE
	VK_VOLUME_UP           = 0xAF
	VK_MEDIA_NEXT_TRACK    = 0xB0
	VK_MEDIA_PREV_TRACK    = 0xB1
	VK_MEDIA_STOP          = 0xB2
	VK_MEDIA_PLAY_PAUSE    = 0xB3
	VK_LAUNCH_MAIL         = 0xB4
	VK_LAUNCH_MEDIA_SELECT = 0xB5
	VK_LAUNCH_APP1         = 0xB6
	VK_LAUNCH_APP2         = 0xB7
)

// KBDLLHOOKSTRUCT contains information about a low-level keyboard input event
type KBDLLHOOKSTRUCT struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

// LowLevelKeyboardProc is the callback function type for WH_KEYBOARD_LL
type LowLevelKeyboardProc func(nCode int, wParam uintptr, lParam uintptr) uintptr

var (
	hookHandle   uintptr
	hookCallback LowLevelKeyboardProc
	captureDone  atomic.Bool // Indicates if capture completed
)

// SetHook sets the low-level keyboard hook with a callback for captured hotkeys
func SetHook(onCapture func(string)) (uintptr, error) {
	hookCallback = func(nCode int, wParam uintptr, lParam uintptr) uintptr {
		if nCode >= 0 && (wParam == WM_KEYDOWN || wParam == WM_SYSKEYDOWN) {
			kbdStruct := (*KBDLLHOOKSTRUCT)(unsafe.Pointer(lParam))

			// Get key string
			keyStr := virtualKeyToString(kbdStruct.VkCode)

			// Check if it's a modifier key (we don't want to capture just modifiers)
			isModifier := false
			switch kbdStruct.VkCode {
			case VK_LCONTROL, VK_RCONTROL, VK_LMENU, VK_RMENU, VK_LSHIFT, VK_RSHIFT, VK_LWIN, VK_RWIN:
				isModifier = true
			}

			if !isModifier && keyStr != "" {
				// Get modifiers
				modifiers := getModifiers()

				// Build hotkey string
				var hotkeyParts []string
				hotkeyParts = append(hotkeyParts, modifiers...)
				hotkeyParts = append(hotkeyParts, keyStr)
				hotkey := joinParts(hotkeyParts)

				// Call the capture callback
				onCapture(hotkey)

				// Signal capture is done
				captureDone.Store(true)

				// Block the event
				return 1
			}
		}

		return CallNextHook(nCode, wParam, lParam)
	}

	handle, _, err := procSetWindowsHookEx.Call(
		WH_KEYBOARD_LL,
		syscall.NewCallback(hookCallback),
		0,
		0,
	)
	if handle == 0 {
		return 0, err
	}
	hookHandle = handle
	return handle, nil
}

// Unhook removes the low-level keyboard hook
func Unhook(handle uintptr) error {
	if handle != 0 {
		ret, _, err := procUnhookWindowsHookEx.Call(handle)
		if ret == 0 {
			return err
		}
	}
	return nil
}

// CallNextHook calls the next hook in the chain
func CallNextHook(nCode int, wParam, lParam uintptr) uintptr {
	ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return ret
}

// GetAsyncKeyState checks the state of a key
func GetAsyncKeyState(vkCode uint32) int16 {
	ret, _, _ := procGetAsyncKeyState.Call(uintptr(vkCode))
	return int16(ret)
}

// isKeyDown checks if a key is currently pressed
func isKeyDown(vkCode uint32) bool {
	ret := GetAsyncKeyState(vkCode)
	return ret < 0 // If the most significant bit is set, the key is pressed
}

// getModifiers returns the current state of modifiers (CTRL, ALT, SHIFT, WIN)
func getModifiers() []string {
	var modifiers []string

	if isKeyDown(VK_LCONTROL) || isKeyDown(VK_RCONTROL) {
		modifiers = append(modifiers, "CTRL")
	}
	if isKeyDown(VK_LMENU) || isKeyDown(VK_RMENU) {
		modifiers = append(modifiers, "ALT")
	}
	if isKeyDown(VK_LSHIFT) || isKeyDown(VK_RSHIFT) {
		modifiers = append(modifiers, "SHIFT")
	}
	if isKeyDown(VK_LWIN) || isKeyDown(VK_RWIN) {
		modifiers = append(modifiers, "WIN")
	}

	return modifiers
}

// virtualKeyToString maps virtual key codes to string representations
func virtualKeyToString(vkCode uint32) string {
	// First check if it's a standard key in keyMap (from hotkeys.go)
	for name, code := range keyMap {
		if code == vkCode {
			return name
		}
	}

	// Check media/volume keys
	switch vkCode {
	case VK_VOLUME_MUTE:
		return "VOLUMEMUTE"
	case VK_VOLUME_DOWN:
		return "VOLUMEDOWN"
	case VK_VOLUME_UP:
		return "VOLUMEUP"
	case VK_MEDIA_NEXT_TRACK:
		return "MEDIANEXTTRACK"
	case VK_MEDIA_PREV_TRACK:
		return "MEDIAPREVTRACK"
	case VK_MEDIA_STOP:
		return "MEDIASTOP"
	case VK_MEDIA_PLAY_PAUSE:
		return "MEDIAPLAYPAUSE"
	case VK_LAUNCH_MAIL:
		return "LAUNCHMAIL"
	case VK_LAUNCH_MEDIA_SELECT:
		return "LAUNCHMEDIASELECT"
	case VK_LAUNCH_APP1:
		return "LAUNCHAPP1"
	case VK_LAUNCH_APP2:
		return "LAUNCHAPP2"
	}

	return ""
}

// joinParts joins string parts with "+" separator
func joinParts(parts []string) string {
	var result string
	for i, part := range parts {
		if i > 0 {
			result += "+"
		}
		result += part
	}
	return result
}
