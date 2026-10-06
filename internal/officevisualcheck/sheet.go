package officevisualcheck

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strconv"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

const (
	sheetTileWidth  = 400
	sheetGap        = 8
	labelPixel      = 4
	labelPadding    = 2
	glyphColumns    = 3
	glyphRows       = 5
	glyphSpacing    = 1
	sheetBackground = 255
)

var digitGlyphs = [10][glyphRows]string{
	{"###", "# #", "# #", "# #", "###"},
	{" # ", "## ", " # ", " # ", "###"},
	{"###", "  #", "###", "#  ", "###"},
	{"###", "  #", "###", "  #", "###"},
	{"# #", "# #", "###", "  #", "  #"},
	{"###", "#  ", "###", "  #", "###"},
	{"###", "#  ", "###", "# #", "###"},
	{"###", "  #", "  #", "  #", "  #"},
	{"###", "# #", "###", "# #", "###"},
	{"###", "# #", "###", "  #", "###"},
}

func sheetColumns(count int) int {
	return int(math.Ceil(math.Sqrt(float64(count))))
}

func contactSheet(renders [][]byte) (model.DecisionImage, error) {
	if len(renders) == 0 {
		return model.DecisionImage{}, errors.New("a contact sheet needs at least one render")
	}
	tiles := make([]image.Image, len(renders))
	for index, render := range renders {
		tile, _, errorValue := image.Decode(bytes.NewReader(render))
		if errorValue != nil {
			return model.DecisionImage{}, fmt.Errorf("decode render %d for the contact sheet: %w", index+1, errorValue)
		}
		tiles[index] = tile
	}
	encoded, errorValue := encodedPNG(laidOut(tiles))
	if errorValue != nil {
		return model.DecisionImage{}, errorValue
	}
	return fitImage(encoded)
}

func laidOut(tiles []image.Image) *image.RGBA {
	columns := sheetColumns(len(tiles))
	rows := (len(tiles) + columns - 1) / columns
	first := tiles[0].Bounds()
	tileHeight := sheetTileWidth * first.Dy() / first.Dx()
	sheet := image.NewRGBA(image.Rect(0, 0, columns*sheetTileWidth+(columns+1)*sheetGap, rows*tileHeight+(rows+1)*sheetGap))
	draw.Draw(sheet, sheet.Bounds(), image.NewUniform(color.Gray{Y: sheetBackground}), image.Point{}, draw.Src)
	for index, tile := range tiles {
		origin := image.Pt(sheetGap+(index%columns)*(sheetTileWidth+sheetGap), sheetGap+(index/columns)*(tileHeight+sheetGap))
		draw.Draw(sheet, image.Rectangle{Min: origin, Max: origin.Add(image.Pt(sheetTileWidth, tileHeight))}, resized(tile, sheetTileWidth, tileHeight), image.Point{}, draw.Src)
		drawLabel(sheet, origin, index+1)
	}
	return sheet
}

func drawLabel(sheet *image.RGBA, origin image.Point, number int) {
	digits := strconv.Itoa(number)
	width := (len(digits)*(glyphColumns+glyphSpacing)-glyphSpacing)*labelPixel + 2*labelPadding*labelPixel
	height := glyphRows*labelPixel + 2*labelPadding*labelPixel
	fillRectangle(sheet, image.Rectangle{Min: origin, Max: origin.Add(image.Pt(width, height))}, color.Black)
	for position, digit := range digits {
		glyph := digitGlyphs[digit-'0']
		for row, line := range glyph {
			for column, cell := range line {
				if cell == '#' {
					left := origin.X + (labelPadding+position*(glyphColumns+glyphSpacing)+column)*labelPixel
					top := origin.Y + (labelPadding+row)*labelPixel
					fillRectangle(sheet, image.Rect(left, top, left+labelPixel, top+labelPixel), color.White)
				}
			}
		}
	}
}

func fillRectangle(target *image.RGBA, area image.Rectangle, fill color.Color) {
	draw.Draw(target, area, image.NewUniform(fill), image.Point{}, draw.Src)
}

func encodedPNG(picture image.Image) ([]byte, error) {
	var encoded bytes.Buffer
	if errorValue := png.Encode(&encoded, picture); errorValue != nil {
		return nil, errorValue
	}
	return encoded.Bytes(), nil
}
