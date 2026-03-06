package server

import (
	"fmt"
	"time"

	"github.com/serty2005/clipqueue/internal/config"
	"github.com/serty2005/clipqueue/internal/logger"
	"github.com/serty2005/clipqueue/internal/parser"
	"github.com/serty2005/clipqueue/platform/windows"
)

type UISnapshotResponse struct {
	Queue   QueueStateResponse `json:"queue"`
	History []HistoryItemDTO   `json:"history"`
}

func (s *Server) buildHistoryDTOs() []HistoryItemDTO {
	history := s.controller.GetHistory()
	queue := s.controller.GetQueue()
	order := s.controller.GetOrderStrategy()
	currentClipboardID := s.controller.GetCurrentClipboardID()

	queueMap := make(map[string]int, len(queue))
	for i, item := range queue {
		queueMap[item.ID] = i
	}

	var nextID string
	if len(queue) > 0 {
		if order == "LIFO" {
			nextID = queue[len(queue)-1].ID
		} else {
			nextID = queue[0].ID
		}
	}

	items := make([]HistoryItemDTO, 0, len(history))
	for i := len(history) - 1; i >= 0; i-- {
		item := history[i]
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
		dto.IsCurrentClipboard = item.ID == currentClipboardID
		items = append(items, dto)
	}

	return items
}

func (s *Server) GetUISnapshot() UISnapshotResponse {
	enabled, count, order := s.controller.GetQueueState()
	return UISnapshotResponse{
		Queue: QueueStateResponse{
			Enabled: enabled,
			Count:   count,
			Order:   order,
		},
		History: s.buildHistoryDTOs(),
	}
}

func (s *Server) NativeToggleQueue() UISnapshotResponse {
	s.controller.ToggleQueue()
	return s.GetUISnapshot()
}

func (s *Server) NativeToggleQueueOrder() UISnapshotResponse {
	s.controller.ToggleOrder()
	return s.GetUISnapshot()
}

func (s *Server) NativeClearQueue() UISnapshotResponse {
	s.controller.ClearQueue()
	return s.GetUISnapshot()
}

func (s *Server) NativeCopyHistoryItem(id string) (UISnapshotResponse, error) {
	if err := s.controller.CopyItem(id); err != nil {
		return UISnapshotResponse{}, err
	}
	return s.GetUISnapshot(), nil
}

func (s *Server) NativeGetConfig() *config.Config {
	return s.config.Get()
}

func (s *Server) NativeSaveConfig(newCfg config.Config) (map[string]string, error) {
	host, ok := s.host.(*windows.Host)
	if !ok {
		return nil, fmt.Errorf("Hotkey validation not supported on this platform")
	}
	for i, macro := range newCfg.Macros {
		if host.ParseHotkeyToSignature(macro.Hotkey) == nil && host.ParseHotkeyToSignature(macro.Signature) == nil {
			return nil, fmt.Errorf("Invalid macro %d: neither Hotkey '%s' nor Signature '%s' is valid", i, macro.Hotkey, macro.Signature)
		}
	}

	if err := s.config.Update(&newCfg); err != nil {
		return nil, fmt.Errorf("Failed to update config: %w", err)
	}

	logger.Info("Config updated successfully (native bridge)")

	if err := s.controller.SetOrderStrategy(newCfg.Queue.DefaultOrder); err != nil {
		logger.Warn("Failed to update order strategy: %v", err)
	}

	if s.OnConfigUpdate != nil {
		s.OnConfigUpdate()
	}

	return map[string]string{"message": "Config updated successfully"}, nil
}

func (s *Server) NativeCaptureHotkey() (map[string]string, error) {
	host, ok := s.host.(interface {
		CaptureHotkeyWithDisplay(timeout time.Duration) (string, string, error)
	})
	if !ok {
		return nil, fmt.Errorf("Hotkey capture not supported on this platform")
	}
	signature, display, err := host.CaptureHotkeyWithDisplay(5 * time.Second)
	if err != nil {
		return nil, err
	}
	return map[string]string{"signature": signature, "display": display}, nil
}

func (s *Server) NativeGetHistory() []HistoryItemDTO {
	return s.buildHistoryDTOs()
}

func (s *Server) NativeGetQueueState() QueueStateResponse {
	enabled, count, order := s.controller.GetQueueState()
	return QueueStateResponse{Enabled: enabled, Count: count, Order: order}
}

func (s *Server) NativeRemoveQueueItem(index int) (map[string]string, error) {
	if err := s.controller.RemoveItem(index); err != nil {
		return nil, err
	}
	return map[string]string{"message": "item removed"}, nil
}

func (s *Server) NativeParseLab(command string) (PipelineDTO, error) {
	pipeline, err := parser.Parse(command)
	if err != nil {
		return PipelineDTO{}, fmt.Errorf("Parse error: %v", err)
	}
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
	return dto, nil
}

func (s *Server) NativeBuildLab(steps []CommandStepDTO) (BuildResponse, error) {
	converted := make([]parser.CommandStep, len(steps))
	for i, step := range steps {
		converted[i] = parser.CommandStep{
			Command:  step.Command,
			Args:     step.Args,
			Operator: step.Operator,
		}
	}
	pipeline := parser.Pipeline{Steps: converted}
	return BuildResponse{Command: pipeline.String()}, nil
}

func (s *Server) NativeStartSequenceRecording() (map[string]string, error) {
	host, ok := s.host.(interface {
		StartSequenceRecording() error
	})
	if !ok {
		return nil, fmt.Errorf("Sequence recording not supported on this platform")
	}
	if err := host.StartSequenceRecording(); err != nil {
		return nil, err
	}
	return map[string]string{"message": "sequence recording started"}, nil
}

func (s *Server) NativeStopSequenceRecording() (SequenceStopResponse, error) {
	host, ok := s.host.(interface {
		StopSequenceRecording() (*windows.RecordedSequence, string, error)
	})
	if !ok {
		return SequenceStopResponse{}, fmt.Errorf("Sequence recording not supported on this platform")
	}
	seq, encoded, err := host.StopSequenceRecording()
	if err != nil {
		return SequenceStopResponse{}, err
	}
	return SequenceStopResponse{
		Sequence:    encoded,
		EventCount:  len(seq.Events),
		RecordedHKL: seq.RecordedHKL,
	}, nil
}

func (s *Server) NativeGetSequenceStatus(last int) (windows.SequenceRecordingStatus, error) {
	if last <= 0 {
		last = 30
	}
	host, ok := s.host.(interface {
		GetSequenceRecordingStatus(lastN int) (windows.SequenceRecordingStatus, error)
	})
	if !ok {
		return windows.SequenceRecordingStatus{}, fmt.Errorf("Sequence status not supported on this platform")
	}
	return host.GetSequenceRecordingStatus(last)
}
