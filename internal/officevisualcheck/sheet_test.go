package officevisualcheck

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

func TestAContactSheetLaysTheRendersOutInReadingOrderEachUnderItsNumber(t *testing.T) {
	fills := []color.RGBA{{200, 30, 30, 255}, {30, 200, 30, 255}, {30, 30, 200, 255}, {200, 200, 30, 255}, {30, 200, 200, 255}, {200, 30, 200, 255}}
	renders := [][]byte{}
	for _, fill := range fills {
		renders = append(renders, flatPNG(t, 1600, 900, fill))
	}
	sheet, errorValue := contactSheet(renders)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	picture, _, errorValue := image.Decode(bytes.NewReader(sheet.Data))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	columns, rows := 3, 2
	if sheetColumns(len(fills)) != columns {
		t.Fatalf("%d slides use %d columns", len(fills), sheetColumns(len(fills)))
	}
	tileWidth := (picture.Bounds().Dx() - (columns+1)*sheetGap) / columns
	tileHeight := tileWidth * 9 / 16
	if want := rows*tileHeight + (rows+1)*sheetGap; picture.Bounds().Dy() != want {
		t.Fatalf("sheet height %d, want %d", picture.Bounds().Dy(), want)
	}
	for index, fill := range fills {
		left := sheetGap + (index%columns)*(tileWidth+sheetGap)
		top := sheetGap + (index/columns)*(tileHeight+sheetGap)
		centre := color.RGBAModel.Convert(picture.At(left+tileWidth/2, top+tileHeight/2)).(color.RGBA)
		if absoluteDifference(int(centre.R), int(fill.R)) > 12 || absoluteDifference(int(centre.G), int(fill.G)) > 12 || absoluteDifference(int(centre.B), int(fill.B)) > 12 {
			t.Errorf("tile %d centre %v, want about %v", index+1, centre, fill)
		}
		corner := color.RGBAModel.Convert(picture.At(left+1, top+1)).(color.RGBA)
		if corner.R > 40 || corner.G > 40 || corner.B > 40 {
			t.Errorf("tile %d has no label plate in its corner: %v", index+1, corner)
		}
	}
}

func TestAContactSheetStaysWithinTheImageBudget(t *testing.T) {
	renders := [][]byte{}
	for range 12 {
		renders = append(renders, noisyPNG(t, 640, 360))
	}
	sheet, errorValue := contactSheet(renders)
	if errorValue != nil || len(sheet.Data) > maximumImageBytes {
		t.Fatalf("%d bytes, error %v", len(sheet.Data), errorValue)
	}
}

func TestAContactSheetOfNothingIsRefused(t *testing.T) {
	if _, errorValue := contactSheet(nil); errorValue == nil {
		t.Fatal("an empty sheet was built")
	}
}
