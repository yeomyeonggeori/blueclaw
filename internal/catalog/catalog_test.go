package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

const sourceRoot = "../../internal"

func TestTheCatalogsMatchTheCodeTheyDescribe(t *testing.T) {
	taskEvents, errorValue := TaskEvents(sourceRoot)
	if errorValue != nil {
		t.Fatalf("reading task events failed: %v", errorValue)
	}
	tools, errorValue := Tools(sourceRoot)
	if errorValue != nil {
		t.Fatalf("reading tools failed: %v", errorValue)
	}
	if len(taskEvents) == 0 || len(tools) == 0 {
		t.Fatal("the extraction stopped finding what it describes, so a green catalog would mean nothing")
	}

	generated := map[string]string{
		"../../docs/event-catalog.md": RenderTaskEvents(taskEvents),
		"../../docs/tool-catalog.md":  RenderTools(tools),
	}
	for path, document := range generated {
		if os.Getenv("BLUECLAW_WRITE_CATALOGS") == "1" {
			if writeError := os.WriteFile(path, []byte(document), 0o644); writeError != nil {
				t.Fatalf("writing %s failed: %v", path, writeError)
			}
			continue
		}
		committed, readError := os.ReadFile(path)
		if readError != nil {
			t.Fatalf("%s is missing; regenerate with BLUECLAW_WRITE_CATALOGS=1 go test ./internal/catalog/", filepath.Base(path))
		}
		if string(committed) != document {
			t.Fatalf("%s no longer describes the code. A catalog nobody regenerates is worse than none, because a reader trusts it. Regenerate with BLUECLAW_WRITE_CATALOGS=1 go test ./internal/catalog/", filepath.Base(path))
		}
	}
}
