package media

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"io"
	"strconv"
)

// readPPMFrame reads one binary RGB frame from FFmpeg's image pipe. A fresh
// RGBA image is returned because the UI can retain it until its next update.
func readPPMFrame(r *bufio.Reader) (*image.RGBA, error) {
	const maxHeader = 4096
	read := 0
	nextByte := func() (byte, error) {
		if read >= maxHeader {
			return 0, errors.New("PPM header too large")
		}
		b, err := r.ReadByte()
		if err == nil {
			read++
		}
		return b, err
	}
	space := func(b byte) bool {
		return b == ' ' || b == '\n' || b == '\r' || b == '\t' || b == '\v' || b == '\f'
	}
	nextToken := func() (string, error) {
		var token []byte
		for {
			b, err := nextByte()
			if err != nil {
				return "", err
			}
			if b == '#' {
				for b != '\n' {
					b, err = nextByte()
					if err != nil {
						return "", err
					}
				}
				continue
			}
			if !space(b) {
				token = append(token, b)
				break
			}
		}
		for {
			b, err := nextByte()
			if err != nil {
				return "", err
			}
			if space(b) {
				return string(token), nil
			}
			if len(token) >= 32 {
				return "", errors.New("PPM header token too long")
			}
			token = append(token, b)
		}
	}
	magic, err := nextToken()
	if err != nil || magic != "P6" {
		return nil, errors.New("invalid PPM signature")
	}
	widthText, err := nextToken()
	if err != nil {
		return nil, fmt.Errorf("read PPM width: %w", err)
	}
	heightText, err := nextToken()
	if err != nil {
		return nil, fmt.Errorf("read PPM height: %w", err)
	}
	maxText, err := nextToken()
	if err != nil {
		return nil, fmt.Errorf("read PPM depth: %w", err)
	}
	width, widthErr := strconv.Atoi(widthText)
	height, heightErr := strconv.Atoi(heightText)
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 || width > 8192 || height > 8192 || int64(width)*int64(height) > 16<<20 || maxText != "255" {
		return nil, errors.New("invalid PPM dimensions or depth")
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	row := make([]byte, width*3)
	for y := 0; y < height; y++ {
		if _, err := io.ReadFull(r, row); err != nil {
			return nil, fmt.Errorf("read PPM pixels: %w", err)
		}
		dst := img.Pix[y*img.Stride:]
		for x := 0; x < width; x++ {
			dst[4*x], dst[4*x+1], dst[4*x+2], dst[4*x+3] = row[3*x], row[3*x+1], row[3*x+2], 255
		}
	}
	return img, nil
}
