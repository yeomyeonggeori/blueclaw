package capabilitycatalog

import (
	"embed"
	"encoding/json"
	"slices"
)

//go:embed manifest.json
var protocolManifest []byte

//go:embed json-schema
var protocolSchemaFiles embed.FS

// ProtocolIdentity is the contract version this build was generated from. It is
// what Blueclaw expects every process on the protocol to report.
type ProtocolIdentity struct {
	ProtocolVersion       string `json:"protocolVersion"`
	AggregateProtocolHash string `json:"aggregateHash"`
}

func BuiltProtocolIdentity() ProtocolIdentity {
	identity := ProtocolIdentity{}
	_ = json.Unmarshal(protocolManifest, &identity)
	return identity
}

var connectorPlatformNames = mustReadPlatformNames("connector-platform")

var messengerPlatformNames = mustReadPlatformNames("messenger-platform")

func ConnectorPlatformNames() []string {
	return slices.Clone(connectorPlatformNames)
}

func MessengerPlatformNames() []string {
	return slices.Clone(messengerPlatformNames)
}

func IsConnectorPlatform(name string) bool {
	return slices.Contains(connectorPlatformNames, name)
}

func IsMessengerPlatform(name string) bool {
	return slices.Contains(messengerPlatformNames, name)
}

func mustReadPlatformNames(schemaName string) []string {
	document, errorValue := protocolSchemaFiles.ReadFile("json-schema/" + schemaName + ".schema.json")
	if errorValue != nil {
		panic(errorValue)
	}
	schema := struct {
		Enum []string `json:"enum"`
	}{}
	if errorValue := json.Unmarshal(document, &schema); errorValue != nil {
		panic(errorValue)
	}
	if len(schema.Enum) == 0 {
		panic("the generated " + schemaName + " schema names no platform")
	}
	return schema.Enum
}
