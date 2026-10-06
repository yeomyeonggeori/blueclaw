package officevisualcheck

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

const (
	maximumImageBytes      = 200_000
	minimumImageSide       = 320
	imageShrinkNumerator   = 4
	imageShrinkDenominator = 5
	jpegQuality            = 85
	jpegMediaType          = "image/jpeg"
)

func fitImage(data []byte) (model.DecisionImage, error) {
	if len(data) <= maximumImageBytes {
		return model.DecisionImage{MediaType: imageMediaType, Data: data}, nil
	}
	picture, _, errorValue := image.Decode(bytes.NewReader(data))
	if errorValue != nil {
		return model.DecisionImage{}, fmt.Errorf("the render is %d bytes and cannot be shrunk: %w", len(data), errorValue)
	}
	for picture.Bounds().Dx() >= minimumImageSide && picture.Bounds().Dy() >= minimumImageSide {
		encoded, errorValue := encodedJPEG(picture)
		if errorValue != nil {
			return model.DecisionImage{}, errorValue
		}
		if len(encoded) <= maximumImageBytes {
			return model.DecisionImage{MediaType: jpegMediaType, Data: encoded}, nil
		}
		picture = scaledDown(picture, imageShrinkNumerator, imageShrinkDenominator)
	}
	return model.DecisionImage{}, fmt.Errorf("the render does not fit %d bytes above %d pixels", maximumImageBytes, minimumImageSide)
}

func encodedJPEG(picture image.Image) ([]byte, error) {
	var encoded bytes.Buffer
	if errorValue := jpeg.Encode(&encoded, picture, &jpeg.Options{Quality: jpegQuality}); errorValue != nil {
		return nil, errorValue
	}
	return encoded.Bytes(), nil
}

func scaledDown(source image.Image, numerator int, denominator int) *image.RGBA {
	bounds := source.Bounds()
	return resized(source, bounds.Dx()*numerator/denominator, bounds.Dy()*numerator/denominator)
}

func resized(source image.Image, width int, height int) *image.RGBA {
	bounds := source.Bounds()
	opaque := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(opaque, opaque.Bounds(), source, bounds.Min, draw.Src)
	scaled := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			scaled.SetRGBA(x, y, areaAverage(opaque, spanOf(x, width, bounds.Dx()), spanOf(y, height, bounds.Dy())))
		}
	}
	return scaled
}

type span struct{ start, end int }

func spanOf(index int, scaledLength int, sourceLength int) span {
	start := index * sourceLength / scaledLength
	return span{start: start, end: max(start+1, (index+1)*sourceLength/scaledLength)}
}

func areaAverage(source *image.RGBA, columns span, rows span) color.RGBA {
	var red, green, blue, count int
	for y := rows.start; y < rows.end; y++ {
		for x := columns.start; x < columns.end; x++ {
			pixel := source.RGBAAt(x, y)
			red, green, blue, count = red+int(pixel.R), green+int(pixel.G), blue+int(pixel.B), count+1
		}
	}
	return color.RGBA{R: uint8(red / count), G: uint8(green / count), B: uint8(blue / count), A: 255}
}
