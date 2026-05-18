package windows

import (
	"strings"
	"testing"
)

func TestClipboardOpenOwnerUsesRegisteredWindow(t *testing.T) {
	const hwnd = uintptr(0x1234)
	SetClipboardOwnerWindow(0)
	t.Cleanup(func() {
		SetClipboardOwnerWindow(0)
	})

	if got := clipboardOpenOwner(); got != 0 {
		t.Fatalf("ожидался нулевой владелец до регистрации, получено %#x", got)
	}

	SetClipboardOwnerWindow(hwnd)

	if got := clipboardOpenOwner(); got != hwnd {
		t.Fatalf("ожидался зарегистрированный владелец %#x, получено %#x", hwnd, got)
	}
}

func TestWriteRequiresClipboardOwnerBeforeClearingClipboard(t *testing.T) {
	SetClipboardOwnerWindow(0)
	t.Cleanup(func() {
		SetClipboardOwnerWindow(0)
	})

	err := Write(ClipboardContent{
		Type: Text,
		Text: "проверка",
	})

	if err == nil {
		t.Fatal("ожидалась ошибка при записи без окна-владельца")
	}
	if !strings.Contains(err.Error(), "окно-владелец") {
		t.Fatalf("ожидалась ошибка про окно-владельца, получено: %v", err)
	}
}
