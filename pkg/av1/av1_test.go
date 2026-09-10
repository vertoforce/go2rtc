package av1

import (
	"encoding/hex"
	"testing"
)

// av1C records captured from live go2rtc stream.mp4 output, extracted
// from the moov>trak>stsd>av01>av1C box.
func TestWidthHeight(t *testing.T) {
	tests := []struct {
		name          string
		av1c          string
		width, height uint16
	}{
		{"portrait_1080x1920", "81080c000a0e00000042aa1bf7f0086640404041", 1080, 1920},
		{"wide_4096x1552", "810c0c000a0c0000006329fff83c02198040", 4096, 1552},
		{"2560x1920", "810c0c000a0c00000062ea7ffbf804330080", 2560, 1920},
		{"4k_3840x2160", "810c0c000a0c00000062efbfe1bc02198040", 3840, 2160},
		{"1080p", "81080c000a0e00000042abbfc370086640404041", 1920, 1080},
		{"4k_3840x2160_b", "810c0c000a0c00000062efbfe1bc02198040", 3840, 2160},
	}
	for _, tt := range tests {
		conf, err := hex.DecodeString(tt.av1c)
		if err != nil {
			t.Fatal(err)
		}
		w, h := WidthHeight(conf)
		if w != tt.width || h != tt.height {
			t.Errorf("%s: got %dx%d, want %dx%d", tt.name, w, h, tt.width, tt.height)
		}
	}
}

func TestWidthHeightFallback(t *testing.T) {
	for _, conf := range [][]byte{
		nil,
		{},
		{0x81, 0x08, 0x0c, 0x00},             // minimal av1C, no configOBUs
		{0x81, 0x08, 0x0c, 0x00, 0x0a},       // truncated OBU header
		{0x81, 0x08, 0x0c, 0x00, 0x0a, 0x20}, // size exceeds available bytes
	} {
		if w, h := WidthHeight(conf); w != 0 || h != 0 {
			t.Errorf("conf %x: got %dx%d, want 0x0", conf, w, h)
		}
	}
}
