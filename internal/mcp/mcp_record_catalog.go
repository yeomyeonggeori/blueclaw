package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
)

// capabilityDescriptorMetaKey and capabilityRequesterMetaKey in
// protocol/src/capability.ts, where the server reads them from.
const (
	DescriptorMetaKey = "kim.intern/descriptor"
	RequesterMetaKey  = "kim.intern/requester"
)

const recordCatalogPath = "/v1/mcp"

type RecordCatalog struct {
	endpoint   string
	httpClient *http.Client

	mutex   sync.Mutex
	session *sdkmcp.ClientSession
}

func NewRecordCatalog(configuration capability.Configuration) *RecordCatalog {
	endpoint, httpClient := capability.Dial(configuration)
	return &RecordCatalog{endpoint: endpoint + recordCatalogPath, httpClient: httpClient}
}

func (catalog *RecordCatalog) DiscoverTools(ctx context.Context, requesterEmail string) ([]capability.ToolDescriptor, error) {
	session, errorValue := catalog.connected(ctx)
	if errorValue != nil {
		return nil, errorValue
	}
	listed, errorValue := session.ListTools(ctx, &sdkmcp.ListToolsParams{Meta: requesterMeta(requesterEmail)})
	if errorValue != nil {
		catalog.forget(session)
		return nil, errorValue
	}

	descriptors := make([]capability.ToolDescriptor, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		descriptor, isCarried := descriptorCarriedBy(tool)
		if !isCarried {
			continue
		}
		descriptors = append(descriptors, descriptor)
	}
	return descriptors, nil
}

func (catalog *RecordCatalog) CallTool(
	ctx context.Context,
	requesterEmail string,
	toolName string,
	input json.RawMessage,
) (ToolResult, error) {
	session, errorValue := catalog.connected(ctx)
	if errorValue != nil {
		return ToolResult{}, errorValue
	}
	arguments, errorValue := parseToolArguments(string(input))
	if errorValue != nil {
		return ToolResult{}, errorValue
	}
	called, errorValue := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Meta:      requesterMeta(requesterEmail),
		Name:      toolName,
		Arguments: arguments,
	})
	if errorValue != nil {
		catalog.forget(session)
		return ToolResult{}, errorValue
	}
	normalized, errorValue := normalizeToolResult(called)
	if errorValue != nil {
		return ToolResult{}, errorValue
	}
	return ParseToolResult(normalized)
}

func (catalog *RecordCatalog) Close() error {
	catalog.mutex.Lock()
	session := catalog.session
	catalog.session = nil
	catalog.mutex.Unlock()
	if session == nil {
		return nil
	}
	return session.Close()
}

func (catalog *RecordCatalog) connected(ctx context.Context) (*sdkmcp.ClientSession, error) {
	catalog.mutex.Lock()
	defer catalog.mutex.Unlock()
	if catalog.session != nil {
		return catalog.session, nil
	}
	if catalog.httpClient == nil {
		return nil, errors.New("the record catalog has no way to reach the capability service")
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "blueclaw", Version: "1"}, nil)
	session, errorValue := client.Connect(ctx, &sdkmcp.StreamableClientTransport{
		Endpoint:             catalog.endpoint,
		HTTPClient:           catalog.httpClient,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}, nil)
	if errorValue != nil {
		return nil, errorValue
	}
	catalog.session = session
	return session, nil
}

func (catalog *RecordCatalog) forget(session *sdkmcp.ClientSession) {
	catalog.mutex.Lock()
	if catalog.session == session {
		catalog.session = nil
	}
	catalog.mutex.Unlock()
	_ = session.Close()
}

func requesterMeta(requesterEmail string) sdkmcp.Meta {
	return sdkmcp.Meta{RequesterMetaKey: strings.ToLower(strings.TrimSpace(requesterEmail))}
}

func descriptorCarriedBy(tool *sdkmcp.Tool) (capability.ToolDescriptor, bool) {
	if tool == nil {
		return capability.ToolDescriptor{}, false
	}
	carried, isCarried := tool.Meta[DescriptorMetaKey]
	if !isCarried {
		return capability.ToolDescriptor{}, false
	}
	encoded, errorValue := json.Marshal(carried)
	if errorValue != nil {
		return capability.ToolDescriptor{}, false
	}
	var descriptor capability.ToolDescriptor
	if json.Unmarshal(encoded, &descriptor) != nil {
		return capability.ToolDescriptor{}, false
	}
	if strings.TrimSpace(descriptor.Name) == "" || strings.TrimSpace(descriptor.Name) != strings.TrimSpace(tool.Name) {
		return capability.ToolDescriptor{}, false
	}
	return descriptor, true
}
