package uihost

type UIHost interface {
	Show() error
	Hide() error
	Toggle() error
	Focus() error
	Close() error
	Navigate(url string) error
}
