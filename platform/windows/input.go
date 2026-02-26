package windows

import (
	"syscall"
	"time"
	"unsafe"

	"github.com/serty2005/clipqueue/internal/logger"
)

const (
	// Input type constants
	INPUT_KEYBOARD = 1

	// Virtual key codes
	VK_CONTROL = 0x11
	VK_V       = 0x56
	VK_MENU    = 0x12 // Alt key
	VK_SHIFT   = 0x10

	// Keyboard event flags
	KEYEVENTF_EXTENDEDKEY = 0x0001
	KEYEVENTF_KEYUP       = 0x0002
	KEYEVENTF_UNICODE     = 0x0004
	KEYEVENTF_SCANCODE    = 0x0008

	// MapVirtualKey constants
	MAPVK_VK_TO_VSC = 0
)

// GetAsyncKeyState checks if a key is currently pressed
func getAsyncKeyState(vkCode uint16) bool {
	// Use the GetAsyncKeyState function from hook.go
	result := GetAsyncKeyState(uint32(vkCode))
	return result < 0 // If the most significant bit is set, the key is pressed
}

// INPUT represents the Windows INPUT structure for SendInput
// https://learn.microsoft.com/en-us/windows/win32/api/winuser/ns-winuser-input
// Size on x64 must be 40 bytes to match C union alignment
type INPUT struct {
	Type    uint32
	Ki      KEYBDINPUT
	Padding [8]byte // Padding to ensure size matches MOUSEINPUT alignment on x64
}

// KEYBDINPUT represents the Windows KEYBDINPUT structure
type KEYBDINPUT struct {
	Wvk         uint16
	WScan       uint16
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

var (
	procSendInput                = user32.NewProc("SendInput")
	procVkKeyScanW               = user32.NewProc("VkKeyScanW")
	procVkKeyScanExW             = user32.NewProc("VkKeyScanExW")
	procMapVirtualKeyW           = user32.NewProc("MapVirtualKeyW")
	procGetKeyboardLayout        = user32.NewProc("GetKeyboardLayout")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
)

func describeVkKeyScanModifiers(mods byte) string {
	names := ""
	if mods&0x01 != 0 {
		names += "SHIFT|"
	}
	if mods&0x02 != 0 {
		names += "CTRL|"
	}
	if mods&0x04 != 0 {
		names += "ALT|"
	}
	if mods&0x08 != 0 {
		names += "HANKAKU|"
	}
	if names == "" {
		return "none"
	}
	return names[:len(names)-1]
}

func getForegroundKeyboardContext() (hwnd uintptr, threadID uint32, hkl uintptr) {
	hwnd, _, _ = procGetForegroundWindow.Call()
	if hwnd == 0 {
		return 0, 0, 0
	}

	tid, _, _ := procGetWindowThreadProcessId.Call(hwnd, 0)
	threadID = uint32(tid)

	layout, _, _ := procGetKeyboardLayout.Call(uintptr(threadID))
	hkl = layout
	return
}

func appendUnicodeRuneInputs(inputs *[]INPUT, r rune) {
	utf16Char := uint16(r)
	*inputs = append(*inputs,
		INPUT{
			Type: INPUT_KEYBOARD,
			Ki: KEYBDINPUT{
				WScan:   utf16Char,
				DwFlags: KEYEVENTF_UNICODE,
			},
		},
		INPUT{
			Type: INPUT_KEYBOARD,
			Ki: KEYBDINPUT{
				WScan:   utf16Char,
				DwFlags: KEYEVENTF_UNICODE | KEYEVENTF_KEYUP,
			},
		},
	)
}

func appendVirtualKeyInput(inputs *[]INPUT, vk uint16, keyUp bool) {
	flags := uint32(0)
	if keyUp {
		flags = KEYEVENTF_KEYUP
	}
	*inputs = append(*inputs, INPUT{
		Type: INPUT_KEYBOARD,
		Ki: KEYBDINPUT{
			Wvk:     vk,
			DwFlags: flags,
		},
	})
}

// SendInput sends input events to the system
func sendInput(inputs []INPUT) uint32 {
	cInputs := uint32(len(inputs))
	pInputs := uintptr(unsafe.Pointer(&inputs[0]))

	ret, _, _ := procSendInput.Call(
		uintptr(cInputs),
		pInputs,
		uintptr(unsafe.Sizeof(INPUT{})),
	)

	return uint32(ret)
}

// TypeString sends text to the active window using Unicode injection for all characters
func TypeString(text string) error {
	var inputs []INPUT

	// Release any stuck modifier keys before sending text
	modifierKeys := []struct {
		vkCode uint16
		name   string
	}{
		{VK_SHIFT, "Shift"},
		{VK_CONTROL, "Control"},
		{VK_MENU, "Alt"},
		{0x5B, "Left Windows"},
		{0x5C, "Right Windows"},
	}

	for _, mod := range modifierKeys {
		if getAsyncKeyState(mod.vkCode) {
			logger.Debug("Releasing stuck modifier: %s", mod.name)
			inputs = append(inputs, INPUT{
				Type: INPUT_KEYBOARD,
				Ki: KEYBDINPUT{
					Wvk:     mod.vkCode,
					DwFlags: KEYEVENTF_KEYUP,
				},
			})
		}
	}

	for _, r := range text {
		appendUnicodeRuneInputs(&inputs, r)
	}

	// Send inputs in chunks with delays for RDP sessions
	const chunkSize = 50
	for i := 0; i < len(inputs); i += chunkSize {
		end := i + chunkSize
		if end > len(inputs) {
			end = len(inputs)
		}

		chunk := inputs[i:end]
		result := sendInput(chunk)
		if result != uint32(len(chunk)) {
			logger.Error("SendInput failed: only %d out of %d inputs sent", result, len(chunk))
			return syscall.GetLastError()
		}

		// Add delay to "humanize" input for RDP sessions
		time.Sleep(20 * time.Millisecond)
	}

	logger.Debug("TypeString completed successfully: %s", text)
	return nil
}

// TypeStringHardware sends text to the active window using hardware key events (scan codes)
func TypeStringHardware(text string) error {
	var inputs []INPUT
	hwnd, threadID, hkl := getForegroundKeyboardContext()
	logger.Debug("TypeStringHardware start: textLen=%d, fgHwnd=0x%X, fgThreadID=%d, fgHKL=0x%X",
		len([]rune(text)), hwnd, threadID, hkl)
	mappedCount := 0
	fallbackUnicodeCount := 0

	// Release any stuck modifier keys before sending text
	modifierKeys := []struct {
		vkCode uint16
		name   string
	}{
		{VK_SHIFT, "Shift"},
		{VK_CONTROL, "Control"},
		{VK_MENU, "Alt"},
		{0x5B, "Left Windows"},
		{0x5C, "Right Windows"},
	}

	for _, mod := range modifierKeys {
		if getAsyncKeyState(mod.vkCode) {
			logger.Debug("Releasing stuck modifier: %s", mod.name)
			inputs = append(inputs, INPUT{
				Type: INPUT_KEYBOARD,
				Ki: KEYBDINPUT{
					Wvk:     mod.vkCode,
					DwFlags: KEYEVENTF_KEYUP,
				},
			})
		}
	}

	for idx, r := range text {
		// Get virtual key code and shift state for the character
		var vkAndShift uintptr
		if hkl != 0 {
			vkAndShift, _, _ = procVkKeyScanExW.Call(uintptr(r), hkl)
		} else {
			vkAndShift, _, _ = procVkKeyScanW.Call(uintptr(r))
		}
		vkScanShort := int16(uint16(vkAndShift))
		unmappable := vkScanShort == -1
		vkScanRaw := uint16(vkScanShort)
		vk := uint16(vkScanRaw & 0x00FF)
		mods := byte((vkScanRaw >> 8) & 0x00FF)
		shift := (mods & 0x01) != 0

		// Get scan code for the virtual key
		sc, _, _ := procMapVirtualKeyW.Call(uintptr(vk), MAPVK_VK_TO_VSC)
		scanCode := uint16(sc)

		logger.Debug("TypeStringHardware map[%d]: rune=%q U+%04X vkScan=0x%04X signed=%d unmappable=%v vk=0x%02X mods=0x%02X(%s) scan=0x%02X",
			idx, r, r, vkScanRaw, vkScanShort, unmappable, vk, mods, describeVkKeyScanModifiers(mods), scanCode)

		if unmappable || vk == 0 || scanCode == 0 || (mods&^byte(0x07)) != 0 {
			fallbackUnicodeCount++
			logger.Debug("TypeStringHardware fallback[%d]: rune=%q reason=unmappable_or_unsupported", idx, r)
			appendUnicodeRuneInputs(&inputs, r)
			continue
		}

		if mods&0x02 != 0 {
			appendVirtualKeyInput(&inputs, VK_CONTROL, false)
		}
		if mods&0x04 != 0 {
			appendVirtualKeyInput(&inputs, VK_MENU, false)
		}

		if shift {
			appendVirtualKeyInput(&inputs, VK_SHIFT, false)
		}

		// Key down event
		inputs = append(inputs, INPUT{
			Type: INPUT_KEYBOARD,
			Ki: KEYBDINPUT{
				Wvk:     vk,
				WScan:   scanCode,
				DwFlags: KEYEVENTF_SCANCODE,
			},
		})

		// Key up event
		inputs = append(inputs, INPUT{
			Type: INPUT_KEYBOARD,
			Ki: KEYBDINPUT{
				Wvk:     vk,
				WScan:   scanCode,
				DwFlags: KEYEVENTF_SCANCODE | KEYEVENTF_KEYUP,
			},
		})

		if shift {
			appendVirtualKeyInput(&inputs, VK_SHIFT, true)
		}
		if mods&0x04 != 0 {
			appendVirtualKeyInput(&inputs, VK_MENU, true)
		}
		if mods&0x02 != 0 {
			appendVirtualKeyInput(&inputs, VK_CONTROL, true)
		}
		mappedCount++
	}

	// Send inputs in chunks with delays for RDP sessions
	const chunkSize = 50
	for i := 0; i < len(inputs); i += chunkSize {
		end := i + chunkSize
		if end > len(inputs) {
			end = len(inputs)
		}

		chunk := inputs[i:end]
		result := sendInput(chunk)
		if result != uint32(len(chunk)) {
			logger.Error("SendInput failed: only %d out of %d inputs sent", result, len(chunk))
			return syscall.GetLastError()
		}

		// Add delay to "humanize" input for RDP sessions
		time.Sleep(20 * time.Millisecond)
	}

	logger.Debug("TypeStringHardware summary: mapped=%d fallbackUnicode=%d", mappedCount, fallbackUnicodeCount)
	logger.Debug("TypeStringHardware completed successfully: %s", text)
	return nil
}

// SendCtrlV sends the Ctrl+V keystroke combination to the system
// SendCtrlV sends the Ctrl+V keystroke combination to the system
func SendCtrlV() error {
	// Create Ctrl+V keystroke sequence
	// First, ensure any Alt key (from Alt+V hotkey) is released
	inputs := []INPUT{
		{
			Type: INPUT_KEYBOARD,
			Ki: KEYBDINPUT{
				Wvk:     VK_MENU,
				DwFlags: KEYEVENTF_KEYUP,
			},
		},
		{
			Type: INPUT_KEYBOARD,
			Ki: KEYBDINPUT{
				Wvk: VK_CONTROL,
			},
		},
	}

	// Send Ctrl down
	result := sendInput(inputs)
	if result != uint32(len(inputs)) {
		logger.Error("SendInput failed (Ctrl down): only %d out of %d inputs sent", result, len(inputs))
		return syscall.GetLastError()
	}

	// Small delay between Ctrl down and V down to prevent keystroke merging
	time.Sleep(10 * time.Millisecond)

	// Send V down and up, then Ctrl up
	inputs = []INPUT{
		{
			Type: INPUT_KEYBOARD,
			Ki: KEYBDINPUT{
				Wvk: VK_V,
			},
		},
		{
			Type: INPUT_KEYBOARD,
			Ki: KEYBDINPUT{
				Wvk:     VK_V,
				DwFlags: KEYEVENTF_KEYUP,
			},
		},
		{
			Type: INPUT_KEYBOARD,
			Ki: KEYBDINPUT{
				Wvk:     VK_CONTROL,
				DwFlags: KEYEVENTF_KEYUP,
			},
		},
	}

	result = sendInput(inputs)
	if result != uint32(len(inputs)) {
		logger.Error("SendInput failed: only %d out of %d inputs sent", result, len(inputs))
		return syscall.GetLastError()
	}

	logger.Debug("SendCtrlV completed successfully")
	return nil
}
