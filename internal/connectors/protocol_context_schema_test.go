package connectors

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestVisibleContextReadsEveryFieldTheProtocolDeclares(t *testing.T) {
	documentBytes, errorValue := os.ReadFile("../../protocol/generated/json-schema/platform-inbound-event.schema.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var schema struct {
		Properties struct {
			Context struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"context"`
		} `json:"properties"`
	}
	if errorValue := json.Unmarshal(documentBytes, &schema); errorValue != nil {
		t.Fatal(errorValue)
	}
	read := jsonFieldNamesOf(reflect.TypeFor[VisibleContext]())
	for fieldName := range schema.Properties.Context.Properties {
		if !read[fieldName] {
			t.Errorf("the protocol declares context.%s and VisibleContext has no field that reads it", fieldName)
		}
	}
}

func jsonFieldNamesOf(structType reflect.Type) map[string]bool {
	names := map[string]bool{}
	for index := range structType.NumField() {
		name, _, _ := strings.Cut(structType.Field(index).Tag.Get("json"), ",")
		names[name] = true
	}
	return names
}
