//go:build windows

package windows

import (
	"syscall"

	"github.com/serty2005/clipqueue/internal/logger"
)

const (
	processPerMonitorDPIAware = 2
)

var (
	user32DPI = syscall.NewLazyDLL("user32.dll")
	shcoreDPI = syscall.NewLazyDLL("shcore.dll")

	procSetProcessDpiAwarenessContext = user32DPI.NewProc("SetProcessDpiAwarenessContext")
	procSetThreadDpiAwarenessContext  = user32DPI.NewProc("SetThreadDpiAwarenessContext")
	procSetProcessDpiAwareness        = shcoreDPI.NewProc("SetProcessDpiAwareness")
)

func dpiAwarenessContextPerMonitorV2() uintptr {
	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 == (HANDLE)-4
	return ^uintptr(3)
}

// EnableHighDPIAwareness включает максимально доступный режим DPI awareness.
func EnableHighDPIAwareness() {
	if ok, _, _ := procSetProcessDpiAwarenessContext.Call(dpiAwarenessContextPerMonitorV2()); ok != 0 {
		logger.Info("DPI awareness enabled: Per Monitor V2")
		return
	}

	if hr, _, _ := procSetProcessDpiAwareness.Call(processPerMonitorDPIAware); int32(hr) >= 0 {
		logger.Info("DPI awareness enabled: Per Monitor (fallback)")
		return
	}

	logger.Warn("Не удалось включить DPI awareness, Windows может масштабировать UI с размытием")
}

// ApplyCurrentThreadHighDPIAwareness включает DPI-контекст для текущего потока (если доступно).
func ApplyCurrentThreadHighDPIAwareness() {
	if procSetThreadDpiAwarenessContext.Find() != nil {
		return
	}
	procSetThreadDpiAwarenessContext.Call(dpiAwarenessContextPerMonitorV2())
}
