package media

import (
	"bufio"
	"bytes"
	"image/color"
	"strings"
	"testing"
)

func TestReadPPMFrames(t *testing.T) {
	data := append([]byte("P6\n# comment\n2 1\n255\n"), []byte{10, 35, 32, 255, 0, 1}...)
	data = append(data, []byte("P6\n1 1\n255\n")...)
	data = append(data, 2, 3, 4)
	r := bufio.NewReader(bytes.NewReader(data))
	first, err := readPPMFrame(r)
	if err != nil {
		t.Fatal(err)
	}
	if first.RGBAAt(0, 0) != (color.RGBA{R: 10, G: 35, B: 32, A: 255}) || first.RGBAAt(1, 0) != (color.RGBA{R: 255, G: 0, B: 1, A: 255}) {
		t.Fatal("incorrect first frame pixels")
	}
	second, err := readPPMFrame(r)
	if err != nil || second.RGBAAt(0, 0) != (color.RGBA{R: 2, G: 3, B: 4, A: 255}) {
		t.Fatalf("second frame=%v error=%v", second, err)
	}
}

func TestReadPPMRejectsMalformedFrames(t *testing.T) {
	for _, input := range []string{
		"P3\n1 1\n255\nabc",
		"P6\n0 1\n255\nabc",
		"P6\n8192 8192\n255\nabc",
		"P6\n1 1\n65535\nabc",
		"P6\n1 1\n255\nab",
		"P6\n#" + strings.Repeat("x", 4096),
	} {
		if _, err := readPPMFrame(bufio.NewReader(strings.NewReader(input))); err == nil {
			t.Fatalf("accepted malformed PPM %q", input[:min(len(input), 32)])
		}
	}
}
