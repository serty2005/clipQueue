package uihost

type NativeBridge struct {
	GetUISnapshot      func() (interface{}, error)
	GetConfig          func() (interface{}, error)
	SaveConfig         func(cfg map[string]interface{}) (interface{}, error)
	CaptureHotkey      func() (interface{}, error)
	GetHistory         func() (interface{}, error)
	GetQueueState      func() (interface{}, error)
	ToggleQueue        func() (interface{}, error)
	ToggleQueueOrder   func() (interface{}, error)
	ClearQueue         func() (interface{}, error)
	CopyHistoryItem    func(id string) (interface{}, error)
	RemoveQueueItem    func(index int) (interface{}, error)
	ParseLab           func(command string) (interface{}, error)
	BuildLab           func(steps []map[string]interface{}) (interface{}, error)
	StartSequence      func() (interface{}, error)
	StopSequence       func() (interface{}, error)
	GetSequenceStatus  func(last int) (interface{}, error)
	NotifyStateChanged func()
}

type NativeBridgeCapable interface {
	SetNativeBridge(bridge *NativeBridge)
	NotifyNativeStateChanged()
	NotifyNativeMacroInvoke(name string, done bool)
}
