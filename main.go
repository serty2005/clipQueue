package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/serty2005/clipqueue/internal/app"
	"github.com/serty2005/clipqueue/internal/config"
	"github.com/serty2005/clipqueue/internal/logger"
	"github.com/serty2005/clipqueue/internal/ui/server"
	"github.com/serty2005/clipqueue/internal/uihost"
	"github.com/serty2005/clipqueue/platform/windows"
)

func main() {
	_, statErr := os.Stat(config.ConfigPath())
	firstRun := os.IsNotExist(statErr)

	// Load config first
	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("Failed to load config: %v\n", err)
		return
	}

	// Hide console if silent mode is enabled
	if cfg.App.Silent {
		windows.HideConsole()
	}

	// Включаем DPI awareness до создания окон, чтобы избежать bitmap-масштабирования и размытия UI.
	windows.EnableHighDPIAwareness()

	// Initialize logger with silent parameter
	if err := logger.Init(cfg); err != nil {
		fmt.Printf("Failed to initialize logger: %v\n", err)
		return
	}
	defer logger.Close()

	logger.Info("ClipQueue starting...")
	logger.Info("Config loaded successfully")

	for key, macro := range cfg.Macros {
		logger.Info("Loaded macro: %s -> Text len: %d, Mode: %s", key, len(macro.Text), macro.Mode)
	}

	// Wrap config for thread-safe access
	safeCfg := config.NewSafeConfig(cfg)

	// Create controller for managing clipboard queue
	controller := app.NewController(safeCfg.Get())

	// Create Windows host
	host, err := windows.NewHost(safeCfg, controller)
	if err != nil {
		logger.Error("Failed to create Windows host: %v", err)
		return
	}

	// Create and start UI server
	uiServer := server.NewServer(safeCfg, host, controller)
	if err := uiServer.Start(); err != nil {
		logger.Error("Failed to start UI server: %v", err)
		return
	}
	uiURL := uiServer.GetURL()
	if firstRun {
		parsedURL, err := url.Parse(uiURL)
		if err == nil {
			query := parsedURL.Query()
			query.Set("screen", "set")
			query.Set("pane", "hotkeys")
			parsedURL.RawQuery = query.Encode()
			uiURL = parsedURL.String()
		}
	}
	uiHost := uihost.NewPreferredUIHost(uiURL, uihost.WindowState{
		Visible:   cfg.UI.Visible,
		HasBounds: cfg.UI.HasBounds,
		X:         cfg.UI.X,
		Y:         cfg.UI.Y,
		Width:     cfg.UI.Width,
		Height:    cfg.UI.Height,
	})
	if stateAware, ok := uiHost.(uihost.WindowStateAware); ok {
		stateAware.SetWindowStateHandler(func(state uihost.WindowState) {
			if err := safeCfg.Mutate(func(cfg *config.Config) {
				cfg.UI.Visible = state.Visible
				cfg.UI.HasBounds = state.HasBounds
				cfg.UI.X = state.X
				cfg.UI.Y = state.Y
				cfg.UI.Width = state.Width
				cfg.UI.Height = state.Height
			}); err != nil {
				logger.Warn("Failed to persist UI state: %v", err)
			}
		})
	}
	if nativeUI, ok := uiHost.(uihost.NativeBridgeCapable); ok {
		nativeUI.SetNativeBridge(&uihost.NativeBridge{
			GetUISnapshot: func() (interface{}, error) {
				return uiServer.GetUISnapshot(), nil
			},
			GetConfig: func() (interface{}, error) {
				return uiServer.NativeGetConfig(), nil
			},
			SaveConfig: func(cfgMap map[string]interface{}) (interface{}, error) {
				raw, err := json.Marshal(cfgMap)
				if err != nil {
					return nil, err
				}
				var cfg config.Config
				if err := json.Unmarshal(raw, &cfg); err != nil {
					return nil, err
				}
				return uiServer.NativeSaveConfig(cfg)
			},
			CaptureHotkey: func() (interface{}, error) {
				return uiServer.NativeCaptureHotkey()
			},
			GetHistory: func() (interface{}, error) {
				return uiServer.NativeGetHistory(), nil
			},
			GetQueueState: func() (interface{}, error) {
				return uiServer.NativeGetQueueState(), nil
			},
			ToggleQueue: func() (interface{}, error) {
				return uiServer.NativeToggleQueue(), nil
			},
			ToggleQueueOrder: func() (interface{}, error) {
				return uiServer.NativeToggleQueueOrder(), nil
			},
			ClearQueue: func() (interface{}, error) {
				return uiServer.NativeClearQueue(), nil
			},
			CopyHistoryItem: func(id string) (interface{}, error) {
				return uiServer.NativeCopyHistoryItem(id)
			},
			RemoveQueueItem: func(index int) (interface{}, error) {
				return uiServer.NativeRemoveQueueItem(index)
			},
			ParseLab: func(command string) (interface{}, error) {
				return uiServer.NativeParseLab(command)
			},
			BuildLab: func(stepsRaw []map[string]interface{}) (interface{}, error) {
				raw, err := json.Marshal(stepsRaw)
				if err != nil {
					return nil, err
				}
				var steps []server.CommandStepDTO
				if err := json.Unmarshal(raw, &steps); err != nil {
					return nil, err
				}
				return uiServer.NativeBuildLab(steps)
			},
			StartSequence: func() (interface{}, error) {
				return uiServer.NativeStartSequenceRecording()
			},
			StopSequence: func() (interface{}, error) {
				return uiServer.NativeStopSequenceRecording()
			},
			GetSequenceStatus: func(last int) (interface{}, error) {
				return uiServer.NativeGetSequenceStatus(last)
			},
		})
	}

	// Set config update callback to reload hotkeys
	uiServer.OnConfigUpdate = func() {
		logger.Info("Config updated, reloading hotkeys...")
		if err := host.ReloadConfig(); err != nil {
			logger.Error("Failed to reload config: %v", err)
		}
	}

	// Set controller state change callback to update tray tooltip
	controller.SetStateCallback(func(enabled bool, count int, mode string) {
		var tooltip string
		if enabled {
			tooltip = fmt.Sprintf("ClipQueue: ON [%s] (%d)", mode, count)
		} else {
			tooltip = "ClipQueue: OFF"
		}
		if err := host.UpdateTrayTooltip(tooltip); err != nil {
			logger.Error("Failed to update tray tooltip: %v", err)
		}
	})
	controller.SetUIRefreshCallback(func() {
		if nativeUI, ok := uiHost.(uihost.NativeBridgeCapable); ok {
			nativeUI.NotifyNativeStateChanged()
		}
	})
	controller.SetMacroInvokeCallback(func(name string, done bool) {
		if nativeUI, ok := uiHost.(uihost.NativeBridgeCapable); ok {
			nativeUI.NotifyNativeMacroInvoke(name, done)
		}
	})

	// Setup event handlers
	host.OnHotkeyToggleQueue(func() {
		logger.Debug("ToggleQueue hotkey pressed")
		go controller.ToggleQueue()
	})

	host.OnHotkeyToggleUI(func() {
		logger.Debug("ToggleUI hotkey pressed")
		go func() {
			if err := uiHost.Toggle(); err != nil {
				logger.Error("Failed to toggle UI host: %v", err)
			}
		}()
	})

	host.OnHotkeyToggleQueueOrder(func() {
		logger.Debug("ToggleQueueOrder hotkey pressed")
		go controller.ToggleOrder()
	})

	host.OnHotkeyPasteNext(func() {
		logger.Debug("PasteNext hotkey pressed")
		go controller.PasteNext()
	})

	// Setup clipboard update coalescing worker
	if cfg.Features.EnableClipboard || cfg.Features.EnableQueue {
		clipEvents := make(chan struct{}, 1)
		go func() {
			for range clipEvents {
				// Debounce
				time.Sleep(time.Duration(cfg.Clipboard.WatchDebounceMs) * time.Millisecond)
				// Drain extra events
			drainLoop:
				for {
					select {
					case <-clipEvents:
						// Skip extra event
					default:
						break drainLoop
					}
				}

				// Process clipboard update
				controller.OnClipboardUpdate()
			}
		}()

		host.OnClipboardUpdate(func() {
			logger.Debug("WM_CLIPBOARDUPDATE received")
			// Non-blocking send to clipEvents channel
			select {
			case clipEvents <- struct{}{}:
			default:
				// Skip if channel is full (already has pending event)
			}
		})
	}

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Setup tray command handler
	host.OnTrayCommand(func(id uint32) {
		switch id {
		case windows.ID_TRAY_INFO:
			logger.Info("Tray info command selected")
		case windows.ID_TRAY_TOGGLE_QUEUE:
			logger.Debug("Tray toggle queue command selected")
			go controller.ToggleQueue()
		case windows.ID_TRAY_SWITCH_ORDER:
			logger.Debug("Tray switch order command selected")
			go controller.ToggleOrder()
		case windows.ID_TRAY_CLEAR:
			logger.Debug("Tray clear queue command selected")
			go controller.ClearQueue()
		case windows.ID_TRAY_TOGGLE_UI:
			logger.Debug("Tray toggle UI command selected")
			if err := uiHost.Toggle(); err != nil {
				logger.Error("Failed to show UI host: %v", err)
			}
		case windows.ID_TRAY_EXIT:
			logger.Info("Tray exit command selected")
			// Send SIGTERM to trigger graceful shutdown
			sigChan <- syscall.SIGTERM
		}
	})

	// Start host (this will run the message loop in a goroutine)
	if err := host.Start(); err != nil {
		logger.Error("Failed to start Windows host: %v", err)
		return
	}
	logger.Info("Host started")

	if firstRun || cfg.UI.Visible {
		if err := uiHost.Show(); err != nil {
			logger.Warn("Failed to show UI host on startup: %v", err)
		}
	}

	<-sigChan

	if err := uiHost.Close(); err != nil {
		logger.Warn("Failed to close UI host: %v", err)
	}

	// Shutdown - correct order: first host, then server
	logger.Info("Host stopping...")
	if err := host.Stop(); err != nil {
		logger.Error("Failed to stop Windows host: %v", err)
	}

	// Wait for host to complete cleanup
	host.Wait()

	// Stop UI server with increased timeout (10 seconds instead of 5)
	logger.Info("Server stopping...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := uiServer.Stop(ctx); err != nil {
		logger.Error("Failed to stop UI server: %v", err)
	}

	logger.Info("ClipQueue stopped")
}
