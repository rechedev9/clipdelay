package flv

import "testing"

func TestCompositionTime(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want int32
	}{
		{"avc frame", []byte{0x27, 1, 0, 0, 66, 0xAA}, 66},
		{"avc negative", []byte{0x27, 1, 0xFF, 0xFF, 0xDF}, -33},
		{"avc sequence header", []byte{0x17, 0, 0, 0, 66}, 0},
		{"enhanced coded frames", []byte{0x91, 'h', 'v', 'c', '1', 0, 0, 100, 0xAA}, 100},
		{"enhanced coded frames x", []byte{0x93, 'h', 'v', 'c', '1', 0xAA}, 0},
		{"audio-sized payload", []byte{0x27}, 0},
	}
	for _, c := range cases {
		if got := CompositionTime(c.data); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
