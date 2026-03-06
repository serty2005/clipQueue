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

	matcher             *SignatureMatcher
	pendingMouseHotkeys map[byte]func()

	// Режим захвата
	captureMode atomic.Bool
	captureChan chan InputSignature

	sequenceRecordMode atomic.Bool
	sequenceRecordHKL  uintptr
	sequenceRecordAt   time.Time
	sequenceLastEvent  time.Time
	sequenceEvents     []RecordedKeyEvent

	mu sync.Mutex
}

// NewInputListener создаёт новый слушатель ввода
func NewInputListener(hwnd uintptr) *InputListener {
	return &InputListener{
		hwnd:                hwnd,
		matcher:             NewSignatureMatcher(),
		pendingMouseHotkeys: make(map[byte]func()),
		captureChan:         make(chan InputSignature, 1),
	}
}

func (l *InputListener) storePendingMouseHotkey(button byte, callback func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pendingMouseHotkeys[button] = callback
}

func (l *InputListener) consumePendingMouseHotkey(button byte) func() {
	l.mu.Lock()
	defer l.mu.Unlock()
	callback := l.pendingMouseHotkeys[button]
	delete(l.pendingMouseHotkeys, button)
	return callback
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

func (l *InputListener) StartSequenceRecording() {
	_, _, hkl := getForegroundKeyboardContext()

	l.mu.Lock()
	l.sequenceEvents = nil
	l.sequenceRecordHKL = hkl
	l.sequenceRecordAt = time.Now()
	l.sequenceLastEvent = time.Time{}
	l.mu.Unlock()

	l.sequenceRecordMode.Store(true)
	logger.Info("Sequence recording started (HKL=0x%X)", hkl)
}

func (l *InputListener) StopSequenceRecording() (*RecordedSequence, error) {
	l.sequenceRecordMode.Store(false)

	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.sequenceEvents) == 0 {
		return nil, fmt.Errorf("no sequence events recorded")
	}

	events := make([]RecordedKeyEvent, len(l.sequenceEvents))
	copy(events, l.sequenceEvents)

	seq := &RecordedSequence{
		Version:     1,
		RecordedAt:  l.sequenceRecordAt,
		RecordedHKL: uint64(l.sequenceRecordHKL),
		Events:      events,
	}

	logger.Info("Sequence recording stopped: events=%d", len(events))
	return seq, nil
}

func (l *InputListener) GetSequenceRecordingStatus(lastN int) SequenceRecordingStatus {
	if lastN <= 0 {
		lastN = 20
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	total := len(l.sequenceEvents)
	start := 0
	if total > lastN {
		start = total - lastN
	}

	events := make([]RecordedKeyEvent, total-start)
	copy(events, l.sequenceEvents[start:])

	return SequenceRecordingStatus{
		Active:      l.sequenceRecordMode.Load(),
		EventCount:  total,
		RecordedHKL: uint64(l.sequenceRecordHKL),
		Events:      events,
	}
}

func (l *InputListener) recordKeyboardEvent(kb *KBDLLHOOKSTRUCT, wParam uintptr) {
	if !l.sequenceRecordMode.Load() {
		return
	}
	if kb.Flags&llkhfInjected != 0 {
		return
	}

	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.sequenceRecordMode.Load() {
		return
	}

	var delta uint32
	if !l.sequenceLastEvent.IsZero() {
		d := now.Sub(l.sequenceLastEvent) / time.Millisecond
		if d < 0 {
			d = 0
		}
		if d > 60000 {
			d = 60000
		}
		delta = uint32(d)
	}

	l.sequenceEvents = append(l.sequenceEvents, RecordedKeyEvent{
		VK:        uint16(kb.VkCode),
		ScanCode:  uint16(kb.ScanCode),
		HookFlags: kb.Flags,
		Message:   uint32(wParam),
		DelayMs:   delta,
	})
	l.sequenceLastEvent = now
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
			l.recordKeyboardEvent(kb, wParam)

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
				rawData = []byte{1, mouseButtonEdgeDown}
				shouldProcess = true

			case WM_RBUTTONDOWN:
				sourceType = SourceMouseButton
				rawData = []byte{2, mouseButtonEdgeDown}
				shouldProcess = true

			case WM_MBUTTONDOWN:
				sourceType = SourceMouseButton
				rawData = []byte{3, mouseButtonEdgeDown}
				shouldProcess = true

			case WM_XBUTTONDOWN:
				sourceType = SourceMouseButton
				xButton := (mouse.MouseData >> 16) & 0xFFFF
				if xButton == XBUTTON1 {
					rawData = []byte{4, mouseButtonEdgeDown}
				} else if xButton == XBUTTON2 {
					rawData = []byte{5, mouseButtonEdgeDown}
				} else {
					rawData = []byte{byte(xButton + 3), mouseButtonEdgeDown}
				}
				shouldProcess = true

			case WM_LBUTTONUP:
				sourceType = SourceMouseButton
				rawData = []byte{1, mouseButtonEdgeUp}
				shouldProcess = true

			case WM_RBUTTONUP:
				sourceType = SourceMouseButton
				rawData = []byte{2, mouseButtonEdgeUp}
				shouldProcess = true

			case WM_MBUTTONUP:
				sourceType = SourceMouseButton
				rawData = []byte{3, mouseButtonEdgeUp}
				shouldProcess = true

			case WM_XBUTTONUP:
				sourceType = SourceMouseButton
				xButton := (mouse.MouseData >> 16) & 0xFFFF
				if xButton == XBUTTON1 {
					rawData = []byte{4, mouseButtonEdgeUp}
				} else if xButton == XBUTTON2 {
					rawData = []byte{5, mouseButtonEdgeUp}
				} else {
					rawData = []byte{byte(xButton + 3), mouseButtonEdgeUp}
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
					if sourceType == SourceMouseButton {
						_, edge, ok := decodeMouseButtonRawData(rawData)
						if ok && edge == mouseButtonEdgeDown {
							logger.Debug("Capture waiting for mouse button release: %s", sig.DisplayHint)
							return 1
						}
					}
					l.captureMode.Store(false)

					select {
					case l.captureChan <- sig:
					default:
					}

					logger.Info("Captured mouse: %s (hash=0x%X)", sig.DisplayHint, sig.Hash)
					return 1
				}

				// Режим сопоставления
				if sourceType == SourceMouseButton {
					button, edge, ok := decodeMouseButtonRawData(rawData)
					if ok && edge == mouseButtonEdgeDown {
						probe := NewInputSignature(sourceType, []byte{button, mouseButtonEdgeUp}, mods)
						if callback := l.matcher.Match(&probe); callback != nil {
							l.storePendingMouseHotkey(button, callback)
							logger.Debug("Matched mouse down, waiting for release: %s", sig.DisplayHint)
							return 1
						}
					}
					if ok && edge == mouseButtonEdgeUp {
						if callback := l.consumePendingMouseHotkey(button); callback != nil {
							logger.Debug("Matched mouse up: %s", sig.DisplayHint)
							go callback()
							return 1
						}
					}
				}
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
