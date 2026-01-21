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
	KEYEVENTF_KEYUP   = 0x0002
	KEYEVENTF_UNICODE = 0x0004
)

// GetAsyncKeyState checks if a key is currently pressed
func getAsyncKeyState(vkCode uint16) bool {
	// Use the GetAsyncKeyState function from hook.go
	result := GetAsyncKeyState(uint32(vkCode))
	return result < 0 // If the most significant bit is set, the key is pressed
}

var (
	procVkKeyScan = user32.NewProc("VkKeyScanW")
)

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
	procSendInput = user32.NewProc("SendInput")
)

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

// TypeString sends text to the active window using physical keystrokes when possible
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
		// Check if character is ASCII or Unicode
		if r < 128 {
			// ASCII character - use VkKeyScanW method
			res, _, _ := procVkKeyScan.Call(uintptr(r))
			scanResult := int16(res)

			if scanResult != -1 {
				// Extract VK code and modifiers
				vkCode := uint16(scanResult & 0xFF)
				modifiers := uint16(scanResult >> 8)

				// Process modifiers (Shift, Ctrl, Alt)
				var pressedModifiers []uint16
				if modifiers&1 != 0 { // Shift
					inputs = append(inputs, INPUT{
						Type: INPUT_KEYBOARD,
						Ki: KEYBDINPUT{
							Wvk: VK_SHIFT,
						},
					})
					pressedModifiers = append(pressedModifiers, VK_SHIFT)
				}
				if modifiers&2 != 0 { // Ctrl
					inputs = append(inputs, INPUT{
						Type: INPUT_KEYBOARD,
						Ki: KEYBDINPUT{
							Wvk: VK_CONTROL,
						},
					})
					pressedModifiers = append(pressedModifiers, VK_CONTROL)
				}
				if modifiers&4 != 0 { // Alt
					inputs = append(inputs, INPUT{
						Type: INPUT_KEYBOARD,
						Ki: KEYBDINPUT{
							Wvk: VK_MENU,
						},
					})
					pressedModifiers = append(pressedModifiers, VK_MENU)
				}

				// Key down
				inputs = append(inputs, INPUT{
					Type: INPUT_KEYBOARD,
					Ki: KEYBDINPUT{
						Wvk: vkCode,
					},
				})

				// Key up
				inputs = append(inputs, INPUT{
					Type: INPUT_KEYBOARD,
					Ki: KEYBDINPUT{
						Wvk:     vkCode,
						DwFlags: KEYEVENTF_KEYUP,
					},
				})

				// Release modifiers in reverse order
				for i := len(pressedModifiers) - 1; i >= 0; i-- {
					inputs = append(inputs, INPUT{
						Type: INPUT_KEYBOARD,
						Ki: KEYBDINPUT{
							Wvk:     pressedModifiers[i],
							DwFlags: KEYEVENTF_KEYUP,
						},
					})
				}
			}
		} else {
			// Unicode character (e.g., Cyrillic) - use Unicode method only
			utf16Char := uint16(r)

			// Key down event
			inputs = append(inputs, INPUT{
				Type: INPUT_KEYBOARD,
				Ki: KEYBDINPUT{
					WScan:   utf16Char,
					DwFlags: KEYEVENTF_UNICODE,
				},
			})

			// Key up event
			inputs = append(inputs, INPUT{
				Type: INPUT_KEYBOARD,
				Ki: KEYBDINPUT{
					WScan:   utf16Char,
					DwFlags: KEYEVENTF_UNICODE | KEYEVENTF_KEYUP,
				},
			})
		}
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

// PasteString sends text to the active window using clipboard paste
func PasteString(text string) error {
	// Save current clipboard content
	oldContent, err := Read()
	if err != nil {
		logger.Error("Failed to read current clipboard: %v", err)
		return err
	}

	// Write text to clipboard
	content := ClipboardContent{
		Type: Text,
		Text: text,
	}
	if err := Write(content); err != nil {
		logger.Error("Failed to write text to clipboard: %v", err)
		return err
	}

	// Send Ctrl+V to paste
	if err := SendCtrlV(); err != nil {
		logger.Error("Failed to send Ctrl+V: %v", err)
		return err
	}

	// Wait for paste to complete
	time.Sleep(150 * time.Millisecond)

	// Restore original clipboard content
	if err := Write(oldContent); err != nil {
		logger.Error("Failed to restore clipboard: %v", err)
		return err
	}

	return nil
}

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
