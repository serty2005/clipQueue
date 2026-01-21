package windows

import "github.com/serty2005/clipqueue/internal/logger"

var (
	procAddClipboardFormatListener    = user32.NewProc("AddClipboardFormatListener")
	procRemoveClipboardFormatListener = user32.NewProc("RemoveClipboardFormatListener")
)

type ClipboardWatcher struct {
	host *Host
}

func NewClipboardWatcher(host *Host) (*ClipboardWatcher, error) {
	return &ClipboardWatcher{
		host: host,
	}, nil
}

func (w *ClipboardWatcher) Start() error {
	ret, _, err := procAddClipboardFormatListener.Call(w.host.hwnd)
	if ret == 0 {
		logger.Error("AddClipboardFormatListener failed (err=%v)", err)
		return err
	}
	logger.Info("AddClipboardFormatListener ok")
	return nil
}

func (w *ClipboardWatcher) Stop() error {
	ret, _, err := procRemoveClipboardFormatListener.Call(w.host.hwnd)
	if ret == 0 {
		return err
	}
	return nil
}
