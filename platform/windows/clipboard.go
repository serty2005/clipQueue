package windows

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/serty2005/clipqueue/internal/logger"
)

// ContentType represents the type of clipboard content
type ContentType int

const (
	Empty ContentType = iota
	Text
	Files
	Image
)

// String returns a string representation of ContentType
func (t ContentType) String() string {
	switch t {
	case Empty:
		return "Empty"
	case Text:
		return "Text"
	case Files:
		return "Files"
	case Image:
		return "Image"
	default:
		return "Unknown"
	}
}

// ClipboardContent contains the clipboard data in a structured format
type ClipboardContent struct {
	Type      ContentType
	Text      string
	Files     []string
	ImagePNG  []byte
	SizeBytes int
	Preview   string
}

// readClipboardDIBBytes reads raw DIB data from clipboard without conversion
func readClipboardDIBBytes(format uint32) ([]byte, error) {
	handle, _, err := procGetClipboardData.Call(uintptr(format))
	if handle == 0 {
		return nil, err
	}

	ptr, _, err := procGlobalLock.Call(handle)
	if ptr == 0 {
		return nil, err
	}
	defer procGlobalUnlock.Call(handle)

	// Get DIB size
	size, _, err := procGlobalSize.Call(handle)
	const maxSize = 200 * 1024 * 1024 // 200MB limit
	if size == 0 || size > maxSize {
		return nil, fmt.Errorf("DIB data size %d exceeds limit %d", size, maxSize)
	}

	// Read DIB data
	dibData := make([]byte, size)
	src := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), size)
	copy(dibData, src)

	return dibData, nil
}

// Read reads the current clipboard content and returns it as ClipboardContent
func Read() (ClipboardContent, error) {
	var content ClipboardContent
	startTime := time.Now()

	// Open clipboard with retry/backoff
	if err := openClipboardWithRetry(); err != nil {
		logger.Error("Failed to open clipboard for reading: %v", err)
		logger.Debug("Total Read() duration: %v", time.Since(startTime))
		return content, err
	}
	clipboardOpenTime := time.Now()

	// Determine content type and read data
	if hasClipboardFormat(CF_HDROP) {
		content.Type = Files
		files, err := readHDrop()
		closeClipboard() // Close clipboard early since we've read all needed data
		logger.Debug("Clipboard open duration: %v", time.Since(clipboardOpenTime))

		if err != nil {
			logger.Error("Failed to read CF_HDROP: %v", err)
			logger.Debug("Total Read() duration: %v", time.Since(startTime))
			return content, err
		}
		content.Files = files
		content.SizeBytes = calculateFilesSize(files)
		content.Preview = formatFilesPreview(files)
	} else if hasClipboardFormat(CF_DIBV5) {
		dibData, err := readClipboardDIBBytes(CF_DIBV5)
		closeClipboard() // Close clipboard before DIB conversion
		logger.Debug("Clipboard open duration: %v", time.Since(clipboardOpenTime))

		if err == nil {
			imgData, err := dibToPNG(dibData)
			if err == nil {
				content.Type = Image
				content.ImagePNG = imgData
				content.SizeBytes = len(imgData)
				content.Preview = formatImagePreview(imgData)
			} else if err == ErrUnsupportedDIB {
				logger.Warn("Unsupported DIBV5 format, trying CF_DIB")

				// Try CF_DIB as fallback
				if err = openClipboardWithRetry(); err != nil {
					logger.Error("Failed to re-open clipboard for reading CF_DIB: %v", err)
					logger.Debug("Total Read() duration: %v", time.Since(startTime))
					return content, err
				}
				clipboardOpenTime = time.Now()

				if hasClipboardFormat(CF_DIB) {
					dibData, err = readClipboardDIBBytes(CF_DIB)
					closeClipboard() // Close clipboard again before conversion
					logger.Debug("Clipboard open duration: %v", time.Since(clipboardOpenTime))

					if err == nil {
						imgData, err = dibToPNG(dibData)
						if err == nil {
							content.Type = Image
							content.ImagePNG = imgData
							content.SizeBytes = len(imgData)
							content.Preview = formatImagePreview(imgData)
						} else if err != ErrUnsupportedDIB {
							logger.Error("Failed to convert DIB to PNG: %v", err)
							logger.Debug("Total Read() duration: %v", time.Since(startTime))
							return content, err
						} else {
							logger.Warn("Unsupported DIB format")
						}
					} else if err != ErrUnsupportedDIB {
						logger.Error("Failed to read CF_DIB: %v", err)
						logger.Debug("Total Read() duration: %v", time.Since(startTime))
						return content, err
					} else {
						logger.Warn("Unsupported DIB format")
					}
				} else {
					closeClipboard() // Close clipboard even if no CF_DIB
					logger.Debug("Clipboard open duration: %v", time.Since(clipboardOpenTime))
				}
			} else {
				logger.Error("Failed to convert DIBV5 to PNG: %v", err)
				logger.Debug("Total Read() duration: %v", time.Since(startTime))
				return content, err
			}
		} else {
			logger.Error("Failed to read CF_DIBV5: %v", err)
			logger.Debug("Total Read() duration: %v", time.Since(startTime))
			return content, err
		}
	} else if hasClipboardFormat(CF_DIB) {
		dibData, err := readClipboardDIBBytes(CF_DIB)
		closeClipboard() // Close clipboard before conversion
		logger.Debug("Clipboard open duration: %v", time.Since(clipboardOpenTime))

		if err == nil {
			imgData, err := dibToPNG(dibData)
			if err == nil {
				content.Type = Image
				content.ImagePNG = imgData
				content.SizeBytes = len(imgData)
				content.Preview = formatImagePreview(imgData)
			} else if err != ErrUnsupportedDIB {
				logger.Error("Failed to convert DIB to PNG: %v", err)
				logger.Debug("Total Read() duration: %v", time.Since(startTime))
				return content, err
			} else {
				logger.Warn("Unsupported DIB format")
			}
		} else if err != ErrUnsupportedDIB {
			logger.Error("Failed to read CF_DIB: %v", err)
			logger.Debug("Total Read() duration: %v", time.Since(startTime))
			return content, err
		} else {
			logger.Warn("Unsupported DIB format")
		}
	} else if hasClipboardFormat(CF_UNICODETEXT) {
		content.Type = Text
		text, err := readUnicodeText()
		closeClipboard() // Close clipboard early
		logger.Debug("Clipboard open duration: %v", time.Since(clipboardOpenTime))

		if err != nil {
			logger.Error("Failed to read CF_UNICODETEXT: %v", err)
			logger.Debug("Total Read() duration: %v", time.Since(startTime))
			return content, err
		}
		content.Text = text
		content.SizeBytes = len([]byte(text))
		content.Preview = formatTextPreview(text)
	} else {
		closeClipboard() // Close clipboard for empty case
		logger.Debug("Clipboard open duration: %v", time.Since(clipboardOpenTime))
		content.Preview = "Empty clipboard"
	}

	logger.Debug("Total Read() duration: %v", time.Since(startTime))
	return content, nil
}

// Write writes the given ClipboardContent to the clipboard
func Write(content ClipboardContent) error {
	startTime := time.Now()

	// Special case: clearing clipboard
	if content.Type == Empty {
		if err := openClipboardWithRetry(); err != nil {
			logger.Error("Failed to open clipboard for clearing: %v", err)
			return err
		}

		if err := emptyClipboard(); err != nil {
			logger.Error("Failed to empty clipboard: %v", err)
			closeClipboard()
			return err
		}

		closeClipboard()
		lastWriteSeq.Store(GetClipboardSequenceNumber())
		logger.Debug("Total Write() duration (clear): %v", time.Since(startTime))
		return nil
	}

	// Prepare payloads BEFORE opening clipboard
	var (
		textHandle  uintptr
		filesHandle uintptr
		imageHandle uintptr
		err         error
	)

	switch content.Type {
	case Text:
		// Convert to UTF-16 with null terminator
		var utf16Str []uint16
		utf16Str, err = syscall.UTF16FromString(content.Text)
		if err != nil {
			logger.Error("Failed to convert text to UTF-16: %v", err)
			return err
		}
		// Allocate global memory
		size := len(utf16Str) * 2
		textHandle, _, err = procGlobalAlloc.Call(GMEM_MOVEABLE|GMEM_DDESHARE, uintptr(size))
		if textHandle == 0 {
			logger.Error("Failed to allocate memory for text: %v", err)
			return err
		}
		// Lock memory and copy data
		var ptr uintptr
		ptr, _, err = procGlobalLock.Call(textHandle)
		if ptr == 0 {
			procGlobalFree.Call(textHandle)
			logger.Error("Failed to lock memory for text: %v", err)
			return err
		}
		// Safe copy without giant-slice
		dst := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), size)
		src := unsafe.Slice((*byte)(unsafe.Pointer(&utf16Str[0])), size)
		copy(dst, src)
		procGlobalUnlock.Call(textHandle)

	case Files:
		// Calculate buffer size
		var bufferSize = int(unsafe.Sizeof(DROPFILES{}))
		var pathData []byte

		for _, file := range content.Files {
			utf16Str, err := syscall.UTF16FromString(file)
			if err != nil {
				continue
			}
			// Add UTF-16 string with null terminator
			pathBytes := unsafe.Slice((*byte)(unsafe.Pointer(&utf16Str[0])), len(utf16Str)*2)
			pathData = append(pathData, pathBytes...)
		}

		// Add final double null terminator
		pathData = append(pathData, 0, 0)
		bufferSize += len(pathData)

		// Allocate memory
		filesHandle, _, err = procGlobalAlloc.Call(GMEM_MOVEABLE|GMEM_DDESHARE, uintptr(bufferSize))
		if filesHandle == 0 {
			logger.Error("Failed to allocate memory for files: %v", err)
			return err
		}
		// Lock memory
		var ptrFiles uintptr
		ptrFiles, _, err = procGlobalLock.Call(filesHandle)
		if ptrFiles == 0 {
			procGlobalFree.Call(filesHandle)
			logger.Error("Failed to lock memory for files: %v", err)
			return err
		}

		// Initialize DROPFILES structure
		var df DROPFILES
		df.pFiles = uint32(unsafe.Sizeof(DROPFILES{}))
		df.fWide = 1 // Unicode

		// Copy DROPFILES to memory
		dfBytes := unsafe.Slice((*byte)(unsafe.Pointer(&df)), unsafe.Sizeof(DROPFILES{}))
		dst := unsafe.Slice((*byte)(unsafe.Pointer(ptrFiles)), bufferSize)
		copy(dst[:unsafe.Sizeof(DROPFILES{})], dfBytes)

		// Write file paths
		copy(dst[unsafe.Sizeof(DROPFILES{}):], pathData)

		// Unlock immediately after filling the buffer
		procGlobalUnlock.Call(filesHandle)

	case Image:
		// Decode PNG to image
		var img image.Image
		img, err = png.Decode(bytes.NewReader(content.ImagePNG))
		if err != nil {
			logger.Error("Failed to decode PNG image: %v", err)
			return err
		}
		// Convert image to DIB
		var dibData []byte
		dibData, err = imageToDIB(img)
		if err != nil {
			logger.Error("Failed to convert image to DIB: %v", err)
			return err
		}
		// Allocate memory
		imageHandle, _, err = procGlobalAlloc.Call(GMEM_MOVEABLE|GMEM_DDESHARE, uintptr(len(dibData)))
		if imageHandle == 0 {
			logger.Error("Failed to allocate memory for DIB: %v", err)
			return err
		}
		// Lock memory and copy data
		var ptrImage uintptr
		ptrImage, _, err = procGlobalLock.Call(imageHandle)
		if ptrImage == 0 {
			procGlobalFree.Call(imageHandle)
			logger.Error("Failed to lock memory for DIB: %v", err)
			return err
		}
		// Safe copy without giant-slice
		dst := unsafe.Slice((*byte)(unsafe.Pointer(ptrImage)), len(dibData))
		copy(dst, dibData)
		procGlobalUnlock.Call(imageHandle)
	}

	// Check if we have a valid handle for the content type
	validHandle := false
	switch content.Type {
	case Text:
		validHandle = textHandle != 0
	case Files:
		validHandle = filesHandle != 0
	case Image:
		validHandle = imageHandle != 0
	}

	if !validHandle {
		// Free allocated memory if there was an error before opening clipboard
		if textHandle != 0 {
			procGlobalFree.Call(textHandle)
		}
		if filesHandle != 0 {
			procGlobalFree.Call(filesHandle)
		}
		if imageHandle != 0 {
			procGlobalFree.Call(imageHandle)
		}
		return fmt.Errorf("failed to prepare clipboard content: no valid handle created")
	}

	// Open clipboard with retry/backoff
	var clipboardOpenTime time.Time
	if err = openClipboardWithRetry(); err != nil {
		logger.Error("Failed to open clipboard for writing: %v", err)
		// Free allocated memory if clipboard couldn't be opened
		if textHandle != 0 {
			procGlobalFree.Call(textHandle)
		}
		if filesHandle != 0 {
			procGlobalFree.Call(filesHandle)
		}
		if imageHandle != 0 {
			procGlobalFree.Call(imageHandle)
		}
		return err
	}
	clipboardOpenTime = time.Now()

	// Empty clipboard before writing
	if err = emptyClipboard(); err != nil {
		logger.Error("Failed to empty clipboard: %v", err)
		closeClipboard()
		// Free allocated memory if clipboard couldn't be emptied
		if textHandle != 0 {
			procGlobalFree.Call(textHandle)
		}
		if filesHandle != 0 {
			procGlobalFree.Call(filesHandle)
		}
		if imageHandle != 0 {
			procGlobalFree.Call(imageHandle)
		}
		return err
	}

	// Write content based on type (fast SetClipboardData calls)
	switch content.Type {
	case Text:
		ret, _, sysErr := procSetClipboardData.Call(CF_UNICODETEXT, textHandle)
		if ret == 0 {
			procGlobalFree.Call(textHandle)
			closeClipboard()
			if sysErr != nil && sysErr.Error() != "The operation completed successfully." {
				logger.Error("Failed to write CF_UNICODETEXT: %v", sysErr)
				return sysErr
			}
		}
	case Files:
		ret, _, sysErr := procSetClipboardData.Call(CF_HDROP, filesHandle)
		if ret == 0 {
			procGlobalFree.Call(filesHandle)
			closeClipboard()
			if sysErr != nil && sysErr.Error() != "The operation completed successfully." {
				logger.Error("Failed to write CF_HDROP: %v", sysErr)
				return sysErr
			}
		}
	case Image:
		ret, _, sysErr := procSetClipboardData.Call(CF_DIB, imageHandle)
		if ret == 0 {
			procGlobalFree.Call(imageHandle)
			closeClipboard()
			if sysErr != nil && sysErr.Error() != "The operation completed successfully." {
				logger.Error("Failed to write CF_DIB: %v", sysErr)
				return sysErr
			}
		}
	}

	closeClipboard()

	// Update last write sequence number
	lastWriteSeq.Store(GetClipboardSequenceNumber())

	// Log timings
	logger.Debug("Clipboard open duration: %v", time.Since(clipboardOpenTime))
	logger.Debug("Total Write() duration: %v", time.Since(startTime))

	// The operation completed successfully
	return nil
}

// openClipboardWithRetry opens the clipboard with retry logic and exponential backoff
func openClipboardWithRetry() error {
	const maxRetries = 5
	const initialDelay = 50 * time.Millisecond
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		if err := openClipboard(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(initialDelay * (1 << uint(i)))
	}

	return lastErr
}

// Helper functions for clipboard operations
func hasClipboardFormat(format uint32) bool {
	ret, _, _ := procIsClipboardFormatAvailable.Call(uintptr(format))
	return ret != 0
}

func calculateFilesSize(files []string) int {
	size := 0
	for _, file := range files {
		// Note: This is a simplified calculation. For accurate size, we should stat each file.
		size += len(file) * 2 // UTF-16 encoding
	}
	size += 2 // Double null terminator
	size += int(unsafe.Sizeof(DROPFILES{}))
	return size
}

func formatTextPreview(text string) string {
	const maxLength = 80
	if len(text) <= maxLength {
		return text
	}
	return text[:maxLength] + "..."
}

func formatFilesPreview(files []string) string {
	const maxFiles = 3
	var preview string
	for i, file := range files {
		if i >= maxFiles {
			preview += ", ..."
			break
		}
		if i > 0 {
			preview += ", "
		}
		preview += file
	}
	return preview
}

func formatImagePreview(imgData []byte) string {
	config, err := png.DecodeConfig(bytes.NewReader(imgData))
	if err != nil {
		return "Invalid PNG image"
	}
	return fmt.Sprintf("%dx%d PNG", config.Width, config.Height)
}

// Windows API constants
const (
	CF_UNICODETEXT = 13
	CF_HDROP       = 15
	CF_DIB         = 8
	CF_DIBV5       = 17
)

// Windows API functions
var (
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procEmptyClipboard             = user32.NewProc("EmptyClipboard")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procSetClipboardData           = user32.NewProc("SetClipboardData")
	procGlobalAlloc                = kernel32.NewProc("GlobalAlloc")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
	procGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
)

var lastWriteSeq atomic.Uint32

// GetClipboardSequenceNumber retrieves the current clipboard sequence number
func GetClipboardSequenceNumber() uint32 {
	ret, _, _ := procGetClipboardSequenceNumber.Call()
	return uint32(ret)
}

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	shell32        = syscall.NewLazyDLL("shell32.dll")
	procGlobalFree = kernel32.NewProc("GlobalFree")
)

func openClipboard() error {
	ret, _, err := procOpenClipboard.Call(0)
	if ret == 0 {
		return err
	}
	return nil
}

func closeClipboard() {
	procCloseClipboard.Call()
}

func emptyClipboard() error {
	ret, _, err := procEmptyClipboard.Call()
	if ret == 0 {
		return err
	}
	return nil
}

// readUnicodeText reads CF_UNICODETEXT from clipboard
func readUnicodeText() (string, error) {
	handle, _, err := procGetClipboardData.Call(CF_UNICODETEXT)
	if handle == 0 {
		return "", err
	}

	ptr, _, err := procGlobalLock.Call(handle)
	if ptr == 0 {
		return "", err
	}
	defer procGlobalUnlock.Call(handle)

	// Get data size
	size, _, err := procGlobalSize.Call(handle)
	if size == 0 || size > 100*1024*1024 { // Limit to 100MB
		return "", err
	}

	// Read UTF-16 string from pointer
	utf16Slice := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), size/2)
	for i, c := range utf16Slice {
		if c == 0 {
			return syscall.UTF16ToString(utf16Slice[:i]), nil
		}
	}

	return syscall.UTF16ToString(utf16Slice), nil
}

// DROPFILES structure for CF_HDROP
type DROPFILES struct {
	pFiles uint32 // Offset of file list in bytes from start of this struct
	pt     struct{ X, Y int32 }
	fNC    uint32
	fWide  uint32
}

// readHDrop reads CF_HDROP from clipboard and returns list of files
func readHDrop() ([]string, error) {
	handle, _, err := procGetClipboardData.Call(CF_HDROP)
	if handle == 0 {
		return nil, err
	}

	// Get number of files
	dragQueryFileW := shell32.NewProc("DragQueryFileW")
	count, _, _ := dragQueryFileW.Call(handle, 0xFFFFFFFF, 0, 0)

	// Get each file path
	var files []string
	for i := uint32(0); i < uint32(count); i++ {
		// First, get the length of the file path
		pathLen, _, _ := dragQueryFileW.Call(handle, uintptr(i), 0, 0)
		if pathLen == 0 {
			continue
		}

		// Allocate buffer for the path
		buffer := make([]uint16, pathLen+1)
		n, _, _ := dragQueryFileW.Call(handle, uintptr(i), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
		if n > 0 {
			files = append(files, syscall.UTF16ToString(buffer))
		}
	}

	return files, nil
}

// imageToDIB converts an image to DIB format (BITMAPINFOHEADER 40, 32bpp BGRA)
func imageToDIB(img image.Image) ([]byte, error) {
	// Convert image to RGBA
	rgba, ok := img.(*image.RGBA)
	if !ok {
		rgba = image.NewRGBA(img.Bounds())
		draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)
	}

	bounds := rgba.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// BITMAPINFOHEADER for 32bpp BGRA
	var bmi BITMAPINFOHEADER
	bmi.biSize = 40
	bmi.biWidth = int32(width)
	bmi.biHeight = int32(height) // Bottom-up image (standard for DIB)
	bmi.biPlanes = 1
	bmi.biBitCount = 32
	bmi.biCompression = BI_RGB
	rowSize := ((width*4 + 3) / 4) * 4
	bmi.biSizeImage = uint32(rowSize * height)
	bmi.biXPelsPerMeter = 2835 // 72 DPI
	bmi.biYPelsPerMeter = 2835
	bmi.biClrUsed = 0
	bmi.biClrImportant = 0

	// Calculate buffer size
	bufferSize := int(bmi.biSize) + int(bmi.biSizeImage)

	// Create buffer
	buffer := make([]byte, bufferSize)

	// Write BITMAPINFOHEADER
	binary.LittleEndian.PutUint32(buffer[0:4], bmi.biSize)
	binary.LittleEndian.PutUint32(buffer[4:8], uint32(bmi.biWidth))
	binary.LittleEndian.PutUint32(buffer[8:12], uint32(bmi.biHeight))
	binary.LittleEndian.PutUint16(buffer[12:14], uint16(bmi.biPlanes))
	binary.LittleEndian.PutUint16(buffer[14:16], uint16(bmi.biBitCount))
	binary.LittleEndian.PutUint32(buffer[16:20], bmi.biCompression)
	binary.LittleEndian.PutUint32(buffer[20:24], bmi.biSizeImage)
	binary.LittleEndian.PutUint32(buffer[24:28], uint32(bmi.biXPelsPerMeter))
	binary.LittleEndian.PutUint32(buffer[28:32], uint32(bmi.biYPelsPerMeter))
	binary.LittleEndian.PutUint32(buffer[32:36], bmi.biClrUsed)
	binary.LittleEndian.PutUint32(buffer[36:40], bmi.biClrImportant)

	// Write pixel data (BGRA format)
	pixelOffset := int(bmi.biSize)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// For bottom-up DIB, first row in buffer is bottom row of image
			bufY := height - 1 - y
			r, g, b, a := rgba.At(x, y).RGBA()
			index := pixelOffset + bufY*rowSize + x*4
			buffer[index] = byte(b >> 8)
			buffer[index+1] = byte(g >> 8)
			buffer[index+2] = byte(r >> 8)
			buffer[index+3] = byte(a >> 8)
		}
	}

	return buffer, nil
}

// ErrUnsupportedDIB is returned when DIB format is not supported
var ErrUnsupportedDIB = fmt.Errorf("unsupported DIB format")

// dibToPNG converts DIB data to PNG format
func dibToPNG(dibData []byte) ([]byte, error) {
	// Check if DIB data has BITMAPINFOHEADER
	if len(dibData) < 40 { // BITMAPINFOHEADER size is 40 bytes
		logger.Warn("DIB data too short for BITMAPINFOHEADER")
		return nil, ErrUnsupportedDIB
	}

	// Read BITMAPINFOHEADER
	var bmi BITMAPINFOHEADER
	bmi.biSize = binary.LittleEndian.Uint32(dibData[0:4])
	bmi.biWidth = int32(binary.LittleEndian.Uint32(dibData[4:8]))
	bmi.biHeight = int32(binary.LittleEndian.Uint32(dibData[8:12]))
	bmi.biPlanes = int16(binary.LittleEndian.Uint16(dibData[12:14]))
	bmi.biBitCount = int16(binary.LittleEndian.Uint16(dibData[14:16]))
	bmi.biCompression = binary.LittleEndian.Uint32(dibData[16:20])
	bmi.biSizeImage = binary.LittleEndian.Uint32(dibData[20:24])
	bmi.biXPelsPerMeter = int32(binary.LittleEndian.Uint32(dibData[24:28]))
	bmi.biYPelsPerMeter = int32(binary.LittleEndian.Uint32(dibData[28:32]))
	bmi.biClrUsed = binary.LittleEndian.Uint32(dibData[32:36])
	bmi.biClrImportant = binary.LittleEndian.Uint32(dibData[36:40])

	// Validate DIB dimensions and size
	if bmi.biWidth <= 0 {
		logger.Warn("Invalid DIB width: %d", bmi.biWidth)
		return nil, ErrUnsupportedDIB
	}

	height := bmi.biHeight
	if height == 0 {
		logger.Warn("Invalid DIB height: %d", height)
		return nil, ErrUnsupportedDIB
	}

	if height < 0 {
		height = -height // Convert to absolute value for top-down DIB
	}

	if int(bmi.biSize) > len(dibData) {
		logger.Warn("DIB header size %d exceeds buffer size %d", bmi.biSize, len(dibData))
		return nil, ErrUnsupportedDIB
	}

	// Currently support 24bpp BGR and 32bpp BGRA (BI_RGB or BI_BITFIELDS with standard masks)
	if (bmi.biBitCount != 24 && bmi.biBitCount != 32) ||
		(bmi.biBitCount == 24 && bmi.biCompression != BI_RGB) ||
		(bmi.biBitCount == 32 && bmi.biCompression != BI_RGB && bmi.biCompression != BI_BITFIELDS) {
		logger.Warn("Only 24bpp BGR (BI_RGB) and 32bpp BGRA (BI_RGB or BI_BITFIELDS) DIBs are supported currently (got %dbpp, compression: %d)",
			bmi.biBitCount, bmi.biCompression)
		return nil, ErrUnsupportedDIB
	}

	// Calculate pixel data offset
	var pixelOffset = int(bmi.biSize)
	if bmi.biClrUsed > 0 || (bmi.biBitCount <= 8 && bmi.biClrUsed == 0) {
		colorsCount := 1 << bmi.biBitCount
		if bmi.biClrUsed > 0 && bmi.biClrUsed < uint32(colorsCount) {
			colorsCount = int(bmi.biClrUsed)
		}
		pixelOffset += colorsCount * 4 // Each color in RGBQUAD is 4 bytes
	}

	// For BI_BITFIELDS with 32bpp, we need to skip color masks (3 DWORDs = 12 bytes)
	if bmi.biCompression == BI_BITFIELDS {
		pixelOffset += 12 // 3 masks (R, G, B) each 4 bytes
	}

	// Calculate row stride
	bpp := int(bmi.biBitCount) / 8
	rowSize := ((int(bmi.biWidth)*bpp + 3) / 4) * 4

	// Determine if image is top-down or bottom-up
	isTopDown := bmi.biHeight < 0
	h := bmi.biHeight
	if isTopDown {
		h = -h
	}

	// Check if we have enough data for pixels
	expectedSize := pixelOffset + int(h)*rowSize
	if len(dibData) < expectedSize {
		logger.Warn("DIB data too short for pixel data. Expected: %d, Got: %d", expectedSize, len(dibData))
		return nil, ErrUnsupportedDIB
	}

	// Create RGBA image
	img := image.NewRGBA(image.Rect(0, 0, int(bmi.biWidth), int(height)))

	// Get pixel data
	pixelData := dibData[pixelOffset:]

	// Copy pixels from DIB to RGBA, taking into account stride
	for y := 0; y < int(height); y++ {
		// Calculate row start in DIB pixel data
		var rowStart int
		if isTopDown {
			rowStart = y * rowSize
		} else {
			rowStart = (int(height) - 1 - y) * rowSize
		}

		// Copy row pixels
		for x := 0; x < int(bmi.biWidth); x++ {
			index := rowStart + x*bpp

			var r, g, b, a byte
			switch bmi.biBitCount {
			case 32:
				// DIB pixels are stored as BGRA (for both BI_RGB and BI_BITFIELDS with standard masks)
				b = pixelData[index]
				g = pixelData[index+1]
				r = pixelData[index+2]
				a = pixelData[index+3]
			case 24:
				// DIB pixels are stored as BGR
				b = pixelData[index]
				g = pixelData[index+1]
				r = pixelData[index+2]
				a = 255 // Opaque
			}

			// RGBA pixels are stored as RGBA
			img.SetRGBA(x, y, color.RGBA{r, g, b, a})
		}
	}

	// Encode to PNG
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		logger.Error("Failed to encode PNG: %v", err)
		return []byte{}, err
	}

	return buf.Bytes(), nil
}

// BITMAPINFOHEADER structure
type BITMAPINFOHEADER struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        int16
	biBitCount      int16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

// BI_RGB and BI_BITFIELDS constants
const BI_RGB = 0
const BI_BITFIELDS = 3

// Global memory allocation constants
const (
	GMEM_MOVEABLE = 0x0002
	GMEM_DDESHARE = 0x2000
)
