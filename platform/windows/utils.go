package windows

import (
	"os/exec"
	"runtime"
)

// OpenBrowser открывает указанный URL в браузере по умолчанию
func OpenBrowser(url string) error {
	if runtime.GOOS != "windows" {
		return nil
	}

	// Используем rundll32 для открытия браузера на Windows
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	return cmd.Start()
}
