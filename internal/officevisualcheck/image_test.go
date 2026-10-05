package officevisualcheck

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"testing"
)

func noisyPNG(t *testing.T, width int, height int) []byte {
	t.Helper()
	random := rand.New(rand.NewSource(1))
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for offset := 0; offset < len(picture.Pix); offset += 4 {
		picture.Pix[offset], picture.Pix[offset+1], picture.Pix[offset+2], picture.Pix[offset+3] = byte(random.Intn(256)), byte(random.Intn(256)), byte(random.Intn(256)), 255
	}
	var encoded bytes.Buffer
	if errorValue := png.Encode(&encoded, picture); errorValue != nil {
		t.Fatal(errorValue)
	}
	return encoded.Bytes()
}

func flatPNG(t *testing.T, width int, height int, fill color.Color) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.Set(x, y, fill)
		}
	}
	var encoded bytes.Buffer
	if errorValue := png.Encode(&encoded, picture); errorValue != nil {
		t.Fatal(errorValue)
	}
	return encoded.Bytes()
}

func TestAnImageWithinTheBudgetIsSentUntouched(t *testing.T) {
	original := flatPNG(t, 1600, 900, color.White)
	fitted, errorValue := fitImage(original)
	if errorValue != nil || !bytes.Equal(fitted.Data, original) || fitted.MediaType != "image/png" {
		t.Fatalf("fitted %d bytes as %s, error %v", len(fitted.Data), fitted.MediaType, errorValue)
	}
}

func TestAnImageOverTheBudgetIsDownscaledUntilItFits(t *testing.T) {
	original := noisyPNG(t, 1600, 900)
	if len(original) <= maximumImageBytes {
		t.Fatalf("the fixture is only %d bytes", len(original))
	}
	fitted, errorValue := fitImage(original)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(fitted.Data) > maximumImageBytes || fitted.MediaType != "image/jpeg" {
		t.Fatalf("%d bytes as %s", len(fitted.Data), fitted.MediaType)
	}
	picture, _, errorValue := image.Decode(bytes.NewReader(fitted.Data))
	if errorValue != nil || picture.Bounds().Dx() >= 1600 || picture.Bounds().Dx()*9 != picture.Bounds().Dy()*16 && absoluteDifference(picture.Bounds().Dx()*9, picture.Bounds().Dy()*16) > 16 {
		t.Fatalf("decoded %v, error %v", picture.Bounds(), errorValue)
	}
}

func absoluteDifference(left int, right int) int {
	if left > right {
		return left - right
	}
	return right - left
}

func TestBytesThatAreNotAnImageAreRefusedWhenTooLargeToSendAsTheyAre(t *testing.T) {
	if _, errorValue := fitImage(bytes.Repeat([]byte("x"), maximumImageBytes+1)); errorValue == nil {
		t.Fatal("undecodable oversized bytes were sent")
	}
}
