package uihost

type WindowState struct {
	Visible   bool
	HasBounds bool
	X         int
	Y         int
	Width     int
	Height    int
}

type UIHost interface {
	Show() error
	Hide() error
	Toggle() error
	Focus() error
	Close() error
	Navigate(url string) error
}

type WindowStateAware interface {
	SetWindowStateHandler(handler func(WindowState))
}
