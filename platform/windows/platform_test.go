//go:build windows

package windows

import (
	"testing"
	"time"
	"unsafe"
)

func TestStructLayout(t *testing.T) {
	t.Run("NOTIFYICONDATA size", func(t *testing.T) {
		expectedSize := uintptr(NOTIFYICONDATA_V2_SIZE)
		actualSize := unsafe.Sizeof(NOTIFYICONDATA{})
		if actualSize != expectedSize {
			t.Fatalf("NOTIFYICONDATA size mismatch: expected %d bytes, got %d bytes", expectedSize, actualSize)
		}
	})

	t.Run("INPUT size", func(t *testing.T) {
		const expectedSize = 40
		actualSize := unsafe.Sizeof(INPUT{})
		if actualSize != expectedSize {
			t.Fatalf("INPUT size mismatch: expected %d bytes, got %d bytes", expectedSize, actualSize)
		}
	})

	t.Run("DROPFILES size", func(t *testing.T) {
		const expectedSize = 20
		actualSize := unsafe.Sizeof(DROPFILES{})
		if actualSize != expectedSize {
			t.Fatalf("DROPFILES size mismatch: expected %d bytes, got %d bytes", expectedSize, actualSize)
		}
	})
}

func TestClipboardCycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping clipboard test in short mode")
	}

	// Generate unique test string with timestamp
	testText := "TestClipboardCycle-" + time.Now().Format("20060102150405.999999")

	// Write to clipboard
	err := Write(ClipboardContent{
		Type: Text,
		Text: testText,
	})
	if err != nil {
		t.Skipf("Failed to write to clipboard: %v", err)
	}

	// Read from clipboard
	content, err := Read()
	if err != nil {
		t.Skipf("Failed to read from clipboard: %v", err)
	}

	// Verify content
	if content.Type != Text {
		t.Fatalf("Clipboard content type mismatch: expected %v, got %v", Text, content.Type)
	}
	if content.Text != testText {
		t.Fatalf("Clipboard content mismatch: expected %q, got %q", testText, content.Text)
	}

	// Test clearing clipboard
	err = Write(ClipboardContent{
		Type: Empty,
	})
	if err != nil {
		t.Skipf("Failed to clear clipboard: %v", err)
	}

	// Verify clipboard is empty
	content, err = Read()
	if err != nil {
		t.Skipf("Failed to read from clipboard after clearing: %v", err)
	}
	if content.Type != Empty {
		t.Fatalf("Clipboard not cleared: expected Empty, got %v", content.Type)
	}
}
