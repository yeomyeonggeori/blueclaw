//go:build appliance && llmeval

package e2e

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
)

const (
	companyInfoToolName         = "company_info_get"
	companyDocumentListToolName = "company_document_list"
	companyDocumentDownloadName = "company_document_download"
	companyProfileFileName      = "company-profile.json"
	dataRoomDocumentsFileName   = "documents.json"
)

var companyRecordToolNames = []string{companyInfoToolName, companyDocumentListToolName, companyDocumentDownloadName}

var companyProfileImageFields = []string{"logoImage", "sealImage"}

type dataRoomDocument struct {
	DocumentID   string   `json:"documentID"`
	Title        string   `json:"title"`
	Summary      string   `json:"summary"`
	CategoryCode string   `json:"categoryCode"`
	DocumentType string   `json:"documentType"`
	Date         string   `json:"date"`
	FileName     string   `json:"fileName"`
	Tags         []string `json:"tags"`
}

type companyRecord struct {
	profileDirectory string
	profiles         map[string]map[string]any
	dataRoomPath     string
	documents        []dataRoomDocument
	fileServerURL    string
}

func capabilityCatalogEntry(t *testing.T, toolName string) map[string]any {
	t.Helper()
	document, errorValue := os.ReadFile(scenarioCapabilityCatalogPath())
	if errorValue != nil {
		t.Fatalf("%s must name the capability catalog: %v", ScenarioCapabilityCatalogVariable, errorValue)
	}
	var catalog struct {
		Tools []map[string]any `json:"tools"`
	}
	if errorValue := json.Unmarshal(document, &catalog); errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, entry := range catalog.Tools {
		if entry["name"] == toolName {
			return entry
		}
	}
	t.Fatalf("the capability catalog carries no %s", toolName)
	return nil
}

func readJSONFile(t *testing.T, path string, target any) {
	t.Helper()
	content, errorValue := os.ReadFile(path)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := json.Unmarshal(content, target); errorValue != nil {
		t.Fatalf("%s cannot be read: %v", path, errorValue)
	}
}

func readCompanyRecord(t *testing.T, profilePath string, dataRoomPath string) *companyRecord {
	t.Helper()
	record := &companyRecord{profileDirectory: filepath.Dir(profilePath), profiles: map[string]map[string]any{}, documents: []dataRoomDocument{}}
	readJSONFile(t, profilePath, &record.profiles)
	if strings.TrimSpace(dataRoomPath) != "" {
		record.dataRoomPath = dataRoomPath
		readJSONFile(t, filepath.Join(dataRoomPath, dataRoomDocumentsFileName), &record.documents)
	}
	return record
}

func argumentsOf(arguments any, target any) {
	encoded, _ := json.Marshal(arguments)
	_ = json.Unmarshal(encoded, target)
}

func (record *companyRecord) profileAsked(arguments any) map[string]any {
	var asked struct {
		Language string `json:"language"`
	}
	argumentsOf(arguments, &asked)
	if profile, isKnown := record.profiles[strings.ToLower(strings.TrimSpace(asked.Language))]; isKnown {
		return profile
	}
	return record.profiles["ko"]
}

func toolAnswer(body any, isError bool, files ...sdkmcp.Content) (*sdkmcp.CallToolResult, error) {
	encodedBody, errorValue := json.Marshal(body)
	if errorValue != nil {
		return nil, errorValue
	}
	content := append([]sdkmcp.Content{&sdkmcp.TextContent{Text: string(encodedBody)}}, files...)
	return &sdkmcp.CallToolResult{Content: content, StructuredContent: json.RawMessage(encodedBody), IsError: isError}, nil
}

func answeredFile(name string, content []byte) sdkmcp.Content {
	contentType := mime.TypeByExtension(filepath.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return &sdkmcp.EmbeddedResource{Resource: &sdkmcp.ResourceContents{URI: "internkim://files/" + name, MIMEType: contentType, Blob: content}}
}

func (record *companyRecord) profileFiles(profile map[string]any) ([]sdkmcp.Content, error) {
	printed := map[string]any{}
	for key, value := range profile {
		printed[key] = value
	}
	files := []sdkmcp.Content{}
	for _, field := range companyProfileImageFields {
		name, _ := profile[field].(string)
		printed[field] = name
		if name == "" {
			continue
		}
		content, errorValue := os.ReadFile(filepath.Join(record.profileDirectory, name))
		if errorValue != nil {
			return nil, errorValue
		}
		files = append(files, answeredFile(name, content))
	}
	encodedProfile, errorValue := json.MarshalIndent(printed, "", "  ")
	if errorValue != nil {
		return nil, errorValue
	}
	profileFile := &sdkmcp.EmbeddedResource{Resource: &sdkmcp.ResourceContents{URI: "internkim://files/" + companyProfileFileName, MIMEType: "application/json", Text: string(encodedProfile)}}
	return append([]sdkmcp.Content{profileFile}, files...), nil
}

func profileView(profile map[string]any) map[string]any {
	view := map[string]any{}
	for key, value := range profile {
		view[key] = value
	}
	for _, field := range companyProfileImageFields {
		delete(view, field)
	}
	return view
}

func (record *companyRecord) answerCompanyInfo(request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
	profile := record.profileAsked(request.Params.Arguments)
	body := map[string]any{"result": profileView(profile)}
	if request.Params.Meta[mcp.AnsweredFilesMetaKey] != "kept" {
		return toolAnswer(body, false)
	}
	files, errorValue := record.profileFiles(profile)
	if errorValue != nil {
		return nil, errorValue
	}
	return toolAnswer(body, false, files...)
}

func (record *companyRecord) storagePath(document dataRoomDocument) string {
	extension := filepath.Ext(document.FileName)
	return document.CategoryCode + "/" + strings.TrimSuffix(document.FileName, extension) + "." + document.DocumentID + extension
}

func (record *companyRecord) listed(document dataRoomDocument) map[string]any {
	return map[string]any{
		"documentID": document.DocumentID, "documentNumber": nil, "kind": "received", "documentType": document.DocumentType,
		"title": document.Title, "counterpart": nil, "language": nil, "filePath": record.storagePath(document),
		"summary": document.Summary, "requesterID": nil, "issuedAt": document.Date + "T00:00:00Z", "categoryCode": document.CategoryCode,
		"date": document.Date, "period": nil, "status": nil, "supersedes": nil, "sha256": nil, "tags": document.Tags,
		"storagePath": record.storagePath(document), "published": nil,
	}
}

func (record *companyRecord) answerDocumentList(request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
	var asked struct {
		CategoryCode string `json:"categoryCode"`
		Query        string `json:"query"`
	}
	argumentsOf(request.Params.Arguments, &asked)
	documents := []map[string]any{}
	for _, document := range record.documents {
		if !strings.HasPrefix(document.CategoryCode, asked.CategoryCode) {
			continue
		}
		if asked.Query != "" && !strings.Contains(strings.ToLower(document.Title+" "+document.Summary), strings.ToLower(asked.Query)) {
			continue
		}
		documents = append(documents, record.listed(document))
	}
	return toolAnswer(map[string]any{"result": map[string]any{"count": len(documents), "documents": documents}}, false)
}

func (record *companyRecord) answerDocumentDownload(request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
	var asked struct {
		DocumentHint string `json:"documentHint"`
	}
	argumentsOf(request.Params.Arguments, &asked)
	for _, document := range record.documents {
		if document.DocumentID == asked.DocumentHint || document.Title == asked.DocumentHint {
			result := map[string]any{"storagePath": record.storagePath(document), "downloadURL": record.fileServerURL + "/" + document.DocumentID}
			return toolAnswer(map[string]any{"result": result}, false)
		}
	}
	return toolAnswer(map[string]any{"error": "no document in the data room answers to " + asked.DocumentHint}, true)
}

func (record *companyRecord) serveFile(responseWriter http.ResponseWriter, request *http.Request) {
	documentID := strings.TrimPrefix(request.URL.Path, "/")
	for _, document := range record.documents {
		if document.DocumentID == documentID {
			http.ServeFile(responseWriter, request, filepath.Join(record.dataRoomPath, document.FileName))
			return
		}
	}
	http.NotFound(responseWriter, request)
}

func addCatalogTool(t *testing.T, server *sdkmcp.Server, toolName string, answer func(*sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error)) {
	t.Helper()
	descriptor := capabilityCatalogEntry(t, toolName)
	description, _ := descriptor["description"].(string)
	server.AddTool(&sdkmcp.Tool{
		Name:        toolName,
		Description: description,
		InputSchema: descriptor["inputSchema"],
		Meta:        sdkmcp.Meta{mcp.DescriptorMetaKey: descriptor},
	}, func(_ context.Context, request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return answer(request)
	})
}

func startCompanyRecordCatalog(t *testing.T, profilePath string, dataRoomPath string) string {
	t.Helper()
	record := readCompanyRecord(t, profilePath, dataRoomPath)
	fileServer := httptest.NewServer(http.HandlerFunc(record.serveFile))
	t.Cleanup(fileServer.Close)
	record.fileServerURL = fileServer.URL
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "internkim", Version: "1"}, nil)
	addCatalogTool(t, server, companyInfoToolName, record.answerCompanyInfo)
	addCatalogTool(t, server, companyDocumentListToolName, record.answerDocumentList)
	addCatalogTool(t, server, companyDocumentDownloadName, record.answerDocumentDownload)
	catalogServer := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *sdkmcp.Server { return server },
		&sdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	))
	t.Cleanup(catalogServer.Close)
	return catalogServer.URL
}
