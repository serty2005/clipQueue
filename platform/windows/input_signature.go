package windows

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"
)

// ===============================
// INPUT SIGNATURE
// ===============================

// InputSourceType определяет источник ввода
type InputSourceType uint8

const (
	SourceKeyboard InputSourceType = iota
	SourceMouseButton
	SourceMouseWheel
	SourceHID
	SourceUnknown
)

func (s InputSourceType) String() string {
	switch s {
	case SourceKeyboard:
		return "Keyboard"
	case SourceMouseButton:
		return "Mouse"
	case SourceMouseWheel:
		return "Wheel"
	case SourceHID:
		return "HID"
	default:
		return "Unknown"
	}
}

// InputSignature уникальная подпись события ввода
type InputSignature struct {
	Hash        uint64
	RawData     []byte
	SourceType  InputSourceType
	DisplayHint string
	RecordedAt  time.Time

	// Состояние Ctrl/Alt/Shift/Win в момент нажатия
	ModifierState uint8
}

// Флаги модификаторов
const (
	ModCtrl  uint8 = 1 << 0
	ModAlt   uint8 = 1 << 1
	ModShift uint8 = 1 << 2
	ModWin   uint8 = 1 << 3
)

// NewInputSignature создаёт сигнатуру из сырых данных
func NewInputSignature(sourceType InputSourceType, rawData []byte, modifiers uint8) InputSignature {
	sig := InputSignature{
		SourceType:    sourceType,
		RawData:       make([]byte, len(rawData)),
		ModifierState: modifiers,
		RecordedAt:    time.Now(),
	}
	copy(sig.RawData, rawData)

	// Вычисляем хеш включая модификаторы
	sig.Hash = sig.computeHash()
	sig.DisplayHint = sig.generateDisplayHint()

	return sig
}

// computeHash вычисляет хеш сигнатуры
func (s *InputSignature) computeHash() uint64 {
	h := fnv.New64a()

	// Включаем тип источника
	h.Write([]byte{byte(s.SourceType)})

	// Включаем модификаторы
	h.Write([]byte{s.ModifierState})

	// Включаем сырые данные
	h.Write(s.RawData)

	return h.Sum64()
}

// generateDisplayHint генерирует человекочитаемую подсказку
func (s *InputSignature) generateDisplayHint() string {
	var parts []string

	// Добавляем модификаторы
	if s.ModifierState&ModCtrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if s.ModifierState&ModAlt != 0 {
		parts = append(parts, "Alt")
	}
	if s.ModifierState&ModShift != 0 {
		parts = append(parts, "Shift")
	}
	if s.ModifierState&ModWin != 0 {
		parts = append(parts, "Win")
	}

	// Добавляем описание источника
	switch s.SourceType {
	case SourceKeyboard:
		if len(s.RawData) >= 2 {
			vk := binary.LittleEndian.Uint16(s.RawData[:2])
			if name := vkToName(uint32(vk)); name != "" {
				parts = append(parts, name)
			} else {
				parts = append(parts, fmt.Sprintf("Key[0x%X]", vk))
			}
		} else {
			parts = append(parts, "Key[?]")
		}

	case SourceMouseButton:
		if len(s.RawData) >= 1 {
			btn := s.RawData[0]
			parts = append(parts, fmt.Sprintf("Mouse%d", btn))
		} else {
			parts = append(parts, "Mouse[?]")
		}

	case SourceMouseWheel:
		if len(s.RawData) >= 2 {
			delta := int16(binary.LittleEndian.Uint16(s.RawData[:2]))
			if delta > 0 {
				parts = append(parts, "WheelUp")
			} else {
				parts = append(parts, "WheelDown")
			}
		} else {
			parts = append(parts, "Wheel[?]")
		}

	case SourceHID:
		if len(s.RawData) > 0 {
			parts = append(parts, fmt.Sprintf("HID[%X...]", s.RawData[0]))
		} else {
			parts = append(parts, "HID[?]")
		}

	default:
		parts = append(parts, fmt.Sprintf("Input[0x%X]", s.Hash&0xFFFF))
	}

	return strings.Join(parts, "+")
}

// Equals проверяет равенство двух сигнатур
func (s *InputSignature) Equals(other *InputSignature) bool {
	// Быстрая проверка по хешу
	if s.Hash != other.Hash {
		return false
	}

	// Проверка типа источника
	if s.SourceType != other.SourceType {
		return false
	}

	// Проверка модификаторов
	if s.ModifierState != other.ModifierState {
		return false
	}

	// Полная проверка сырых данных (на случай коллизии хешей)
	return bytes.Equal(s.RawData, other.RawData)
}

// ToBytes сериализует сигнатуру для сохранения в конфиг
func (s *InputSignature) ToBytes() []byte {
	buf := new(bytes.Buffer)

	// Версия формата (для обратной совместимости)
	buf.WriteByte(1)

	// Тип источника
	buf.WriteByte(byte(s.SourceType))

	// Модификаторы
	buf.WriteByte(s.ModifierState)

	// Длина сырых данных
	binary.Write(buf, binary.LittleEndian, uint16(len(s.RawData)))

	// Сырые данные
	buf.Write(s.RawData)

	return buf.Bytes()
}

// SignatureFromBytes десериализует сигнатуру
func SignatureFromBytes(data []byte) (*InputSignature, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("signature data too short")
	}

	buf := bytes.NewReader(data)

	// Версия
	version, _ := buf.ReadByte()
	if version != 1 {
		return nil, fmt.Errorf("unsupported signature version: %d", version)
	}

	sig := &InputSignature{}

	// Тип источника
	srcType, _ := buf.ReadByte()
	sig.SourceType = InputSourceType(srcType)

	// Модификаторы
	sig.ModifierState, _ = buf.ReadByte()

	// Длина данных
	var rawLen uint16
	binary.Read(buf, binary.LittleEndian, &rawLen)

	// Сырые данные
	sig.RawData = make([]byte, rawLen)
	buf.Read(sig.RawData)

	// Пересчитываем хеш и подсказку
	sig.Hash = sig.computeHash()
	sig.DisplayHint = sig.generateDisplayHint()

	return sig, nil
}

// ToBase64 для хранения в YAML/JSON конфиге
func (s *InputSignature) ToBase64() string {
	return base64.StdEncoding.EncodeToString(s.ToBytes())
}

// SignatureFromBase64 десериализует из base64
func SignatureFromBase64(encoded string) (*InputSignature, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	return SignatureFromBytes(data)
}

// ===============================
// SIGNATURE MATCHER
// ===============================

// SignatureMatcher сопоставляет входящие события с зарегистрированными сигнатурами
type SignatureMatcher struct {
	mu         sync.RWMutex
	signatures map[uint64][]*RegisteredSignature // Хеш -> список (для коллизий)
}

// RegisteredSignature связывает сигнатуру с callback
type RegisteredSignature struct {
	Signature InputSignature
	Callback  func()
	ID        string // Для идентификации в конфиге
}

// NewSignatureMatcher создаёт новый матчер
func NewSignatureMatcher() *SignatureMatcher {
	return &SignatureMatcher{
		signatures: make(map[uint64][]*RegisteredSignature),
	}
}

// Register регистрирует сигнатуру с callback
func (m *SignatureMatcher) Register(sig InputSignature, id string, callback func()) {
	m.mu.Lock()
	defer m.mu.Unlock()

	reg := &RegisteredSignature{
		Signature: sig,
		Callback:  callback,
		ID:        id,
	}

	m.signatures[sig.Hash] = append(m.signatures[sig.Hash], reg)
}

// Unregister удаляет сигнатуру по ID
func (m *SignatureMatcher) Unregister(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for hash, regs := range m.signatures {
		for i, reg := range regs {
			if reg.ID == id {
				m.signatures[hash] = append(regs[:i], regs[i+1:]...)
				if len(m.signatures[hash]) == 0 {
					delete(m.signatures, hash)
				}
				return
			}
		}
	}
}

// UnregisterAll удаляет все сигнатуры
func (m *SignatureMatcher) UnregisterAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.signatures = make(map[uint64][]*RegisteredSignature)
}

// Match проверяет сигнатуру и возвращает callback если найдено совпадение
func (m *SignatureMatcher) Match(sig *InputSignature) func() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	regs, ok := m.signatures[sig.Hash]
	if !ok {
		return nil
	}

	for _, reg := range regs {
		if reg.Signature.Equals(sig) {
			return reg.Callback
		}
	}

	return nil
}

// GetAll возвращает все зарегистрированные сигнатуры
func (m *SignatureMatcher) GetAll() []RegisteredSignature {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []RegisteredSignature
	for _, regs := range m.signatures {
		for _, reg := range regs {
			result = append(result, *reg)
		}
	}
	return result
}

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
	"GRAVE":           0xC0,
	"TILDE":           0xC0,
}

// vkToName пытается получить имя клавиши (только для отображения!)
func vkToName(vk uint32) string {
	for name, code := range keyMap {
		if code == vk {
			return name
		}
	}
	return ""
}
