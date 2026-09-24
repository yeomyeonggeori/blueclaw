package config

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

var environmentReferencePattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func expandEnvironmentReferences(document []byte) ([]byte, error) {
	var value any
	if errorValue := json.Unmarshal(document, &value); errorValue != nil {
		return nil, errorValue
	}
	missing := map[string]bool{}
	expanded := expandValue(value, missing)
	if len(missing) > 0 {
		return nil, fmt.Errorf("the runtime configuration refers to environment variables that are not set: %s", strings.Join(sortedNames(missing), ", "))
	}
	return json.Marshal(expanded)
}

func expandValue(value any, missing map[string]bool) any {
	switch typedValue := value.(type) {
	case string:
		return expandString(typedValue, missing)
	case []any:
		for index, element := range typedValue {
			typedValue[index] = expandValue(element, missing)
		}
		return typedValue
	case map[string]any:
		for key, element := range typedValue {
			typedValue[key] = expandValue(element, missing)
		}
		return typedValue
	default:
		return value
	}
}

func expandString(text string, missing map[string]bool) string {
	return environmentReferencePattern.ReplaceAllStringFunc(text, func(reference string) string {
		name := environmentReferencePattern.FindStringSubmatch(reference)[1]
		environmentValue, isSet := os.LookupEnv(name)
		if !isSet || environmentValue == "" {
			missing[name] = true
		}
		return environmentValue
	})
}

func sortedNames(names map[string]bool) []string {
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	return sorted
}
