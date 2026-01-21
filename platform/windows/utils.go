package windows

import (
	"os/exec"
	"runtime"
)

var (
	procGetConsoleWindow = kernel32.NewProc("GetConsoleWindow")
	procShowWindow       = user32.NewProc("ShowWindow")

	SW_HIDE = 0
)

// HideConsole скрывает консольное окно приложения
func HideConsole() {
	hwnd, _, _ := procGetConsoleWindow.Call()
	if hwnd != 0 {
		procShowWindow.Call(hwnd, uintptr(SW_HIDE))
	}
}

// OpenBrowser открывает указанный URL в браузере по умолчанию
func OpenBrowser(url string) error {
	if runtime.GOOS != "windows" {
		return nil
	}

	// Используем rundll32 для открытия браузера на Windows
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	return cmd.Start()
}
