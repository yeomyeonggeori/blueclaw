package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
)

func TestARetiredCircleSomebodyHoldsAgainIsLeftAlone(t *testing.T) {
	stores, root := memorytest.OpenWithRoot(t)
	protected := filepath.Join(root, "circles", "hr", ".protected")
	if errorValue := os.MkdirAll(protected, 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	application := &Application{
		memoryStores: stores,
		heldCircles:  func() map[string]bool { return map[string]bool{"hr": true} },
	}

	if errorValue := application.removeRetiredCircleFolders(); errorValue != nil {
		t.Fatal(errorValue)
	}

	if _, errorValue := os.Stat(protected); errorValue != nil {
		t.Fatalf("circles/hr is held again and must stay: %v", errorValue)
	}
}
