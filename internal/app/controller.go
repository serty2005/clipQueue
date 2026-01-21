package app

import (
	"fmt"
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
	history          []windows.ClipboardContent // Stores last 50 clipboard items
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
	time.Sleep(50 * time.Millisecond)

	c.mu.Lock()
	defer c.mu.Unlock()

	// Check for self-event suppression
	seq := windows.GetClipboardSequenceNumber()
	if c.isSelfEvent(seq) {
		logger.Debug("OnClipboardUpdate: пропущен самопоявление (seq=%d)", seq)
		return
	}

	// Read clipboard content
	content, err := windows.Read()
	if err != nil {
		logger.Error("OnClipboardUpdate: ошибка чтения буфера обмена - %v", err)
		return
	}

	if content.Type == windows.Empty {
		logger.Debug("OnClipboardUpdate: пропущен пустой контент")
		return
	}

	// Deduplication check
	if len(c.history) > 0 {
		last := c.history[len(c.history)-1]
		if content.Type == last.Type && content.Timestamp.Sub(last.Timestamp) < time.Second {
			var contentMatch bool
			if content.Type == windows.Text {
				contentMatch = content.Text == last.Text
			} else {
				contentMatch = content.SizeBytes == last.SizeBytes
			}
			if contentMatch {
				logger.Debug("OnClipboardUpdate: пропущен дубликат контента")
				return
			}
		}
	}

	// Add to history with rotation (keep last 50)
	if len(c.history) >= 50 {
		c.history = c.history[1:]
	}
	c.history = append(c.history, content)
	logger.Debug("OnClipboardUpdate: добавлено в историю (тип=%s, размер=%d байт, предпросмотр=%q, длина истории=%d)",
		content.Type.String(), content.SizeBytes, content.Preview, len(c.history))

	// Add to queue if enabled
	if c.queueEnabled {
		c.queue = append(c.queue, content)
		logger.Info("OnClipboardUpdate: добавлено в очередь (тип=%s, размер=%d байт, предпросмотр=%q, длина очереди=%d)",
			content.Type.String(), content.SizeBytes, content.Preview, len(c.queue))
		c.onStateChange(c.queueEnabled, len(c.queue), c.orderStrategy)
	} else {
		logger.Debug("OnClipboardUpdate: не добавлено в очередь (очередь отключена)")
	}
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

// GetQueue returns a copy of the clipboard queue with mutex protection
func (c *Controller) GetQueue() []windows.ClipboardContent {
	c.mu.Lock()
	defer c.mu.Unlock()

	queueCopy := make([]windows.ClipboardContent, len(c.queue))
	copy(queueCopy, c.queue)
	return queueCopy
}

// GetHistory returns a copy of the clipboard history with mutex protection
func (c *Controller) GetHistory() []windows.ClipboardContent {
	c.mu.Lock()
	defer c.mu.Unlock()

	historyCopy := make([]windows.ClipboardContent, len(c.history))
	copy(historyCopy, c.history)
	return historyCopy
}

// GetOrderStrategy returns the current order strategy
func (c *Controller) GetOrderStrategy() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.orderStrategy
}

// SetOrderStrategy sets the queue order strategy (LIFO or FIFO)
func (c *Controller) SetOrderStrategy(order string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if order != "LIFO" && order != "FIFO" {
		return fmt.Errorf("не поддерживаемая стратегия порядка: %s. Допустимые значения: LIFO, FIFO", order)
	}

	if c.orderStrategy == order {
		logger.Debug("SetOrderStrategy: стратегия уже установлена на %s", order)
		return nil
	}

	c.orderStrategy = order
	logger.Info("SetOrderStrategy: стратегия порядка изменена на %s", order)
	c.onStateChange(c.queueEnabled, len(c.queue), c.orderStrategy)
	return nil
}

// RemoveItem removes an item from the queue by index with mutex protection and index validation
func (c *Controller) RemoveItem(index int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if index < 0 || index >= len(c.queue) {
		return fmt.Errorf("invalid index: %d, queue length: %d", index, len(c.queue))
	}

	// Remove the item by slicing
	c.queue = append(c.queue[:index], c.queue[index+1:]...)
	logger.Info("Removed item at index %d, queue length now: %d", index, len(c.queue))
	c.onStateChange(c.queueEnabled, len(c.queue), c.orderStrategy)
	return nil
}

// addSelfEventLocked adds a sequence number to the self-event suppression ring buffer
// Предполагает, что мьютекс уже захвачен
func (c *Controller) addSelfEventLocked(seq uint32) {
	c.selfEventsRing[c.ringIndex] = seq
	c.ringIndex = (c.ringIndex + 1) % c.ringSize
	logger.Debug("Added self-event sequence number: %d", seq)
}

// addSelfEvent adds a sequence number to the self-event suppression ring buffer
func (c *Controller) addSelfEvent(seq uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addSelfEventLocked(seq)
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

// ExecuteMacro выполняет макрос с заданным текстом и режимом
func (c *Controller) ExecuteMacro(macro config.Macro) error {
	logger.Info("Executing macro with text: %q, mode: %s", macro.Text, macro.Mode)

	switch macro.Mode {
	case "type":
		// Режим "type" - ввод текста символ за символом
		err := windows.TypeString(macro.Text)
		if err != nil {
			logger.Error("Failed to type text: %v", err)
			return err
		}
		logger.Debug("Macro executed in type mode")

	case "paste":
		// Режим "paste" - вставка через буфер обмена с сохранением и восстановлением текущего состояния
		// Сохраняем текущий буфер обмена
		oldContent, err := windows.Read()
		if err != nil {
			logger.Error("Failed to read current clipboard: %v", err)
			return err
		}

		// Записываем текст макроса в буфер обмена
		content := windows.ClipboardContent{
			Type: windows.Text,
			Text: macro.Text,
		}
		if err := windows.Write(content); err != nil {
			logger.Error("Failed to write macro text to clipboard: %v", err)
			return err
		}
		c.addSelfEvent(windows.GetClipboardSequenceNumber())

		// Дайте время для обновления буфера обмена
		time.Sleep(100 * time.Millisecond)

		// Отправляем Ctrl+V для вставки
		if err := windows.SendCtrlV(); err != nil {
			logger.Error("Failed to send Ctrl+V: %v", err)
			// Попытка восстановить буфер даже при ошибке
			_ = windows.Write(oldContent)
			c.addSelfEvent(windows.GetClipboardSequenceNumber())
			return err
		}

		// Дожидаемся завершения вставки
		time.Sleep(time.Duration(c.cfg.Clipboard.RestoreDelayMs) * time.Millisecond)

		// Восстанавливаем исходный буфер обмена
		if err := windows.Write(oldContent); err != nil {
			logger.Error("Failed to restore clipboard: %v", err)
			return err
		}
		c.addSelfEvent(windows.GetClipboardSequenceNumber())

		logger.Debug("Macro executed in paste mode")

	default:
		return fmt.Errorf("unsupported macro mode: %s. Supported modes: type, paste", macro.Mode)
	}

	return nil
}

// CopyItem copies an item from history to clipboard by ID
func (c *Controller) CopyItem(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, item := range c.history {
		if item.ID == id {
			err := windows.Write(item)
			if err != nil {
				return err
			}
			c.addSelfEventLocked(windows.GetClipboardSequenceNumber())
			logger.Info("Copied item from history to clipboard (id=%s, type=%s)", id, item.Type.String())
			return nil
		}
	}
	return fmt.Errorf("item with id %s not found in history", id)
}
