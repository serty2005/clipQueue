package windows

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"syscall"
	"time"

	"github.com/serty2005/clipqueue/internal/logger"
)

const (
	llkhfExtended = 0x01
	llkhfInjected = 0x10
)

// RecordedKeyEvent stores a low-level keyboard event for later replay.
type RecordedKeyEvent struct {
	VK        uint16 `json:"vk"`
	ScanCode  uint16 `json:"scanCode"`
	HookFlags uint32 `json:"hookFlags"`
	Message   uint32 `json:"message"`
	DelayMs   uint32 `json:"delayMs"`
}

// RecordedSequence contains keyboard events captured from the low-level hook.
type RecordedSequence struct {
	Version     int                `json:"version"`
	RecordedAt  time.Time          `json:"recordedAt"`
	RecordedHKL uint64             `json:"recordedHkl,omitempty"`
	Events      []RecordedKeyEvent `json:"events"`
}

type SequenceRecordingStatus struct {
	Active      bool               `json:"active"`
	EventCount  int                `json:"eventCount"`
	RecordedHKL uint64             `json:"recordedHkl"`
	Events      []RecordedKeyEvent `json:"events"`
}

type SequencePlaybackOptions struct {
	NormalizeDelays bool `json:"normalizeDelays"`
	FixedDelayMs    uint32 `json:"fixedDelayMs"`
}

func (s *RecordedSequence) EncodeBase64() (string, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func DecodeRecordedSequenceBase64(encoded string) (*RecordedSequence, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode sequence base64: %w", err)
	}
	var seq RecordedSequence
	if err := json.Unmarshal(raw, &seq); err != nil {
		return nil, fmt.Errorf("decode sequence json: %w", err)
	}
	if seq.Version == 0 {
		seq.Version = 1
	}
	return &seq, nil
}

func sequenceEventToInput(ev RecordedKeyEvent) INPUT {
	flags := uint32(KEYEVENTF_SCANCODE)
	if ev.Message == WM_KEYUP || ev.Message == WM_SYSKEYUP {
		flags |= KEYEVENTF_KEYUP
	}
	if ev.HookFlags&llkhfExtended != 0 {
		flags |= KEYEVENTF_EXTENDEDKEY
	}

	return INPUT{
		Type: INPUT_KEYBOARD,
		Ki: KEYBDINPUT{
			Wvk:     0,
			WScan:   ev.ScanCode,
			DwFlags: flags,
		},
	}
}

// PlayRecordedSequence replays a captured keyboard sequence.
func PlayRecordedSequence(seq *RecordedSequence) error {
	return PlayRecordedSequenceWithOptions(seq, SequencePlaybackOptions{})
}

func PlayRecordedSequenceWithOptions(seq *RecordedSequence, opts SequencePlaybackOptions) error {
	if seq == nil {
		return fmt.Errorf("sequence is nil")
	}
	if len(seq.Events) == 0 {
		return fmt.Errorf("sequence has no events")
	}

	logger.Debug("PlayRecordedSequence start: events=%d recordedHKL=0x%X normalize=%v fixedDelayMs=%d",
		len(seq.Events), seq.RecordedHKL, opts.NormalizeDelays, opts.FixedDelayMs)

	for i, ev := range seq.Events {
		delayMs := ev.DelayMs
		if opts.NormalizeDelays {
			delayMs = opts.FixedDelayMs
			if delayMs > 10000 {
				delayMs = 10000
			}
		}
		if delayMs > 0 {
			time.Sleep(time.Duration(delayMs) * time.Millisecond)
		}

		input := sequenceEventToInput(ev)
		if result := sendInput([]INPUT{input}); result != 1 {
			logger.Error("PlayRecordedSequence send failed at event %d (msg=0x%X scan=0x%X)", i, ev.Message, ev.ScanCode)
			return syscall.GetLastError()
		}
	}

	logger.Debug("PlayRecordedSequence completed successfully")
	return nil
}

// PlayRecordedSequenceBase64 decodes and replays a sequence stored in config.
func PlayRecordedSequenceBase64(encoded string) error {
	return PlayRecordedSequenceBase64WithOptions(encoded, SequencePlaybackOptions{})
}

func PlayRecordedSequenceBase64WithOptions(encoded string, opts SequencePlaybackOptions) error {
	seq, err := DecodeRecordedSequenceBase64(encoded)
	if err != nil {
		return err
	}
	return PlayRecordedSequenceWithOptions(seq, opts)
}
