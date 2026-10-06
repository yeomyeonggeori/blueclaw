package agentruntime

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

//go:embed office_host_contract.json
var officeHostContractDocument []byte

type officeHostContract struct {
	RuntimeContextVariable string                    `json:"runtimeContextVariable"`
	DeckPreparation        officeDeckPreparationFile `json:"deckPreparation"`
	DraftClaims            officeDraftClaimsFiles    `json:"draftClaims"`
	DeckLayouts            officeDeckPreparationFile `json:"deckLayouts"`
	SourceSuffix           string                    `json:"sourceSuffix"`
	SourceContent          officeSourceContent       `json:"sourceContent"`
}

type officeDeckPreparationFile struct {
	RequestFile string `json:"requestFile"`
}

type officeDraftClaimsFiles struct {
	RequestFile  string `json:"requestFile"`
	ReportedFile string `json:"reportedFile"`
}

type officeSourceContent struct {
	Fields          []string `json:"fields"`
	CompanionFields []string `json:"companionFields"`
}

var officeContract = parsedOfficeHostContract(officeHostContractDocument)

func parsedOfficeHostContract(document []byte) officeHostContract {
	var contract officeHostContract
	if errorValue := json.Unmarshal(document, &contract); errorValue != nil {
		panic("office_host_contract.json does not parse: " + errorValue.Error())
	}
	return contract
}

const (
	officeRuntimeContextFileName    = "office-runtime-context.json"
	officeRuntimeContextReadLimit   = 1 << 20
	companyProfileFileName          = "company-profile.json"
	companyInfoGetToolName          = "company_info_get"
	companyDocumentRegisterToolName = "company_document_register"
	companyProfileDefaultLanguage   = "ko"
)

type officeRuntimeContext struct {
	Requester           officeRequester            `json:"requester"`
	Today               string                     `json:"today"`
	Company             map[string]string          `json:"company"`
	RegisteredDocuments []officeRegisteredDocument `json:"registeredDocuments"`
	Attachments         []officeAttachment         `json:"attachments"`
	ReviewsDeckRenders  bool                       `json:"reviewsDeckRenders"`
	PreparesDecks       bool                       `json:"preparesDecks"`
	DeckDesign          *officeDeckDesign          `json:"deckDesign"`
	Images              []officeImage              `json:"images"`
	Fonts               []officeFont               `json:"fonts"`
	JudgesDraftClaims   bool                       `json:"judgesDraftClaims"`
	DraftClaims         *officeDraftClaims         `json:"draftClaims"`
	ChoosesDeckLayouts  bool                       `json:"choosesDeckLayouts"`
	DeckLayouts         *officeDeckLayouts         `json:"deckLayouts"`
}

type officeFont struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type officeRequester struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type officeRegisteredDocument struct {
	DocumentNumber string `json:"documentNumber"`
}

type officeAttachment struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func officeRuntimeContextPath(taskTemporaryDirectoryPath string) string {
	if strings.TrimSpace(taskTemporaryDirectoryPath) == "" {
		return ""
	}
	return filepath.Join(taskTemporaryDirectoryPath, officeRuntimeContextFileName)
}

func (toolCatalogBuilder *ToolCatalogBuilder) writeOfficeRuntimeContext(ctx context.Context, actor security.WorkspaceActor, request ToolCatalogRequest, update func(*officeRuntimeContext)) error {
	taskRunID := toolcontract.TaskRunIDFromContext(ctx)
	contextPath := officeRuntimeContextPath(security.TaskTemporaryDirectoryPath(toolCatalogBuilder.requesterHomePath(request), taskRunID))
	if contextPath == "" {
		return nil
	}
	runtimeContext := toolCatalogBuilder.officeRuntimeContextFor(request, taskRunID)
	runtimeContext.keepRecordedFrom(ctx, actor, contextPath)
	update(&runtimeContext)
	if errorValue := actor.MkdirAll(ctx, filepath.Dir(contextPath)); errorValue != nil {
		return errorValue
	}
	return actor.WriteFile(ctx, contextPath, []byte(MarshalBody(runtimeContext)))
}

func (toolCatalogBuilder *ToolCatalogBuilder) officeRuntimeContextFor(request ToolCatalogRequest, taskRunID string) officeRuntimeContext {
	return officeRuntimeContext{
		Requester:           officeRequester{Name: strings.TrimSpace(request.RequesterName), Email: strings.TrimSpace(request.RequesterEmail)},
		Today:               toolCatalogBuilder.taskStartedAt(taskRunID).In(toolCatalogBuilder.companyLocation()).Format(time.DateOnly),
		Company:             map[string]string{},
		RegisteredDocuments: []officeRegisteredDocument{},
		Attachments:         toolCatalogBuilder.officeAttachments(request),
		ReviewsDeckRenders:  toolCatalogBuilder.reviewsDeckRenders(),
		PreparesDecks:       toolCatalogBuilder.preparesDecks(),
		Images:              []officeImage{},
		Fonts:               []officeFont{},
		JudgesDraftClaims:   toolCatalogBuilder.claimDecisionModel != nil,
		ChoosesDeckLayouts:  toolCatalogBuilder.choosesDeckLayouts(),
	}
}

func (runtimeContext *officeRuntimeContext) keepRecordedFrom(ctx context.Context, actor security.WorkspaceActor, contextPath string) {
	content, errorValue := actor.ReadFile(ctx, contextPath, officeRuntimeContextReadLimit)
	if errorValue != nil {
		return
	}
	var recorded officeRuntimeContext
	if json.Unmarshal(content, &recorded) != nil {
		return
	}
	for language, profilePath := range recorded.Company {
		runtimeContext.Company[language] = profilePath
	}
	runtimeContext.RegisteredDocuments = append(runtimeContext.RegisteredDocuments, recorded.RegisteredDocuments...)
	runtimeContext.DraftClaims = recorded.DraftClaims
	runtimeContext.DeckLayouts = recorded.DeckLayouts
	if recorded.DeckDesign != nil {
		runtimeContext.DeckDesign, runtimeContext.Images = recorded.DeckDesign, recorded.Images
		if recorded.Fonts != nil {
			runtimeContext.Fonts = recorded.Fonts
		}
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) officeAttachments(request ToolCatalogRequest) []officeAttachment {
	attachments := []officeAttachment{}
	for _, material := range visibleAttachmentMaterials(request.VisibleContext) {
		if !material.IsAvailable || strings.TrimSpace(material.Path) == "" {
			continue
		}
		nativePath := toolCatalogBuilder.nativeRequesterPath(request, material.Path)
		attachments = append(attachments, officeAttachment{Name: firstNonEmptyString(strings.TrimSpace(material.Filename), filepath.Base(nativePath)), Path: nativePath})
	}
	return attachments
}

func (toolCatalogBuilder *ToolCatalogBuilder) recordOfficeFacts(ctx context.Context, request ToolCatalogRequest, toolName string, input json.RawMessage, result mcp.ToolResult) {
	update := officeFactsUpdate(toolName, input, result)
	if update == nil {
		return
	}
	actor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(ctx, request)
	if actorFailure != nil {
		return
	}
	_ = toolCatalogBuilder.writeOfficeRuntimeContext(ctx, actor, request, update)
}

func officeFactsUpdate(toolName string, input json.RawMessage, result mcp.ToolResult) func(*officeRuntimeContext) {
	if result.IsError {
		return nil
	}
	switch strings.TrimSpace(toolName) {
	case companyInfoGetToolName:
		profilePath := keptFilePath(result.Content, companyProfileFileName)
		if profilePath == "" {
			return nil
		}
		language := companyProfileLanguage(input)
		return func(runtimeContext *officeRuntimeContext) { runtimeContext.Company[language] = profilePath }
	case companyDocumentRegisterToolName:
		documentNumber := registeredDocumentNumber(result.StructuredContent)
		if documentNumber == "" {
			return nil
		}
		return func(runtimeContext *officeRuntimeContext) {
			runtimeContext.RegisteredDocuments = append(runtimeContext.RegisteredDocuments, officeRegisteredDocument{DocumentNumber: documentNumber})
		}
	default:
		return nil
	}
}

func keptFilePath(content []json.RawMessage, name string) string {
	for _, item := range content {
		var link answeredFileLink
		if json.Unmarshal(item, &link) != nil || link.Type != "resource_link" || link.Name != name {
			continue
		}
		parsed, errorValue := url.Parse(link.URI)
		if errorValue != nil || parsed.Scheme != "file" {
			continue
		}
		return parsed.Path
	}
	return ""
}

func companyProfileLanguage(input json.RawMessage) string {
	var document struct {
		Language string `json:"language"`
	}
	_ = json.Unmarshal(input, &document)
	return firstNonEmptyString(strings.TrimSpace(document.Language), companyProfileDefaultLanguage)
}

func registeredDocumentNumber(structuredContent json.RawMessage) string {
	var document struct {
		DocumentNumber *string `json:"documentNumber"`
	}
	if json.Unmarshal(resultInsideTheEnvelope(structuredContent), &document) != nil || document.DocumentNumber == nil {
		return ""
	}
	return strings.TrimSpace(*document.DocumentNumber)
}
