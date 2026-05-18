package windows

import "testing"

func TestClipboardContentNeedsImageCapture(t *testing.T) {
	item := ClipboardContent{
		Type:      Image,
		SourceSeq: 42,
	}

	if !item.NeedsImageCapture() {
		t.Fatal("ожидался признак ожидающего захвата изображения")
	}
}

func TestClipboardContentWithPayloadDoesNotNeedImageCapture(t *testing.T) {
	item := ClipboardContent{
		Type:      Image,
		SourceSeq: 42,
		ImagePNG:  []byte{1, 2, 3},
	}

	if item.NeedsImageCapture() {
		t.Fatal("изображение с локальным payload не должно ожидать захвата")
	}
}
