package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/serty2005/clipqueue/internal/config"
	"github.com/serty2005/clipqueue/internal/logger"
)

//go:embed index.html
var embedFS embed.FS

type Server struct {
	httpServer     *http.Server
	config         *config.SafeConfig
	host           interface{} // Pointer to platform-specific host implementation
	OnConfigUpdate func()      // Callback for config changes
}

func NewServer(cfg *config.SafeConfig, host interface{}) *Server {
	mux := http.NewServeMux()

	s := &Server{
		httpServer: &http.Server{
			Addr:    "127.0.0.1:0", // Используем случайный свободный порт
			Handler: mux,
		},
		config: cfg,
		host:   host,
	}

	// Настраиваем маршруты
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/hotkeys/capture", s.handleCaptureHotkey)

	return s
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Get current config
		cfg := s.config.Get()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cfg)
		return
	case http.MethodPost:
		// Update config
		var newCfg config.Config
		if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "Invalid config: %v", err)
			return
		}

		if err := s.config.Update(&newCfg); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "Failed to update config: %v", err)
			return
		}

		// Call the callback if set
		if s.OnConfigUpdate != nil {
			s.OnConfigUpdate()
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Config updated successfully")
		return
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		fmt.Fprintf(w, "Method %s not allowed", r.Method)
		return
	}
}

func (s *Server) Start() error {
	// Создаем listener с случайным свободным портом
	ln, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}

	// Обновляем адрес сервера с фактическим портом
	s.httpServer.Addr = ln.Addr().String()

	// Запускаем сервер в горутине
	go func() {
		if err := s.httpServer.Serve(ln); err != http.ErrServerClosed {
			logger.Error("server error: %v", err)
		}
	}()

	logger.Info("server started at %s", s.GetURL())
	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	logger.Info("stopping server...")
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleCaptureHotkey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
		return
	}

	// Cast host to windows.Host type (Windows platform specific)
	host, ok := s.host.(interface {
		CaptureHotkey(timeout time.Duration) (string, error)
	})
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Hotkey capture not supported on this platform"})
		return
	}

	// Capture hotkey with 5 second timeout
	hotkey, err := host.CaptureHotkey(5 * time.Second)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Return captured hotkey
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"hotkey": hotkey})
}

func (s *Server) GetURL() string {
	// Заменяем ":0" на фактический порт
	return fmt.Sprintf("http://%s", s.httpServer.Addr)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	content, err := embedFS.ReadFile("index.html")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Error reading index.html: %v", err)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}
