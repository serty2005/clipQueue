package config

import (
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

type Macro struct {
	Text string `yaml:"text" json:"text"`
	Mode string `yaml:"mode" json:"mode"` // "type" (default) or "paste"
}

// UnmarshalYAML implements custom YAML unmarshaling for backward compatibility
func (m *Macro) UnmarshalYAML(value *yaml.Node) error {
	// Handle case 1: Macro is a string (old format)
	var str string
	if err := value.Decode(&str); err == nil {
		m.Text = str
		m.Mode = "type" // Default mode if not specified
		return nil
	}

	// Handle case 2: Macro is an object (new format)
	type Alias Macro // Create alias type to avoid recursion
	alias := &struct {
		*Alias
	}{
		Alias: (*Alias)(m),
	}
	if err := value.Decode(alias); err != nil {
		return err
	}

	// Set default mode if not specified
	if m.Mode == "" {
		m.Mode = "type"
	}

	return nil
}

type Config struct {
	App struct {
		DataDir string `yaml:"data_dir"`
	} `yaml:"app"`
	Hotkeys struct {
		ToggleQueue string `yaml:"toggle_queue"`
		PasteNext   string `yaml:"paste_next"`
	} `yaml:"hotkeys"`
	Clipboard struct {
		WatchDebounceMs int `yaml:"watch_debounce_ms"`
		PasteDelayMs    int `yaml:"paste_delay_ms"`
		RestoreDelayMs  int `yaml:"restore_delay_ms"`
	} `yaml:"clipboard"`
	Queue struct {
		DefaultOrder string `yaml:"default_order"`
	} `yaml:"queue"`
	Macros map[string]Macro `yaml:"macros"`
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

// Get returns a deep copy of the current config for safe reading
func (sc *SafeConfig) Get() *Config {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	// Create a deep copy to ensure that the original config isn't modified
	copyCfg := defaultConfig()
	*copyCfg = *sc.cfg
	copyCfg.Macros = make(map[string]Macro, len(sc.cfg.Macros))
	for k, v := range sc.cfg.Macros {
		copyCfg.Macros[k] = v
	}
	return copyCfg
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
		sc.cfg.Macros = make(map[string]Macro)
	}
	return nil
}

func defaultConfig() *Config {
	cfg := &Config{}
	cfg.App.DataDir = filepath.Join(os.Getenv("APPDATA"), "ClipQueue")
	cfg.Hotkeys.ToggleQueue = "Alt+C"
	cfg.Hotkeys.PasteNext = "Alt+V"
	cfg.Clipboard.WatchDebounceMs = 30
	cfg.Clipboard.PasteDelayMs = 150
	cfg.Clipboard.RestoreDelayMs = 1000
	cfg.Queue.DefaultOrder = "LIFO"
	cfg.Macros = make(map[string]Macro)
	return cfg
}

func Load() (*Config, error) {
	// Check if config file exists
	if _, err := os.Stat("config.yml"); os.IsNotExist(err) {
		// Create default config
		cfg := defaultConfig()
		if err := saveConfig(cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	// Read existing config file
	data, err := os.ReadFile("config.yml")
	if err != nil {
		return nil, err
	}

	// Parse YAML
	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// Ensure data dir exists
	if err := os.MkdirAll(cfg.App.DataDir, 0755); err != nil {
		return nil, err
	}

	return cfg, nil
}

func saveConfig(cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile("config.yml", data, 0644)
}
