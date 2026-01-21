package app

import (
	"sync"
	"time"

	"github.com/serty2005/clipqueue/internal/config"
	"github.com/serty2005/clipqueue/internal/logger"
	"github.com/serty2005/clipqueue/platform/windows"
)

// Controller manages the clipboard queue functionality
type Controller struct {
	mu               sync.Mutex
	queueEnabled     bool
	queue            []windows.ClipboardContent
	snapshotOnEnable *windows.ClipboardContent
	selfEventsRing   []uint32 // Ring buffer for self-event suppression
	ringIndex        int      // Current index for ring buffer
	ringSize         int      // Size of ring buffer
	cfg              *config.Config
	orderStrategy    string                                     // "LIFO" or "FIFO"
	onStateChange    func(enabled bool, count int, mode string) // Callback for state changes
}

// NewController creates a new instance of Controller
func NewController(cfg *config.Config) *Controller {
	const ringBufferSize = 8
	order := cfg.Queue.DefaultOrder
	if order != "LIFO" && order != "FIFO" {
		order = "LIFO" // Default to LIFO if invalid
	}
	return &Controller{
		selfEventsRing: make([]uint32, ringBufferSize),
		ringSize:       ringBufferSize,
		cfg:            cfg,
		orderStrategy:  order,
		onStateChange:  func(enabled bool, count int, mode string) {}, // Default empty callback
	}
}

// SetStateCallback sets the callback function to be called when the state changes
func (c *Controller) SetStateCallback(fn func(enabled bool, count int, mode string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onStateChange = fn
}

// ClearQueue clears the clipboard queue
func (c *Controller) ClearQueue() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.queue) == 0 {
		logger.Debug("ClearQueue skipped - queue is already empty")
		// Still call callback to update UI
		c.onStateChange(c.queueEnabled, 0, c.orderStrategy)
		return
	}

	c.queue = nil
	logger.Info("Queue cleared")
	c.onStateChange(c.queueEnabled, 0, c.orderStrategy)
}

// ToggleOrder toggles the queue order between LIFO and FIFO
func (c *Controller) ToggleOrder() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.orderStrategy == "LIFO" {
		c.orderStrategy = "FIFO"
	} else {
		c.orderStrategy = "LIFO"
	}

	logger.Info("Queue order toggled to: %s", c.orderStrategy)
	c.onStateChange(c.queueEnabled, len(c.queue), c.orderStrategy)
}

// ToggleQueue toggles the queue mode on or off
func (c *Controller) ToggleQueue() {
	logger.Info("Entering ToggleQueue, current state: %v", c.queueEnabled)

	c.mu.Lock()

	if !c.queueEnabled {
		// Enable queue mode - take snapshot before enabling
		c.mu.Unlock()
		logger.Debug("Taking clipboard snapshot before enabling queue")
		snap, err := windows.Read()
		if err != nil {
			logger.Error("Failed to take clipboard snapshot: %v", err)
		}
		c.mu.Lock()
		c.snapshotOnEnable = &snap

		c.queue = nil // Clear any existing queue
		c.queueEnabled = true
		logger.Info("Queue mode enabled")
		c.mu.Unlock()
		c.onStateChange(c.queueEnabled, len(c.queue), c.orderStrategy)
	} else {
		// Disable queue mode
		c.queueEnabled = false
		c.queue = nil

		var snapshotToRestore *windows.ClipboardContent
		if c.snapshotOnEnable != nil {
			snapshotToRestore = c.snapshotOnEnable
			c.snapshotOnEnable = nil
		}

		c.mu.Unlock()

		if snapshotToRestore != nil {
			logger.Debug("Restoring clipboard to snapshot state")
			err := windows.Write(*snapshotToRestore)
			if err != nil {
				logger.Error("Failed to restore clipboard snapshot: %v", err)
			}
			// Add sequence number to self-event suppression ring buffer
			c.addSelfEvent(windows.GetClipboardSequenceNumber())
		}

		logger.Info("Queue mode disabled")
		c.onStateChange(c.queueEnabled, 0, c.orderStrategy)
	}
}

// OnClipboardUpdate handles clipboard update events
func (c *Controller) OnClipboardUpdate() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.queueEnabled {
		logger.Debug("OnClipboardUpdate skipped - queue mode disabled")
		return
	}

	// Check for self-event suppression
	seq := windows.GetClipboardSequenceNumber()
	if c.isSelfEvent(seq) {
		logger.Debug("OnClipboardUpdate skipped - self-event (seq=%d)", seq)
		return
	}

	// Read clipboard content
	content, err := windows.Read()
	if err != nil {
		logger.Error("Failed to read clipboard: %v", err)
		return
	}

	if content.Type == windows.Empty {
		logger.Debug("OnClipboardUpdate skipped - empty clipboard content")
		return
	}

	// Add to queue
	c.queue = append(c.queue, content)
	logger.Info("Enqueued clipboard content (type=%s, size=%d bytes, preview=%q, queue length=%d)",
		content.Type.String(), content.SizeBytes, content.Preview, len(c.queue))
	c.onStateChange(c.queueEnabled, len(c.queue), c.orderStrategy)
}

// PasteNext retrieves and pastes the next item from the clipboard queue
func (c *Controller) PasteNext() {
	logger.Info("Entering PasteNext")

	c.mu.Lock()
	if !c.queueEnabled {
		c.mu.Unlock()
		logger.Warn("PasteNext skipped - queue mode disabled")
		return
	}

	if len(c.queue) == 0 {
		c.mu.Unlock()
		logger.Warn("PasteNext skipped - queue is empty")
		return
	}

	logger.Info("PasteNext called, queue length: %d, order: %s", len(c.queue), c.orderStrategy)

	var item windows.ClipboardContent

	// Get next item from queue based on order strategy
	if c.orderStrategy == "LIFO" {
		// LIFO: get last item
		item = c.queue[len(c.queue)-1]
		c.queue = c.queue[:len(c.queue)-1]
	} else {
		// FIFO: get first item
		item = c.queue[0]
		c.queue = c.queue[1:]
	}

	logger.Info("Dequeued clipboard content (type=%s, size=%d bytes, preview=%q, queue length=%d, order=%s)",
		item.Type.String(), item.SizeBytes, item.Preview, len(c.queue), c.orderStrategy)
	c.onStateChange(c.queueEnabled, len(c.queue), c.orderStrategy)
	c.mu.Unlock()

	// Save current clipboard state
	logger.Debug("Saving current clipboard state before pasting")
	before, err := windows.Read()
	if err != nil {
		logger.Error("Failed to save current clipboard state: %v", err)
		return
	}

	// Perform the paste operation
	logger.Debug("Writing item to clipboard for pasting")
	err = windows.Write(item)
	if err != nil {
		logger.Error("Failed to write item to clipboard: %v", err)
		return
	}
	c.addSelfEvent(windows.GetClipboardSequenceNumber())

	// Give Windows time to update clipboard handles before sending Ctrl+V
	time.Sleep(100 * time.Millisecond)

	logger.Debug("Sending Ctrl+V keystroke")
	err = windows.SendCtrlV()
	if err != nil {
		logger.Error("Failed to send Ctrl+V keystroke: %v", err)
		// Try to restore clipboard anyway
		_ = windows.Write(before)
		c.addSelfEvent(windows.GetClipboardSequenceNumber())
		return
	}

	// Wait before restoring clipboard
	time.Sleep(time.Duration(c.cfg.Clipboard.RestoreDelayMs) * time.Millisecond)

	logger.Debug("Restoring previous clipboard state")
	err = windows.Write(before)
	if err != nil {
		logger.Error("Failed to restore previous clipboard state: %v", err)
	}
	c.addSelfEvent(windows.GetClipboardSequenceNumber())
}

// addSelfEvent adds a sequence number to the self-event suppression ring buffer
func (c *Controller) addSelfEvent(seq uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.selfEventsRing[c.ringIndex] = seq
	c.ringIndex = (c.ringIndex + 1) % c.ringSize
	logger.Debug("Added self-event sequence number: %d", seq)
}

// isSelfEvent checks if a sequence number is in the self-event suppression ring buffer
func (c *Controller) isSelfEvent(seq uint32) bool {
	for _, s := range c.selfEventsRing {
		if s == seq {
			return true
		}
	}
	return false
}
