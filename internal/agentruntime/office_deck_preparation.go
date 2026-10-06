package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const (
	officeDeckRequestReadLimit      = 1 << 16
	officeDeckDesignReadLimit       = 1 << 20
	officeDeckImageReadLimit        = 25 << 20
	officeDeckImageLimit            = 24
	officeDeckImagesDirectoryName   = "deck-images"
	officeDeckDownloadTimeout       = 30 * time.Second
	officeDeckDecisionTimeout       = 90 * time.Second
	companyDocumentListToolName     = "company_document_list"
	companyDocumentDownloadToolName = "company_document_download"
	officeImageSourceDataRoom       = "dataroom"
)

var officeDeckImageExtensions = map[string]bool{".jpg": true, ".jpeg": true, ".png": true}

var officeAttachedImageExtensions = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}

type officeDeckDesign struct {
	Choices map[string]officeDesignChoice `json:"choices"`
	Model   string                        `json:"model,omitempty"`
	CostUSD float64                       `json:"costUSD"`
	Failure string                        `json:"failure,omitempty"`
}

type officeDesignChoice struct {
	Option        string             `json:"option"`
	Probabilities map[string]float64 `json:"probabilities"`
}

type officeImage struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Source  string `json:"source"`
	Folder  string `json:"folder,omitempty"`
	Title   string `json:"title,omitempty"`
	Summary string `json:"summary,omitempty"`
	Date    string `json:"date,omitempty"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
}

type officeDeckPreparationRequest struct {
	Design string `json:"design"`
}

type officeDesignDefinition struct {
	Instructions string                              `json:"instructions"`
	Questions    map[string]officeDesignQuestionText `json:"questions"`
}

type officeDesignQuestionText struct {
	Instructions string            `json:"instructions"`
	Options      map[string]string `json:"options"`
}

type dataRoomDocument struct {
	DocumentID   string  `json:"documentID"`
	Title        string  `json:"title"`
	Summary      *string `json:"summary"`
	Date         *string `json:"date"`
	FilePath     *string `json:"filePath"`
	Domain       *string `json:"domain"`
	CategoryCode *string `json:"categoryCode"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseDeckDesignModel(decisionModel model.DecisionModel) {
	toolCatalogBuilder.deckDesignModel = decisionModel
}

func (toolCatalogBuilder *ToolCatalogBuilder) preparesDecks() bool {
	return toolCatalogBuilder.deckDesignModel != nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) prepareRequestedDeck(ctx context.Context, actor security.WorkspaceActor, request ToolCatalogRequest) {
	if !toolCatalogBuilder.preparesDecks() {
		return
	}
	taskDirectoryPath := security.TaskTemporaryDirectoryPath(toolCatalogBuilder.requesterHomePath(request), toolcontract.TaskRunIDFromContext(ctx))
	contextPath := officeRuntimeContextPath(taskDirectoryPath)
	if contextPath == "" || toolCatalogBuilder.isDeckPrepared(ctx, actor, contextPath) {
		return
	}
	definition, isRequested := readDeckPreparationRequest(ctx, actor, filepath.Join(taskDirectoryPath, officeContract.DeckPreparation.RequestFile))
	if !isRequested {
		return
	}
	toolCatalogBuilder.callRecordTool(ctx, request, companyInfoGetToolName, json.RawMessage(`{}`))
	images := toolCatalogBuilder.gatherDataRoomImages(ctx, actor, request, filepath.Join(taskDirectoryPath, officeDeckImagesDirectoryName))
	design := toolCatalogBuilder.decideDeckDesign(ctx, request, definition, images)
	if errorValue := toolCatalogBuilder.writeOfficeRuntimeContext(ctx, actor, request, func(runtimeContext *officeRuntimeContext) {
		runtimeContext.DeckDesign, runtimeContext.Images = &design, images
	}); errorValue != nil {
		slog.Warn("deck preparation was not recorded", "error", errorValue)
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) isDeckPrepared(ctx context.Context, actor security.WorkspaceActor, contextPath string) bool {
	content, errorValue := actor.ReadFile(ctx, contextPath, officeRuntimeContextReadLimit)
	if errorValue != nil {
		return false
	}
	var recorded officeRuntimeContext
	return json.Unmarshal(content, &recorded) == nil && recorded.DeckDesign != nil
}

func readDeckPreparationRequest(ctx context.Context, actor security.WorkspaceActor, requestPath string) (officeDesignDefinition, bool) {
	content, errorValue := actor.ReadFile(ctx, requestPath, officeDeckRequestReadLimit)
	if errorValue != nil {
		return officeDesignDefinition{}, false
	}
	var preparationRequest officeDeckPreparationRequest
	if json.Unmarshal(content, &preparationRequest) != nil || strings.TrimSpace(preparationRequest.Design) == "" {
		return officeDesignDefinition{}, false
	}
	definitionContent, errorValue := actor.ReadFile(ctx, preparationRequest.Design, officeDeckDesignReadLimit)
	if errorValue != nil {
		return officeDesignDefinition{}, false
	}
	var definition officeDesignDefinition
	if json.Unmarshal(definitionContent, &definition) != nil || len(definition.Questions) == 0 {
		return officeDesignDefinition{}, false
	}
	return definition, true
}

func (toolCatalogBuilder *ToolCatalogBuilder) decideDeckDesign(ctx context.Context, request ToolCatalogRequest, definition officeDesignDefinition, images []officeImage) officeDeckDesign {
	decisionContext, cancel := context.WithTimeout(ctx, officeDeckDecisionTimeout)
	defer cancel()
	response, errorValue := toolCatalogBuilder.deckDesignModel.Decide(decisionContext, model.DecisionRequest{
		State:     map[string]any{"request": requestWordings(request), "images": append(imagesForDecision(images), toolCatalogBuilder.attachedImagesForDecision(request)...)},
		Questions: designQuestions(definition),
	})
	if errorValue != nil {
		slog.Warn("deck design was not decided", "error", errorValue)
		return officeDeckDesign{Choices: map[string]officeDesignChoice{}, Failure: errorValue.Error()}
	}
	design := officeDeckDesign{Choices: map[string]officeDesignChoice{}, Model: response.ModelName, CostUSD: response.Usage.CostUSD}
	for name, answer := range response.Answers {
		design.Choices[name] = officeDesignChoice{Option: answer.Choice, Probabilities: answer.Probabilities}
	}
	return design
}

func designQuestions(definition officeDesignDefinition) map[string]model.DecisionQuestion {
	questions := map[string]model.DecisionQuestion{}
	for name, question := range definition.Questions {
		questions[name] = model.ChoiceQuestion{
			Instructions:       strings.TrimSpace(definition.Instructions + " " + question.Instructions),
			OptionDescriptions: question.Options,
		}.Question()
	}
	return questions
}

func imagesForDecision(images []officeImage) []map[string]any {
	described := []map[string]any{}
	for _, image := range images {
		described = append(described, map[string]any{"name": image.Name, "title": image.Title, "summary": image.Summary, "folder": image.Folder, "width": image.Width, "height": image.Height})
	}
	return described
}

func (toolCatalogBuilder *ToolCatalogBuilder) attachedImagesForDecision(request ToolCatalogRequest) []map[string]any {
	described := []map[string]any{}
	for _, attachment := range toolCatalogBuilder.officeAttachments(request) {
		if officeAttachedImageExtensions[strings.ToLower(filepath.Ext(attachment.Name))] {
			described = append(described, map[string]any{"name": attachment.Name, "source": "attachment"})
		}
	}
	return described
}

func (toolCatalogBuilder *ToolCatalogBuilder) gatherDataRoomImages(ctx context.Context, actor security.WorkspaceActor, request ToolCatalogRequest, directoryPath string) []officeImage {
	images := []officeImage{}
	result, isAnswered := toolCatalogBuilder.callRecordTool(ctx, request, companyDocumentListToolName, json.RawMessage(`{}`))
	if !isAnswered {
		return images
	}
	documents := imageDocuments(result)
	if len(documents) == 0 || actor.MkdirAll(ctx, directoryPath) != nil {
		return images
	}
	for _, document := range documents {
		image, errorValue := toolCatalogBuilder.keepDataRoomImage(ctx, actor, request, directoryPath, document)
		if errorValue != nil {
			slog.Info("a data room image was not kept for the deck", "document", document.DocumentID, "error", errorValue)
			continue
		}
		images = append(images, image)
	}
	return images
}

func imageDocuments(result mcp.ToolResult) []dataRoomDocument {
	var listing struct {
		Documents []dataRoomDocument `json:"documents"`
	}
	if json.Unmarshal(resultInsideTheEnvelope(result.StructuredContent), &listing) != nil {
		return nil
	}
	documents := []dataRoomDocument{}
	for _, document := range listing.Documents {
		if officeDeckImageExtensions[strings.ToLower(filepath.Ext(textOf(document.FilePath)))] {
			documents = append(documents, document)
		}
	}
	sort.SliceStable(documents, func(first, second int) bool { return textOf(documents[first].Date) > textOf(documents[second].Date) })
	if len(documents) > officeDeckImageLimit {
		documents = documents[:officeDeckImageLimit]
	}
	return documents
}

func (toolCatalogBuilder *ToolCatalogBuilder) keepDataRoomImage(ctx context.Context, actor security.WorkspaceActor, request ToolCatalogRequest, directoryPath string, document dataRoomDocument) (officeImage, error) {
	input, _ := json.Marshal(map[string]string{"documentHint": document.DocumentID})
	result, isAnswered := toolCatalogBuilder.callRecordTool(ctx, request, companyDocumentDownloadToolName, input)
	if !isAnswered {
		return officeImage{}, errors.New("the download was refused")
	}
	var download struct {
		DownloadURL string `json:"downloadURL"`
	}
	if json.Unmarshal(resultInsideTheEnvelope(result.StructuredContent), &download) != nil || download.DownloadURL == "" {
		return officeImage{}, errors.New("the download answered no address")
	}
	content, errorValue := downloadImage(ctx, download.DownloadURL)
	if errorValue != nil {
		return officeImage{}, errorValue
	}
	configuration, _, errorValue := image.DecodeConfig(bytes.NewReader(content))
	if errorValue != nil {
		return officeImage{}, fmt.Errorf("not a readable image: %w", errorValue)
	}
	name := filepath.Base(textOf(document.FilePath))
	path := filepath.Join(directoryPath, name)
	if errorValue := actor.WriteFile(ctx, path, content); errorValue != nil {
		return officeImage{}, errorValue
	}
	return officeImage{
		Path: path, Name: name, Source: officeImageSourceDataRoom,
		Folder: firstNonEmptyString(textOf(document.Domain), textOf(document.CategoryCode)),
		Title:  strings.TrimSpace(document.Title), Summary: textOf(document.Summary), Date: textOf(document.Date),
		Width: configuration.Width, Height: configuration.Height,
	}, nil
}

func downloadImage(ctx context.Context, address string) ([]byte, error) {
	downloadContext, cancel := context.WithTimeout(ctx, officeDeckDownloadTimeout)
	defer cancel()
	httpRequest, errorValue := http.NewRequestWithContext(downloadContext, http.MethodGet, address, nil)
	if errorValue != nil {
		return nil, errorValue
	}
	response, errorValue := http.DefaultClient.Do(httpRequest)
	if errorValue != nil {
		return nil, errorValue
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the file store answered %d", response.StatusCode)
	}
	content, errorValue := io.ReadAll(io.LimitReader(response.Body, officeDeckImageReadLimit+1))
	if errorValue != nil {
		return nil, errorValue
	}
	if len(content) > officeDeckImageReadLimit {
		return nil, fmt.Errorf("the image is larger than %d bytes", officeDeckImageReadLimit)
	}
	return content, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) callRecordTool(ctx context.Context, request ToolCatalogRequest, toolName string, input json.RawMessage) (mcp.ToolResult, bool) {
	result, errorValue := toolCatalogBuilder.callRecordToolAsRequester(ctx, request, toolName, input)
	return result, errorValue == nil && !result.IsError
}

func textOf(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
