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

const (
	memoryFactsSamplePath  = "testdata/memory_facts.json"
	memoryRecallSamplePath = "testdata/memory_recall.json"
)

func TestMemoryFactsSampleIsAnAnswerTheHandlerCanGive(t *testing.T) {
	keys := decodeSample(t, memoryFactsSamplePath, &memoryFactListResponse{})
	expectEveryField(t, memoryFactsSamplePath, reflect.TypeOf(memoryFactListResponse{}), []map[string]any{keys})
	expectEveryField(t, memoryFactsSamplePath, reflect.TypeOf(memoryLayerView{}), objectsAt(t, keys, "layers"))
	expectEveryField(t, memoryFactsSamplePath, reflect.TypeOf(memoryIndexView{}), []map[string]any{objectAt(t, keys, "index")})
	expectEveryField(t, memoryFactsSamplePath, reflect.TypeOf(memoryFactView{}), objectsAt(t, keys, "facts"))
}

func TestMemoryRecallSampleIsAnAnswerTheHandlerCanGive(t *testing.T) {
	keys := decodeSample(t, memoryRecallSamplePath, &memoryRecallResponse{})
	expectEveryField(t, memoryRecallSamplePath, reflect.TypeOf(memoryRecallResponse{}), []map[string]any{keys})
	expectEveryField(t, memoryRecallSamplePath, reflect.TypeOf(memoryRecalledView{}), objectsAt(t, keys, "facts"))
}

func decodeSample(t *testing.T, path string, into any) map[string]any {
	t.Helper()
	document, errorValue := os.ReadFile(filepath.FromSlash(path))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if errorValue := decoder.Decode(into); errorValue != nil {
		t.Fatalf("%s names a field the handler never answers with: %v", path, errorValue)
	}
	var keys map[string]any
	if errorValue := json.Unmarshal(document, &keys); errorValue != nil {
		t.Fatal(errorValue)
	}
	return keys
}

func expectEveryField(t *testing.T, path string, structType reflect.Type, samples []map[string]any) {
	t.Helper()
	for field := range structType.Fields() {
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if !anyHolds(samples, name) {
			t.Errorf("%s never shows %s.%s; add it so the web schema meets it", path, structType.Name(), name)
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
		t.Fatalf("the sample has no %s object", key)
	}
	return object
}

func objectsAt(t *testing.T, document map[string]any, key string) []map[string]any {
	t.Helper()
	values, isArray := document[key].([]any)
	if !isArray || len(values) == 0 {
		t.Fatalf("the sample has no %s", key)
	}
	objects := []map[string]any{}
	for _, value := range values {
		if object, isObject := value.(map[string]any); isObject {
			objects = append(objects, object)
		}
	}
	return objects
}
