package adminapi

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const memoryFactsSamplePath = "testdata/memory_facts.json"

func TestMemoryFactsSampleIsAnAnswerTheHandlerCanGive(t *testing.T) {
	document, errorValue := os.ReadFile(filepath.FromSlash(memoryFactsSamplePath))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var sample memoryFactListResponse
	if errorValue := decoder.Decode(&sample); errorValue != nil {
		t.Fatalf("%s names a field the handler never answers with: %v", memoryFactsSamplePath, errorValue)
	}

	var keys map[string]any
	if errorValue := json.Unmarshal(document, &keys); errorValue != nil {
		t.Fatal(errorValue)
	}
	expectEveryField(t, reflect.TypeOf(memoryFactListResponse{}), []map[string]any{keys})
	expectEveryField(t, reflect.TypeOf(memoryIndexView{}), []map[string]any{objectAt(t, keys, "index")})
	expectEveryField(t, reflect.TypeOf(memoryFactView{}), objectsAt(t, keys, "facts"))
}

func expectEveryField(t *testing.T, structType reflect.Type, samples []map[string]any) {
	t.Helper()
	for field := range structType.Fields() {
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if !anyHolds(samples, name) {
			t.Errorf("%s never shows %s.%s; add it so the web schema meets it", memoryFactsSamplePath, structType.Name(), name)
		}
	}
}

func anyHolds(samples []map[string]any, name string) bool {
	for _, sample := range samples {
		if _, isHeld := sample[name]; isHeld {
			return true
		}
	}
	return false
}

func objectAt(t *testing.T, document map[string]any, key string) map[string]any {
	t.Helper()
	object, isObject := document[key].(map[string]any)
	if !isObject {
		t.Fatalf("%s has no %s object", memoryFactsSamplePath, key)
	}
	return object
}

func objectsAt(t *testing.T, document map[string]any, key string) []map[string]any {
	t.Helper()
	values, isArray := document[key].([]any)
	if !isArray || len(values) == 0 {
		t.Fatalf("%s has no %s", memoryFactsSamplePath, key)
	}
	objects := []map[string]any{}
	for _, value := range values {
		if object, isObject := value.(map[string]any); isObject {
			objects = append(objects, object)
		}
	}
	return objects
}
