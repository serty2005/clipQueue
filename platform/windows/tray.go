package windows

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Константы для работы с системным треем
const (
	WM_USER          = 0x0400
	WM_TRAY_CALLBACK = WM_USER + 1

	// Флаги для Shell_NotifyIcon
	NIM_ADD        = 0x00000000
	NIM_MODIFY     = 0x00000001
	NIM_DELETE     = 0x00000002
	NIM_SETVERSION = 0x00000004

	// Флаги для NOTIFYICONDATA.uFlags
	NIF_MESSAGE = 0x00000001
	NIF_ICON    = 0x00000002
	NIF_TIP     = 0x00000004

	// Флаги для TrackPopupMenu
	TPM_RETURNCMD = 0x0100

	// IDs пунктов контекстного меню
	ID_TRAY_INFO         = 101
	ID_TRAY_TOGGLE_QUEUE = 102
	ID_TRAY_SWITCH_ORDER = 103
	ID_TRAY_CLEAR        = 104
	ID_TRAY_SETTINGS     = 106
	ID_TRAY_EXIT         = 105

	// Размеры для NOTIFYICONDATA (для Windows Vista и выше)
	NOTIFYICONDATA_V2_SIZE = 968 // Размер структуры для Windows Vista+ (x64)
)

// NOTIFYICONDATA структура для работы с Shell_NotifyIconW
// Важно: Поля должны быть выровнены правильно для x64
// Структура для Windows Vista+ (NOTIFYICONDATA_V2_SIZE = 952 байт на x64)
// https://learn.microsoft.com/ru-ru/windows/win32/api/shellapi/ns-shellapi-notifyicondataw
type NOTIFYICONDATA struct {
	CbSize       uint32
	HWnd         uintptr
	UID          uint32
	UFlags       uint32
	UMsg         uint32
	HIcon        uintptr
	SzTip        [128]uint16 // Максимальная длина подсказки 128 символов
	DwState      uint32
	DwStateMask  uint32
	SzInfo       [256]uint16
	UnionPadding uint32 // Заполнитель для выравнивания объединенного поля
	SzInfoTitle  [64]uint16
	DwInfoFlags  uint32
	GuidItem     [16]byte // GUID для Windows Vista+
}

// Tray структура для управления системным треем
type Tray struct {
	hwnd   uintptr
	hIcon  uintptr
	hidden bool
}

// NewTray создаёт новый экземпляр Tray
func NewTray(hwnd uintptr) *Tray {
	return &Tray{
		hwnd: hwnd,
	}
}

// Setup инициализирует иконку в системном трее
func (t *Tray) Setup(iconPath string) error {
	var hIcon uintptr
	var err error

	if iconPath == "" {
		// Загружаем системную иконку по умолчанию (IDI_APPLICATION)
		user32 := windows.NewLazySystemDLL("user32.dll")
		procLoadIcon := user32.NewProc("LoadIconW")
		hIcon, _, err = procLoadIcon.Call(
			0,
			uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("#32512"))), // IDI_APPLICATION
		)
		if hIcon == 0 {
			return err
		}
	} else {
		// Загружаем иконку из файла
		user32 := windows.NewLazySystemDLL("user32.dll")
		procLoadImage := user32.NewProc("LoadImageW")
		hIcon, _, err = procLoadImage.Call(
			0,
			uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(iconPath))),
			1, // IMAGE_ICON
			0,
			0,
			0x00000010|0x00000002, // LR_DEFAULTSIZE|LR_LOADFROMFILE
		)
		if hIcon == 0 {
			// Если не удалось загрузить из файла, используем системную иконку
			procLoadIcon := user32.NewProc("LoadIconW")
			hIcon, _, err = procLoadIcon.Call(
				0,
				uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("#32512"))),
			)
			if hIcon == 0 {
				return err
			}
		}
	}

	t.hIcon = hIcon

	// Инициализируем структуру NOTIFYICONDATA
	var nid NOTIFYICONDATA
	nid.CbSize = NOTIFYICONDATA_V2_SIZE
	nid.HWnd = t.hwnd
	nid.UID = 1
	nid.UFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP
	nid.UMsg = WM_TRAY_CALLBACK
	nid.HIcon = hIcon

	// Устанавливаем подсказку по умолчанию
	tip := "ClipQueue"
	copy(nid.SzTip[:], windows.StringToUTF16(tip))

	// Вызываем Shell_NotifyIconW для добавления иконки
	shell32 := windows.NewLazySystemDLL("shell32.dll")
	procShellNotifyIcon := shell32.NewProc("Shell_NotifyIconW")
	result, _, err := procShellNotifyIcon.Call(
		uintptr(NIM_ADD),
		uintptr(unsafe.Pointer(&nid)),
	)
	if result == 0 {
		return err
	}

	return nil
}

// UpdateTooltip обновляет всплывающую подсказку для иконки
func (t *Tray) UpdateTooltip(text string) error {
	var nid NOTIFYICONDATA
	nid.CbSize = NOTIFYICONDATA_V2_SIZE
	nid.HWnd = t.hwnd
	nid.UID = 1
	nid.UFlags = NIF_TIP

	// Ограничиваем длину подсказки 128 символами
	if len(text) > 127 {
		text = text[:127]
	}
	copy(nid.SzTip[:], windows.StringToUTF16(text))

	shell32 := windows.NewLazySystemDLL("shell32.dll")
	procShellNotifyIcon := shell32.NewProc("Shell_NotifyIconW")
	result, _, err := procShellNotifyIcon.Call(
		uintptr(NIM_MODIFY),
		uintptr(unsafe.Pointer(&nid)),
	)
	if result == 0 {
		return err
	}

	return nil
}

// SetIcon обновляет иконку в системном трее
func (t *Tray) SetIcon(iconPath string) error {
	var hIcon uintptr
	var err error

	if iconPath == "" {
		// Загружаем системную иконку по умолчанию
		user32 := windows.NewLazySystemDLL("user32.dll")
		procLoadIcon := user32.NewProc("LoadIconW")
		hIcon, _, err = procLoadIcon.Call(
			0,
			uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("#32512"))),
		)
		if hIcon == 0 {
			return err
		}
	} else {
		// Загружаем иконку из файла
		user32 := windows.NewLazySystemDLL("user32.dll")
		procLoadImage := user32.NewProc("LoadImageW")
		hIcon, _, err = procLoadImage.Call(
			0,
			uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(iconPath))),
			1, // IMAGE_ICON
			0,
			0,
			0x00000010|0x00000002, // LR_DEFAULTSIZE|LR_LOADFROMFILE
		)
		if hIcon == 0 {
			return err
		}
	}

	if t.hIcon != 0 {
		// Уничтожаем старую иконку
		user32 := windows.NewLazySystemDLL("user32.dll")
		procDestroyIcon := user32.NewProc("DestroyIcon")
		procDestroyIcon.Call(t.hIcon)
	}

	t.hIcon = hIcon

	var nid NOTIFYICONDATA
	nid.CbSize = NOTIFYICONDATA_V2_SIZE
	nid.HWnd = t.hwnd
	nid.UID = 1
	nid.UFlags = NIF_ICON
	nid.HIcon = hIcon

	shell32 := windows.NewLazySystemDLL("shell32.dll")
	procShellNotifyIcon := shell32.NewProc("Shell_NotifyIconW")
	result, _, err := procShellNotifyIcon.Call(
		uintptr(NIM_MODIFY),
		uintptr(unsafe.Pointer(&nid)),
	)
	if result == 0 {
		return err
	}

	return nil
}

// ShowMenu показывает контекстное меню и возвращает ID выбранного пункта
func (t *Tray) ShowMenu() uint32 {
	user32 := windows.NewLazySystemDLL("user32.dll")

	// Создаём контекстное меню
	procCreatePopupMenu := user32.NewProc("CreatePopupMenu")
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return 0
	}
	defer func() {
		procDestroyMenu := user32.NewProc("DestroyMenu")
		procDestroyMenu.Call(hMenu)
	}()

	// Добавляем пункты меню
	const MF_STRING = 0x00000000
	const MF_ENABLED = 0x00000000
	const MF_GRAYED = 0x00000001

	procAppendMenu := user32.NewProc("AppendMenuW")
	_, _, _ = procAppendMenu.Call(
		hMenu,
		uintptr(MF_STRING|MF_ENABLED),
		uintptr(ID_TRAY_INFO),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Информация"))),
	)
	_, _, _ = procAppendMenu.Call(
		hMenu,
		uintptr(MF_STRING|MF_ENABLED),
		uintptr(ID_TRAY_TOGGLE_QUEUE),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Включить/Выключить очередь"))),
	)
	_, _, _ = procAppendMenu.Call(
		hMenu,
		uintptr(MF_STRING|MF_ENABLED),
		uintptr(ID_TRAY_SWITCH_ORDER),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Переключить порядок"))),
	)
	_, _, _ = procAppendMenu.Call(
		hMenu,
		uintptr(MF_STRING|MF_ENABLED),
		uintptr(ID_TRAY_CLEAR),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Очистить очередь"))),
	)
	_, _, _ = procAppendMenu.Call(
		hMenu,
		uintptr(MF_STRING|MF_ENABLED),
		uintptr(ID_TRAY_SETTINGS),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Настройки"))),
	)
	_, _, _ = procAppendMenu.Call(
		hMenu,
		uintptr(MF_STRING|MF_ENABLED),
		uintptr(ID_TRAY_EXIT),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Выход"))),
	)

	// Получаем позицию курсора
	var point struct {
		X int32
		Y int32
	}
	procGetCursorPos := user32.NewProc("GetCursorPos")
	ret, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	if ret == 0 {
		return 0
	}

	// Устанавливаем окно в передний план (фиксит проблему закрытия меню при клике вне)
	procSetForegroundWindow := user32.NewProc("SetForegroundWindow")
	procSetForegroundWindow.Call(t.hwnd)

	// Показываем контекстное меню
	const TPM_LEFTALIGN = 0x0000
	const TPM_TOPALIGN = 0x0000
	procTrackPopupMenu := user32.NewProc("TrackPopupMenu")
	selectedID, _, _ := procTrackPopupMenu.Call(
		hMenu,
		uintptr(TPM_RETURNCMD|TPM_LEFTALIGN|TPM_TOPALIGN),
		uintptr(point.X),
		uintptr(point.Y),
		0,
		t.hwnd,
		0,
	)

	return uint32(selectedID)
}

// Remove удаляет иконку из системного трея и очищает ресурсы
func (t *Tray) Remove() error {
	var nid NOTIFYICONDATA
	nid.CbSize = NOTIFYICONDATA_V2_SIZE
	nid.HWnd = t.hwnd
	nid.UID = 1

	shell32 := windows.NewLazySystemDLL("shell32.dll")
	procShellNotifyIcon := shell32.NewProc("Shell_NotifyIconW")
	result, _, err := procShellNotifyIcon.Call(
		uintptr(NIM_DELETE),
		uintptr(unsafe.Pointer(&nid)),
	)
	if result == 0 {
		return err
	}

	if t.hIcon != 0 {
		user32 := windows.NewLazySystemDLL("user32.dll")
		procDestroyIcon := user32.NewProc("DestroyIcon")
		procDestroyIcon.Call(t.hIcon)
		t.hIcon = 0
	}

	return nil
}
