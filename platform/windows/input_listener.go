package windows

import (
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/serty2005/clipqueue/internal/logger"
)

// ===============================
// UNIFIED INPUT LISTENER
// ===============================

// InputListener слушает весь низкоуровневый ввод и генерирует сигнатуры
type InputListener struct {
	hwnd         uintptr
	keyboardHook uintptr
	mouseHook    uintptr

	matcher *SignatureMatcher

	// Режим захвата
	captureMode atomic.Bool
	captureChan chan InputSignature

	mu sync.Mutex
}

// NewInputListener создаёт новый слушатель ввода
func NewInputListener(hwnd uintptr) *InputListener {
	return &InputListener{
		hwnd:        hwnd,
		matcher:     NewSignatureMatcher(),
		captureChan: make(chan InputSignature, 1),
	}
}

// GetMatcher возвращает матчер для регистрации сигнатур
func (l *InputListener) GetMatcher() *SignatureMatcher {
	return l.matcher
}

// Start запускает прослушивание ввода
func (l *InputListener) Start() error {
	var err error

	// Устанавливаем клавиатурный хук
	l.keyboardHook, err = l.setKeyboardHook()
	if err != nil {
		return fmt.Errorf("failed to set keyboard hook: %w", err)
	}

	// Устанавливаем мышиный хук
	l.mouseHook, err = l.setMouseHook()
	if err != nil {
		Unhook(l.keyboardHook)
		return fmt.Errorf("failed to set mouse hook: %w", err)
	}

	logger.Info("Input listener started")
	return nil
}

// Stop останавливает прослушивание
func (l *InputListener) Stop() error {
	if l.keyboardHook != 0 {
		Unhook(l.keyboardHook)
		l.keyboardHook = 0
	}
	if l.mouseHook != 0 {
		Unhook(l.mouseHook)
		l.mouseHook = 0
	}
	logger.Info("Input listener stopped")
	return nil
}

// StartCapture начинает захват следующего ввода
func (l *InputListener) StartCapture() {
	// Очищаем канал
	select {
	case <-l.captureChan:
	default:
	}

	l.captureMode.Store(true)
	logger.Info("Capture mode started")
}

// StopCapture останавливает захват
func (l *InputListener) StopCapture() {
	l.captureMode.Store(false)
	logger.Info("Capture mode stopped")
}

// WaitForCapture ожидает захваченную сигнатуру
func (l *InputListener) WaitForCapture(timeout time.Duration) (*InputSignature, error) {
	select {
	case sig := <-l.captureChan:
		return &sig, nil
	case <-time.After(timeout):
		l.StopCapture()
		return nil, fmt.Errorf("capture timeout")
	}
}

// getCurrentModifiers получает текущее состояние модификаторов
func (l *InputListener) getCurrentModifiers() uint8 {
	var mods uint8

	if isKeyDown(VK_LCONTROL) || isKeyDown(VK_RCONTROL) {
		mods |= ModCtrl
	}
	if isKeyDown(VK_LMENU) || isKeyDown(VK_RMENU) {
		mods |= ModAlt
	}
	if isKeyDown(VK_LSHIFT) || isKeyDown(VK_RSHIFT) {
		mods |= ModShift
	}
	if isKeyDown(VK_LWIN) || isKeyDown(VK_RWIN) {
		mods |= ModWin
	}

	return mods
}

// setKeyboardHook устанавливает низкоуровневый клавиатурный хук
func (l *InputListener) setKeyboardHook() (uintptr, error) {
	callback := func(nCode int, wParam uintptr, lParam uintptr) uintptr {
		if nCode >= 0 && (wParam == WM_KEYDOWN || wParam == WM_SYSKEYDOWN || wParam == WM_KEYUP || wParam == WM_SYSKEYUP) {
			kb := (*KBDLLHOOKSTRUCT)(unsafe.Pointer(lParam))

			// Игнорируем чистые модификаторы
			if l.isModifierKey(kb.VkCode) {
				return CallNextHook(nCode, wParam, lParam)
			}

			// Создаём сырые данные: VK + ScanCode + Flags
			rawData := make([]byte, 10)
			binary.LittleEndian.PutUint16(rawData[0:2], uint16(kb.VkCode))
			binary.LittleEndian.PutUint16(rawData[2:4], uint16(kb.ScanCode))
			binary.LittleEndian.PutUint32(rawData[4:8], kb.Flags)
			binary.LittleEndian.PutUint16(rawData[8:10], uint16(wParam))

			mods := l.getCurrentModifiers()
			sig := NewInputSignature(SourceKeyboard, rawData, mods)

			// Режим захвата
			if l.captureMode.Load() {
				l.captureMode.Store(false)

				select {
				case l.captureChan <- sig:
				default:
				}

				logger.Info("Captured keyboard: %s (hash=0x%X)", sig.DisplayHint, sig.Hash)
				return 1 // Блокируем
			}

			// Режим сопоставления
			if callback := l.matcher.Match(&sig); callback != nil {
				logger.Debug("Matched keyboard: %s", sig.DisplayHint)
				go callback()
				return 1 // Блокируем
			}
		}

		return CallNextHook(nCode, wParam, lParam)
	}

	handle, _, err := procSetWindowsHookEx.Call(
		WH_KEYBOARD_LL,
		syscall.NewCallback(callback),
		0,
		0,
	)

	if handle == 0 {
		return 0, err
	}

	return handle, nil
}

// setMouseHook устанавливает низкоуровневый мышиный хук
func (l *InputListener) setMouseHook() (uintptr, error) {
	callback := func(nCode int, wParam uintptr, lParam uintptr) uintptr {
		if nCode >= 0 {
			mouse := (*MSLLHOOKSTRUCT)(unsafe.Pointer(lParam))

			var sourceType InputSourceType
			var rawData []byte
			shouldProcess := false

			switch wParam {
			case WM_LBUTTONDOWN:
				sourceType = SourceMouseButton
				rawData = []byte{1}
				shouldProcess = true

			case WM_RBUTTONDOWN:
				sourceType = SourceMouseButton
				rawData = []byte{2}
				shouldProcess = true

			case WM_MBUTTONDOWN:
				sourceType = SourceMouseButton
				rawData = []byte{3}
				shouldProcess = true

			case WM_XBUTTONDOWN:
				sourceType = SourceMouseButton
				xButton := (mouse.MouseData >> 16) & 0xFFFF
				if xButton == XBUTTON1 {
					rawData = []byte{4}
				} else if xButton == XBUTTON2 {
					rawData = []byte{5}
				} else {
					// Дополнительные кнопки
					rawData = make([]byte, 6)
					rawData[0] = byte(xButton + 3)
					binary.LittleEndian.PutUint32(rawData[1:5], mouse.MouseData)
					rawData[5] = byte(xButton)
				}
				shouldProcess = true

			case WM_LBUTTONUP:
				sourceType = SourceMouseButton
				rawData = []byte{1}
				shouldProcess = true

			case WM_RBUTTONUP:
				sourceType = SourceMouseButton
				rawData = []byte{2}
				shouldProcess = true

			case WM_MBUTTONUP:
				sourceType = SourceMouseButton
				rawData = []byte{3}
				shouldProcess = true

			case WM_XBUTTONUP:
				sourceType = SourceMouseButton
				xButton := (mouse.MouseData >> 16) & 0xFFFF
				if xButton == XBUTTON1 {
					rawData = []byte{4}
				} else if xButton == XBUTTON2 {
					rawData = []byte{5}
				} else {
					// Дополнительные кнопки
					rawData = make([]byte, 6)
					rawData[0] = byte(xButton + 3)
					binary.LittleEndian.PutUint32(rawData[1:5], mouse.MouseData)
					rawData[5] = byte(xButton)
				}
				shouldProcess = true

			case WM_MOUSEWHEEL:
				sourceType = SourceMouseWheel
				delta := int16(mouse.MouseData >> 16)
				rawData = make([]byte, 3)
				binary.LittleEndian.PutUint16(rawData[0:2], uint16(delta))
				rawData[2] = 0 // Вертикальное
				shouldProcess = true

			case WM_MOUSEHWHEEL:
				sourceType = SourceMouseWheel
				delta := int16(mouse.MouseData >> 16)
				rawData = make([]byte, 3)
				binary.LittleEndian.PutUint16(rawData[0:2], uint16(delta))
				rawData[2] = 1 // Горизонтальное
				shouldProcess = true
			}

			if shouldProcess {
				mods := l.getCurrentModifiers()
				sig := NewInputSignature(sourceType, rawData, mods)

				// Режим захвата
				if l.captureMode.Load() {
					l.captureMode.Store(false)

					select {
					case l.captureChan <- sig:
					default:
					}

					logger.Info("Captured mouse: %s (hash=0x%X)", sig.DisplayHint, sig.Hash)
					return 1
				}

				// Режим сопоставления
				if callback := l.matcher.Match(&sig); callback != nil {
					logger.Debug("Matched mouse: %s", sig.DisplayHint)
					go callback()
					return 1
				}
			}
		}

		return CallNextHook(nCode, wParam, lParam)
	}

	handle, _, err := procSetWindowsHookEx.Call(
		WH_MOUSE_LL,
		syscall.NewCallback(callback),
		0,
		0,
	)

	if handle == 0 {
		return 0, err
	}

	return handle, nil
}

// isModifierKey проверяет, является ли клавиша модификатором
func (l *InputListener) isModifierKey(vkCode uint32) bool {
	switch vkCode {
	case VK_LCONTROL, VK_RCONTROL, VK_LMENU, VK_RMENU,
		VK_LSHIFT, VK_RSHIFT, VK_LWIN, VK_RWIN:
		return true
	}
	return false
}

// ===============================
// КОНСТАНТЫ
// ===============================

const (
	WH_MOUSE_LL = 14

	WM_LBUTTONDOWN = 0x0201
	WM_LBUTTONUP   = 0x0202
	WM_RBUTTONDOWN = 0x0204
	WM_RBUTTONUP   = 0x0205
	WM_MBUTTONDOWN = 0x0207
	WM_MBUTTONUP   = 0x0208
	WM_XBUTTONDOWN = 0x020B
	WM_XBUTTONUP   = 0x020C
	WM_MOUSEWHEEL  = 0x020A
	WM_MOUSEHWHEEL = 0x020E

	XBUTTON1 = 0x0001
	XBUTTON2 = 0x0002
)

// MSLLHOOKSTRUCT структура для WH_MOUSE_LL
type MSLLHOOKSTRUCT struct {
	Pt          POINT
	MouseData   uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type POINT struct {
	X int32
	Y int32
}
