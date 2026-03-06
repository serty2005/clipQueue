package config

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

var keyMap = map[string]uint32{
	"A": 0x41, "B": 0x42, "C": 0x43, "D": 0x44, "E": 0x45, "F": 0x46, "G": 0x47,
	"H": 0x48, "I": 0x49, "J": 0x4A, "K": 0x4B, "L": 0x4C, "M": 0x4D, "N": 0x4E,
	"O": 0x4F, "P": 0x50, "Q": 0x51, "R": 0x52, "S": 0x53, "T": 0x54, "U": 0x55,
	"V": 0x56, "W": 0x57, "X": 0x58, "Y": 0x59, "Z": 0x5A,
	"0": 0x30, "1": 0x31, "2": 0x32, "3": 0x33, "4": 0x34,
	"5": 0x35, "6": 0x36, "7": 0x37, "8": 0x38, "9": 0x39,
	"F1": 0x70, "F2": 0x71, "F3": 0x72, "F4": 0x73,
	"F5": 0x74, "F6": 0x75, "F7": 0x76, "F8": 0x77,
	"F9": 0x78, "F10": 0x79, "F11": 0x7A, "F12": 0x7B,
	"VOLUMEMUTE": 0xAD, "VOLUMEDOWN": 0xAE, "VOLUMEUP": 0xAF,
	"MEDIANEXTTRACK": 0xB0, "MEDIAPREVTRACK": 0xB1, "MEDIASTOP": 0xB2,
	"MEDIAPLAYPAUSE": 0xB3, "LAUNCHMAIL": 0xB4, "LAUNCHMEDIASELECT": 0xB5,
	"LAUNCHAPP1": 0xB6, "LAUNCHAPP2": 0xB7,
	"AUDIOVOLUMEMUTE": 0xAD, "AUDIOVOLUMEDOWN": 0xAE, "AUDIOVOLUMEUP": 0xAF,
	"GRAVE": 0xC0, "TILDE": 0xC0,
}

var yamlQuotedYKeyPattern = regexp.MustCompile(`(?m)^(\s*)"y":`)

const (
	MOD_ALT                        = 0x0001
	MOD_CONTROL                    = 0x0002
	MOD_SHIFT                      = 0x0004
	MOD_WIN                        = 0x0008
	SourceKeyboard InputSourceType = iota
)

type InputSourceType uint8

const (
	ModCtrl  uint8 = 1 << 0
	ModAlt   uint8 = 1 << 1
	ModShift uint8 = 1 << 2
	ModWin   uint8 = 1 << 3
)

func parseHotkey(hotkeyString string) (uint32, uint32, error) {
	var modifiers uint32
	var vk uint32
	foundKey := false
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
			if code, exists := keyMap[part]; exists {
				vk = code
				foundKey = true
			} else {
				return 0, 0, fmt.Errorf("unknown key: %s", part)
			}
		}
	}
	if !foundKey {
		return 0, 0, fmt.Errorf("no valid key found in hotkey: %s", hotkeyString)
	}
	return modifiers, vk, nil
}
func generateSignatureFromHotkey(hotkeyString string) (string, error) {
	modifiers, vk, err := parseHotkey(hotkeyString)
	if err != nil {
		return "", err
	}
	rawData := make([]byte, 2)
	binary.LittleEndian.PutUint16(rawData, uint16(vk))
	sourceType := SourceKeyboard
	modifierState := uint8(modifiers)
	h := fnv.New64a()
	h.Write([]byte{byte(sourceType)})
	h.Write([]byte{modifierState})
	h.Write(rawData)
	buf := &bytes.Buffer{}
	buf.WriteByte(1)
	buf.WriteByte(byte(sourceType))
	buf.WriteByte(modifierState)
	binary.Write(buf, binary.LittleEndian, uint16(len(rawData)))
	buf.Write(rawData)
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

type Macro struct {
	Name                    string `yaml:"name" json:"name"`
	Hotkey                  string `yaml:"hotkey" json:"hotkey"`
	Signature               string `yaml:"signature" json:"signature"`
	Enabled                 bool   `yaml:"enabled,omitempty" json:"enabled"`
	Text                    string `yaml:"text" json:"text"`
	Sequence                string `yaml:"sequence,omitempty" json:"sequence,omitempty"`
	SequenceNormalizeDelays bool   `yaml:"sequence_normalize_delays,omitempty" json:"sequenceNormalizeDelays,omitempty"`
	SequenceDelayMs         int    `yaml:"sequence_delay_ms,omitempty" json:"sequenceDelayMs,omitempty"`
	Mode                    string `yaml:"mode" json:"mode"` // "type" (default), "paste", "type_hw", or "sequence"
}

// UnmarshalYAML implements custom YAML unmarshaling for backward compatibility
func (m *Macro) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		// Decode scalar node into Text and set Mode to "type"
		if err := value.Decode(&m.Text); err != nil {
			return err
		}
		m.Mode = "type"
	case yaml.MappingNode:
		type macroDecoded struct {
			Name                    string `yaml:"name"`
			Hotkey                  string `yaml:"hotkey"`
			Signature               string `yaml:"signature"`
			Enabled                 *bool  `yaml:"enabled"`
			Text                    string `yaml:"text"`
			Sequence                string `yaml:"sequence"`
			SequenceNormalizeDelays bool   `yaml:"sequence_normalize_delays"`
			SequenceDelayMs         int    `yaml:"sequence_delay_ms"`
			Mode                    string `yaml:"mode"`
		}
		var aux macroDecoded
		if err := value.Decode(&aux); err != nil {
			return err
		}
		m.Name = aux.Name
		m.Hotkey = aux.Hotkey
		m.Signature = aux.Signature
		m.Text = aux.Text
		m.Sequence = aux.Sequence
		m.SequenceNormalizeDelays = aux.SequenceNormalizeDelays
		m.SequenceDelayMs = aux.SequenceDelayMs
		m.Mode = aux.Mode
		if aux.Enabled == nil {
			m.Enabled = true
		} else {
			m.Enabled = *aux.Enabled
		}
		if m.Mode == "" {
			m.Mode = "type"
		}
	default:
		return fmt.Errorf("unsupported YAML node kind for Macro: %v", value.Kind)
	}
	return nil
}

type oldConfig struct {
	App struct {
		DataDir string `yaml:"data_dir" json:"dataDir"`
		Silent  bool   `yaml:"silent" json:"silent"`
		Logs    bool   `yaml:"logs" json:"logs"`
	} `yaml:"app"`
	Hotkeys struct {
		ToggleQueue      string `yaml:"toggle_queue" json:"toggleQueue"`
		PasteNext        string `yaml:"paste_next" json:"pasteNext"`
		ToggleQueueOrder string `yaml:"toggle_queue_order" json:"toggleQueueOrder"`
	} `yaml:"hotkeys"`
	Clipboard struct {
		WatchDebounceMs int `yaml:"watch_debounce_ms" json:"watchDebounceMs"`
		PasteDelayMs    int `yaml:"paste_delay_ms" json:"pasteDelayMs"`
		RestoreDelayMs  int `yaml:"restore_delay_ms" json:"restoreDelayMs"`
	} `yaml:"clipboard"`
	Queue struct {
		DefaultOrder string `yaml:"default_order" json:"defaultOrder"`
	} `yaml:"queue"`
	Macros map[string]Macro `yaml:"macros"`
}

type UIConfig struct {
	Visible   bool `yaml:"visible" json:"visible"`
	HasBounds bool `yaml:"has_bounds" json:"hasBounds"`
	X         int  `yaml:"x" json:"x"`
	Y         int  `yaml:"y" json:"y"`
	Width     int  `yaml:"width" json:"width"`
	Height    int  `yaml:"height" json:"height"`
}

type Config struct {
	App struct {
		DataDir string `yaml:"data_dir" json:"dataDir"`
		Silent  bool   `yaml:"silent" json:"silent"`
		Logs    bool   `yaml:"logs" json:"logs"`
	} `yaml:"app" json:"app"`
	Hotkeys struct {
		ToggleQueue             string `yaml:"toggle_queue" json:"toggleQueue"`
		PasteNext               string `yaml:"paste_next" json:"pasteNext"`
		ToggleQueueOrder        string `yaml:"toggle_queue_order" json:"toggleQueueOrder"`
		ToggleUI                string `yaml:"toggle_ui" json:"toggleUI"`
		ToggleQueueDisplay      string `yaml:"toggle_queue_display" json:"toggleQueueDisplay"`
		PasteNextDisplay        string `yaml:"paste_next_display" json:"pasteNextDisplay"`
		ToggleQueueOrderDisplay string `yaml:"toggle_queue_order_display" json:"toggleQueueOrderDisplay"`
		ToggleUIDisplay         string `yaml:"toggle_ui_display" json:"toggleUIDisplay"`
	} `yaml:"hotkeys" json:"hotkeys"`
	Clipboard struct {
		WatchDebounceMs int `yaml:"watch_debounce_ms" json:"watchDebounceMs"`
		PasteDelayMs    int `yaml:"paste_delay_ms" json:"pasteDelayMs"`
		RestoreDelayMs  int `yaml:"restore_delay_ms" json:"restoreDelayMs"`
	} `yaml:"clipboard" json:"clipboard"`
	Queue struct {
		DefaultOrder string `yaml:"default_order" json:"defaultOrder"`
	} `yaml:"queue" json:"queue"`
	Features struct {
		EnableQueue     bool `yaml:"enable_queue" json:"enableQueue"`
		EnableClipboard bool `yaml:"enable_clipboard" json:"enableClipboard"`
		EnableMacros    bool `yaml:"enable_macros" json:"enableMacros"`
		EnableLab       bool `yaml:"enable_lab" json:"enableLab"`
	} `yaml:"features" json:"features"`
	UI     UIConfig `yaml:"ui" json:"ui"`
	Macros []Macro  `yaml:"macros" json:"macros"`
}

// SafeConfig wraps Config with RWMutex for thread-safe access
type SafeConfig struct {
	mu  sync.RWMutex
	cfg *Config
}

// NewSafeConfig creates a new SafeConfig instance
func NewSafeConfig(cfg *Config) *SafeConfig {
	return &SafeConfig{
		cfg: cfg,
	}
}

func executableDir() string {
	exePath, err := os.Executable()
	if err != nil || exePath == "" {
		return "."
	}
	return filepath.Dir(exePath)
}

func ConfigPath() string {
	return filepath.Join(executableDir(), "config.yml")
}

func ResolvePath(path string) string {
	if path == "" {
		return executableDir()
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Clean(filepath.Join(executableDir(), path))
}

func cloneConfig(src *Config) *Config {
	copyCfg := defaultConfig()
	*copyCfg = *src
	copyCfg.Macros = make([]Macro, len(src.Macros))
	copy(copyCfg.Macros, src.Macros)
	return copyCfg
}

// Get returns a deep copy of the current config for safe reading
func (sc *SafeConfig) Get() *Config {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	return cloneConfig(sc.cfg)
}

// Update updates the config with a new config value and saves it to disk
func (sc *SafeConfig) Update(newCfg *Config) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	// Save to disk before updating in memory
	if err := saveConfig(newCfg); err != nil {
		return err
	}

	*sc.cfg = *newCfg
	if sc.cfg.Macros == nil {
		sc.cfg.Macros = []Macro{}
	}
	return nil
}

// Mutate safely applies a partial config update and saves it to disk.
func (sc *SafeConfig) Mutate(fn func(cfg *Config)) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	nextCfg := cloneConfig(sc.cfg)
	fn(nextCfg)

	if err := saveConfig(nextCfg); err != nil {
		return err
	}

	*sc.cfg = *nextCfg
	if sc.cfg.Macros == nil {
		sc.cfg.Macros = []Macro{}
	}
	return nil
}

func defaultConfig() *Config {
	cfg := &Config{}
	cfg.App.DataDir = "."
	cfg.App.Silent = false
	cfg.App.Logs = false
	cfg.Hotkeys.ToggleQueueDisplay = "Ctrl+Alt+C"
	cfg.Hotkeys.PasteNextDisplay = "Ctrl+Alt+V"
	cfg.Hotkeys.ToggleQueue = "sig:AQADCgBDAC4AAAAAAAAB"
	cfg.Hotkeys.PasteNext = "sig:AQADCgBWAC8AAAAAAAAB"
	cfg.Hotkeys.ToggleQueueOrder = ""
	cfg.Hotkeys.ToggleUI = ""
	cfg.Hotkeys.ToggleQueueOrderDisplay = ""
	cfg.Hotkeys.ToggleUIDisplay = ""
	cfg.Clipboard.WatchDebounceMs = 30
	cfg.Clipboard.PasteDelayMs = 50
	cfg.Clipboard.RestoreDelayMs = 250
	cfg.Queue.DefaultOrder = "LIFO"
	cfg.Features.EnableQueue = true
	cfg.Features.EnableClipboard = true
	cfg.Features.EnableMacros = true
	cfg.Features.EnableLab = false
	cfg.UI.Visible = false
	cfg.UI.HasBounds = false
	cfg.UI.Width = 500
	cfg.UI.Height = 300
	cfg.Macros = []Macro{}
	return cfg
}

func EnsureSignatures(cfg *Config) error {
	if cfg.Hotkeys.ToggleQueue == "" && cfg.Hotkeys.ToggleQueueDisplay != "" {
		sig, err := generateSignatureFromHotkey(cfg.Hotkeys.ToggleQueueDisplay)
		if err != nil {
			return err
		}
		cfg.Hotkeys.ToggleQueue = sig
	}
	if cfg.Hotkeys.PasteNext == "" && cfg.Hotkeys.PasteNextDisplay != "" {
		sig, err := generateSignatureFromHotkey(cfg.Hotkeys.PasteNextDisplay)
		if err != nil {
			return err
		}
		cfg.Hotkeys.PasteNext = sig
	}
	if cfg.Hotkeys.ToggleQueueOrder == "" && cfg.Hotkeys.ToggleQueueOrderDisplay != "" {
		sig, err := generateSignatureFromHotkey(cfg.Hotkeys.ToggleQueueOrderDisplay)
		if err != nil {
			return err
		}
		cfg.Hotkeys.ToggleQueueOrder = sig
	}
	if cfg.Hotkeys.ToggleUI == "" && cfg.Hotkeys.ToggleUIDisplay != "" {
		sig, err := generateSignatureFromHotkey(cfg.Hotkeys.ToggleUIDisplay)
		if err != nil {
			return err
		}
		cfg.Hotkeys.ToggleUI = sig
	}
	return nil
}

func validateConfig(cfg *Config) error {
	validModes := map[string]bool{
		"type":     true,
		"paste":    true,
		"type_hw":  true,
		"sequence": true,
	}
	for i, macro := range cfg.Macros {
		if macro.Hotkey == "" {
			return fmt.Errorf("macro %d has empty hotkey", i)
		}
		if macro.Signature == "" {
			return fmt.Errorf("macro %d has empty signature", i)
		}
		sig := macro.Signature
		if strings.HasPrefix(sig, "sig:") {
			sig = strings.TrimPrefix(sig, "sig:")
		}
		if _, err := base64.StdEncoding.DecodeString(sig); err != nil {
			return fmt.Errorf("macro %d has invalid signature: %v", i, err)
		}
		if macro.Sequence != "" {
			if _, err := base64.StdEncoding.DecodeString(macro.Sequence); err != nil {
				return fmt.Errorf("macro %d has invalid sequence: %v", i, err)
			}
		}
		if !validModes[macro.Mode] {
			return fmt.Errorf("macro %d has invalid mode: %s", i, macro.Mode)
		}
	}
	return nil
}

func Load() (*Config, error) {
	configPath := ConfigPath()

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Create default config
		cfg := defaultConfig()
		if err := EnsureSignatures(cfg); err != nil {
			return nil, err
		}
		if err := saveConfig(cfg); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(ResolvePath(cfg.App.DataDir), 0755); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	// Read existing config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	// Try to parse as old config with map[string]Macro
	oldCfg := &oldConfig{}
	if err := yaml.Unmarshal(data, oldCfg); err == nil && len(oldCfg.Macros) > 0 {
		// Migration: convert map to slice
		cfg := defaultConfig()
		cfg.App = oldCfg.App
		cfg.Hotkeys.ToggleQueue = oldCfg.Hotkeys.ToggleQueue
		cfg.Hotkeys.PasteNext = oldCfg.Hotkeys.PasteNext
		cfg.Hotkeys.ToggleQueueOrder = oldCfg.Hotkeys.ToggleQueueOrder
		cfg.Clipboard = oldCfg.Clipboard
		cfg.Queue = oldCfg.Queue
		cfg.Macros = make([]Macro, 0, len(oldCfg.Macros))
		for sig, macro := range oldCfg.Macros {
			generatedSig, err := generateSignatureFromHotkey(sig)
			if err != nil {
				return nil, fmt.Errorf("failed to generate signature for hotkey %s: %v", sig, err)
			}
			cfg.Macros = append(cfg.Macros, Macro{
				Name:      sig,
				Hotkey:    sig,
				Signature: generatedSig,
				Enabled:   true,
				Text:      macro.Text,
				Mode:      macro.Mode,
			})
		}
		if err := validateConfig(cfg); err != nil {
			return nil, err
		}
		// Save migrated config
		if err := saveConfig(cfg); err != nil {
			return nil, err
		}
		// Ensure data dir exists
		if err := os.MkdirAll(ResolvePath(cfg.App.DataDir), 0755); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	// Parse as new config
	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	// Ensure data dir exists
	if err := os.MkdirAll(ResolvePath(cfg.App.DataDir), 0755); err != nil {
		return nil, err
	}

	return cfg, nil
}

func saveConfig(cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	normalized := yamlQuotedYKeyPattern.ReplaceAllString(string(data), "${1}y:")

	return os.WriteFile(ConfigPath(), []byte(normalized), 0644)
}
