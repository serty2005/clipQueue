package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/serty2005/clipqueue/internal/app"
	"github.com/serty2005/clipqueue/internal/config"
	"github.com/serty2005/clipqueue/internal/logger"
	"github.com/serty2005/clipqueue/internal/parser"
	"github.com/serty2005/clipqueue/platform/windows"
)

//go:embed index.html
var embedFS embed.FS

// HistoryItemDTO represents a history item for API responses
type HistoryItemDTO struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Preview    string    `json:"preview"`
	Timestamp  time.Time `json:"timestamp"`
	IsQueued   bool      `json:"isQueued"`
	QueueIndex int       `json:"queueIndex"`
	IsNext     bool      `json:"isNext"`
}

// CommandStepDTO represents a single step in a command pipeline for API
type CommandStepDTO struct {
	Command  string   `json:"command"`
	Args     []string `json:"args"`
	Operator string   `json:"operator"`
}

// PipelineDTO represents the parsed command structure for API
type PipelineDTO struct {
	Steps    []CommandStepDTO `json:"steps"`
	Original string           `json:"original"`
}

// ParseRequest is the request body for parsing a command
type ParseRequest struct {
	Command string `json:"command"`
}

// BuildRequest is the request body for building a command from steps
type BuildRequest struct {
	Steps []CommandStepDTO `json:"steps"`
}

// BuildResponse is the response body containing the built command
type BuildResponse struct {
	Command string `json:"command"`
}

type Server struct {
	httpServer     *http.Server
	config         *config.SafeConfig
	host           interface{} // Pointer to platform-specific host implementation
	controller     *app.Controller
	OnConfigUpdate func() // Callback for config changes
}

func NewServer(cfg *config.SafeConfig, host interface{}, controller *app.Controller) *Server {
	mux := http.NewServeMux()

	s := &Server{
		httpServer: &http.Server{
			Addr:    "127.0.0.1:0", // Используем случайный свободный порт
			Handler: mux,
		},
		config:     cfg,
		host:       host,
		controller: controller,
	}

	// Настраиваем маршруты
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/hotkeys/capture", s.handleCaptureHotkey)
	mux.HandleFunc("/api/history", s.handleHistory)
	mux.HandleFunc("/api/queue/clear", s.handleQueueClear)
	mux.HandleFunc("/api/copy", s.handleCopy)

	// Lab API routes
	mux.HandleFunc("/api/lab/parse", s.handleLabParse)
	mux.HandleFunc("/api/lab/build", s.handleLabBuild)

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
			logger.Error("Failed to decode JSON config: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "Invalid config: %v", err)
			return
		}

		// Validate macros
		host, ok := s.host.(*windows.Host)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "Hotkey validation not supported on this platform")
			return
		}
		for i, macro := range newCfg.Macros {
			if host.ParseHotkeyToSignature(macro.Hotkey) == nil && host.ParseHotkeyToSignature(macro.Signature) == nil {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, "Invalid macro %d: neither Hotkey '%s' nor Signature '%s' is valid", i, macro.Hotkey, macro.Signature)
				return
			}
		}

		if err := s.config.Update(&newCfg); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "Failed to update config: %v", err)
			return
		}

		logger.Info("Config updated successfully")

		// Update order strategy
		if err := s.controller.SetOrderStrategy(newCfg.Queue.DefaultOrder); err != nil {
			logger.Warn("Failed to update order strategy: %v", err)
		}

		// Call the callback if set
		if s.OnConfigUpdate != nil {
			s.OnConfigUpdate()
		}

		logger.Info("OnConfigUpdate callback invoked")

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
		CaptureHotkeyWithDisplay(timeout time.Duration) (string, string, error)
	})
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Hotkey capture not supported on this platform"})
		return
	}

	// Capture hotkey with 5 second timeout
	signature, display, err := host.CaptureHotkeyWithDisplay(5 * time.Second)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Return captured hotkey
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"signature": signature, "display": display})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Get history items
		history := s.controller.GetHistory()
		queue := s.controller.GetQueue()
		order := s.controller.GetOrderStrategy()
		var items []HistoryItemDTO

		// Create map for quick lookup in queue
		queueMap := make(map[string]int) // id -> index
		for i, item := range queue {
			queueMap[item.ID] = i
		}

		// Determine next for paste
		var nextID string
		if len(queue) > 0 {
			if order == "LIFO" {
				nextID = queue[len(queue)-1].ID
			} else {
				nextID = queue[0].ID
			}
		}

		for _, item := range history {
			dto := HistoryItemDTO{
				ID:        item.ID,
				Type:      item.Type.String(),
				Preview:   item.Preview,
				Timestamp: item.Timestamp,
			}
			if idx, exists := queueMap[item.ID]; exists {
				dto.IsQueued = true
				dto.QueueIndex = idx
			} else {
				dto.IsQueued = false
				dto.QueueIndex = -1
			}
			dto.IsNext = dto.IsQueued && item.ID == nextID
			items = append(items, dto)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(items)
		return
	case http.MethodDelete:
		// Delete item by index from queue
		indexStr := r.URL.Query().Get("index")
		if indexStr == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "index parameter required"})
			return
		}
		var index int
		if _, err := fmt.Sscanf(indexStr, "%d", &index); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid index"})
			return
		}
		if err := s.controller.RemoveItem(index); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"message": "item removed"})
		return
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
		return
	}
}

func (s *Server) handleQueueClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
		return
	}

	s.controller.ClearQueue()
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "queue cleared"})
}

func (s *Server) handleCopy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
		return
	}

	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "id parameter required"})
		return
	}

	if err := s.controller.CopyItem(idStr); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "item copied to clipboard"})
}

func (s *Server) handleLabParse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
		return
	}

	var req ParseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}

	pipeline, err := parser.Parse(req.Command)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Parse error: %v", err)})
		return
	}

	// Convert to DTO
	dto := PipelineDTO{
		Original: pipeline.Original,
		Steps:    make([]CommandStepDTO, len(pipeline.Steps)),
	}

	for i, step := range pipeline.Steps {
		dto.Steps[i] = CommandStepDTO{
			Command:  step.Command,
			Args:     step.Args,
			Operator: step.Operator,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dto)
}

func (s *Server) handleLabBuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
		return
	}

	var req BuildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}

	// Convert from DTO
	steps := make([]parser.CommandStep, len(req.Steps))
	for i, step := range req.Steps {
		steps[i] = parser.CommandStep{
			Command:  step.Command,
			Args:     step.Args,
			Operator: step.Operator,
		}
	}

	pipeline := parser.Pipeline{Steps: steps}
	builtCommand := pipeline.String()

	resp := BuildResponse{
		Command: builtCommand,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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
