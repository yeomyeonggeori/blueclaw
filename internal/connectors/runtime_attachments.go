package connectors

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

// Only the triggering message's attachments are imported eagerly: what the
// person just handed over should be visible without a tool call. Everything
// older stays where it is and is read on demand by the url standing in its
// message — importing the whole visible window bought nothing but latency and
// still missed every message that had scrolled past it.
func (connectorRuntime *ConnectorRuntime) withAttachmentMaterials(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent, personID string) PlatformInboundEvent {
	attachments := connectorUniqueInputAttachments(event.Context.InputAttachments)
	if len(attachments) == 0 {
		return event
	}
	importingAdapter, isSupported := adapter.(InputAttachmentImportingAdapter)
	if !isSupported {
		event.Context.Materials = attachments
		return event
	}
	scope := connectorInputAttachmentScope(personID, event)
	targetDirectoryPath := connectorInputAttachmentDirectory(scope, event)
	result, errorValue := importingAdapter.ImportInputAttachments(ctx, InputAttachmentImportRequest{
		MessageID:           event.MessageID,
		TargetDirectoryPath: targetDirectoryPath,
		InputAttachments:    attachments,
	})
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".attachments.import_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		refused := connectorRefusedInputAttachments(attachments, errorValue)
		event.Context.InputAttachments = refused
		event.Context.Materials = refused
		event.Context.Messages = connectorReplaceImportedMessageAttachments(event.Context.Messages, refused)
		return event
	}
	if len(result.InputAttachments) > 0 {
		writtenAttachments, writtenContents := connectorRuntime.attachmentWriterFor(personID).writeAll(ctx, result.InputAttachments)
		result.InputParts = connectorInputPartsAtWrittenPaths(result.InputParts, writtenAttachments)
		result.InputParts = connectorImagePartsShowingTheirBytes(result.InputParts, writtenContents)
		connectorRuntime.warnAboutImagesMissingBytes(adapter, event.MessageID, result.InputParts)
		importedAttachments := connectorReadableInputAttachments(writtenAttachments, personID, scope)
		event.Context.InputAttachments = connectorReplaceImportedInputAttachments(event.Context.InputAttachments, importedAttachments)
		event.Context.Materials = connectorReplaceImportedInputAttachments(event.Context.Materials, importedAttachments)
		event.Context.Messages = connectorReplaceImportedMessageAttachments(event.Context.Messages, importedAttachments)
	}
	if len(result.InputParts) > 0 {
		readableInputParts := connectorReadableAgentParts(result.InputParts, personID, scope)
		event.InputParts = append(event.InputParts, connectorCurrentInputParts(readableInputParts, event)...)
	}
	return event
}

const connectorAttachmentImportRefusedCode = "attachment_import_failed"

// An attachment that could not be brought in is still handed to the agent, and
// without this it arrives looking ordinary: a file with a url the agent cannot
// open. It would then ask whoever sent it to attach the file again, which is
// the one thing they already did. It arrives refused, with the reason, so the
// agent says what went wrong and the ledger holds it.
func connectorRefusedInputAttachments(attachments []InputAttachment, errorValue error) []InputAttachment {
	refused := make([]InputAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		refused = append(refused, refusedInputAttachment(attachment, errorValue))
	}
	return refused
}

func (connectorRuntime *ConnectorRuntime) attachmentWriterFor(personID string) importedAttachmentWriter {
	return importedAttachmentWriter{workspaceActorFactory: connectorRuntime.workspaceActorFactory, personID: personID}
}

// An image part that reaches the model without bytes degrades to its filename
// text with nothing marking the loss. This names the attachment that could not
// be shown instead of letting it disappear silently.
func (connectorRuntime *ConnectorRuntime) warnAboutImagesMissingBytes(adapter PlatformAdapter, messageID string, parts []agentcontract.AgentPart) {
	for _, part := range parts {
		if part.Type != agentcontract.AgentPartTypeImage || part.Image == nil {
			continue
		}
		if strings.TrimSpace(part.Image.DataBase64) != "" {
			continue
		}
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".attachments.image_bytes_missing",
			slog.String("messageID", messageID),
			slog.String("fileID", part.Source.FileID),
			slog.String("filename", part.Image.Filename))
	}
}

const mostInlineImageBytesShown = 8 << 20

// An image the message came with is put in front of the model with the message,
// whatever messenger it came over. This layer is the one owner of that: an
// importing adapter hands back bytes, the writer puts them in the person's
// workspace, and the picture travels with the prompt instead of costing a tool
// call to look at what was just sent.
func connectorImagePartsShowingTheirBytes(parts []agentcontract.AgentPart, contents writtenAttachmentContents) []agentcontract.AgentPart {
	result := make([]agentcontract.AgentPart, 0, len(parts))
	for _, part := range parts {
		if part.Image != nil && strings.TrimSpace(part.Image.DataBase64) == "" {
			if content, isHeld := contents[strings.TrimSpace(part.Image.Path)]; isHeld && len(content) <= mostInlineImageBytesShown {
				image := *part.Image
				image.DataBase64 = base64.StdEncoding.EncodeToString(content)
				part.Image = &image
			}
		}
		result = append(result, part)
	}
	return result
}

// A part is matched to the attachment that wrote it by file id, not filename:
// a collision with an existing file under a different name renames the file,
// and the part still carries the name it arrived under.
func connectorInputPartsAtWrittenPaths(parts []agentcontract.AgentPart, writtenAttachments []InputAttachment) []agentcontract.AgentPart {
	pathByFileID := map[string]string{}
	for _, attachment := range writtenAttachments {
		if strings.TrimSpace(attachment.Path) == "" || strings.TrimSpace(attachment.FileID) == "" {
			continue
		}
		pathByFileID[attachment.FileID] = attachment.Path
	}
	result := make([]agentcontract.AgentPart, 0, len(parts))
	for _, part := range parts {
		if part.File != nil {
			file := *part.File
			file.Path = firstNonEmptyString(pathByFileID[part.Source.FileID], file.Path)
			part.File = &file
		}
		if part.Image != nil {
			image := *part.Image
			image.Path = firstNonEmptyString(pathByFileID[part.Source.FileID], image.Path)
			part.Image = &image
		}
		result = append(result, part)
	}
	return result
}

func connectorReplaceImportedMessageAttachments(messages []VisibleContextMessage, importedAttachments []InputAttachment) []VisibleContextMessage {
	result := make([]VisibleContextMessage, 0, len(messages))
	for _, message := range messages {
		message.InputAttachments = connectorReplaceImportedInputAttachments(message.InputAttachments, importedAttachments)
		result = append(result, message)
	}
	return result
}

func connectorReplaceImportedInputAttachments(attachments []InputAttachment, importedAttachments []InputAttachment) []InputAttachment {
	importedByKey := connectorImportedAttachmentByKey(importedAttachments)
	replacedAttachments := make([]InputAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		key := connectorInputAttachmentKey(attachment)
		if importedAttachment, isFound := importedByKey[key]; isFound {
			replacedAttachments = append(replacedAttachments, importedAttachment)
			continue
		}
		replacedAttachments = append(replacedAttachments, attachment)
	}
	return connectorUniqueInputAttachments(replacedAttachments)
}

func connectorImportedAttachmentByKey(importedAttachments []InputAttachment) map[string]InputAttachment {
	importedByKey := map[string]InputAttachment{}
	for _, attachment := range importedAttachments {
		key := connectorInputAttachmentKey(attachment)
		if key != "" {
			importedByKey[key] = attachment
		}
	}
	return importedByKey
}

func connectorCurrentInputParts(parts []agentcontract.AgentPart, event PlatformInboundEvent) []agentcontract.AgentPart {
	currentFileIDs := map[string]bool{}
	for _, attachment := range event.Context.InputAttachments {
		fileID := strings.TrimSpace(attachment.FileID)
		if fileID != "" {
			currentFileIDs[fileID] = true
		}
	}
	currentParts := []agentcontract.AgentPart{}
	for _, part := range parts {
		if connectorIsCurrentInputPart(part, event, currentFileIDs) {
			currentParts = append(currentParts, part)
		}
	}
	return currentParts
}

func connectorIsCurrentInputPart(part agentcontract.AgentPart, event PlatformInboundEvent, currentFileIDs map[string]bool) bool {
	if strings.TrimSpace(part.Source.MessageID) != "" && strings.TrimSpace(part.Source.MessageID) == strings.TrimSpace(event.MessageID) {
		return true
	}
	if strings.TrimSpace(part.Source.FileID) != "" && currentFileIDs[strings.TrimSpace(part.Source.FileID)] {
		return true
	}
	return false
}

func connectorInputAttachmentScope(personID string, event PlatformInboundEvent) agentruntime.ConversationResourceScope {
	return agentruntime.ConversationScopeForRequest(connectorWorkspaceRootPath, agentruntime.ToolCatalogRequest{
		RequesterPersonID:       personID,
		ConversationID:          event.ConversationID,
		ConversationType:        event.Context.ConversationType,
		ConversationChannelID:   event.Context.ChannelID,
		ConversationChannelName: event.Context.ChannelName,
	})
}

func connectorInputAttachmentDirectory(scope agentruntime.ConversationResourceScope, event PlatformInboundEvent) string {
	platform := connectorSafePathSegment(firstNonEmptyString(event.Platform, "platform"))
	conversationLabel := connectorAttachmentConversationLabel(event)
	return strings.TrimRight(scope.DefaultDirectoryPath, "/") + "/inbox/" + platform + "/" + conversationLabel
}

func connectorAttachmentConversationLabel(event PlatformInboundEvent) string {
	if channelName := strings.TrimSpace(event.Context.ChannelName); channelName != "" {
		return connectorSafePathSegment(channelName)
	}
	conversationID := strings.TrimSpace(event.ConversationID)
	if strings.HasPrefix(strings.ToLower(conversationID), "dm:") {
		return "dm"
	}
	return connectorSafePathSegment(firstNonEmptyString(conversationID, "conversation"))
}

func connectorReadableInputAttachments(attachments []InputAttachment, personID string, scope agentruntime.ConversationResourceScope) []InputAttachment {
	result := make([]InputAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		attachment.Path = connectorReadableAttachmentPath(attachment.Path, personID, scope)
		result = append(result, attachment)
	}
	return result
}

func connectorReadableAgentParts(parts []agentcontract.AgentPart, personID string, scope agentruntime.ConversationResourceScope) []agentcontract.AgentPart {
	result := make([]agentcontract.AgentPart, 0, len(parts))
	for _, part := range parts {
		if part.File != nil {
			file := *part.File
			file.Path = connectorReadableAttachmentPath(file.Path, personID, scope)
			part.File = &file
		}
		if part.Image != nil {
			image := *part.Image
			image.Path = connectorReadableAttachmentPath(image.Path, personID, scope)
			part.Image = &image
		}
		result = append(result, part)
	}
	return result
}

func connectorReadableAttachmentPath(path string, personID string, scope agentruntime.ConversationResourceScope) string {
	if scope.Kind != "private" || strings.TrimSpace(personID) == "" {
		return path
	}
	prefix := "/workspace/private/people/" + connectorSafePathSegment(personID)
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == prefix {
		return "~"
	}
	if strings.HasPrefix(trimmedPath, prefix+"/") {
		return "~/" + strings.TrimPrefix(trimmedPath, prefix+"/")
	}
	return path
}

func connectorUniqueInputAttachments(attachments []InputAttachment) []InputAttachment {
	seen := map[string]bool{}
	result := make([]InputAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		key := connectorInputAttachmentKey(attachment)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, attachment)
	}
	return result
}

func connectorInputAttachmentKey(attachment InputAttachment) string {
	if strings.TrimSpace(attachment.FileID) != "" {
		return strings.TrimSpace(attachment.Platform) + ":" + strings.TrimSpace(attachment.FileID)
	}
	if strings.TrimSpace(attachment.URL) != "" {
		return strings.TrimSpace(attachment.Platform) + ":" + strings.TrimSpace(attachment.URL)
	}
	if strings.TrimSpace(attachment.Path) != "" {
		return strings.TrimSpace(attachment.Platform) + ":" + strings.TrimSpace(attachment.Path)
	}
	return ""
}

type connectorAttachmentMaterialResolver struct {
	adapter          PlatformAdapter
	personID         string
	event            PlatformInboundEvent
	sentSources      *sentAttachmentSourceStore
	attachmentWriter importedAttachmentWriter
}

func (resolver connectorAttachmentMaterialResolver) ResolveAttachmentMaterial(ctx context.Context, materialID string) (agentcontract.VisibleContextMaterial, error) {
	attachment, isFound, errorValue := resolver.findAttachmentMaterial(ctx, materialID)
	if errorValue != nil {
		return agentcontract.VisibleContextMaterial{}, errorValue
	}
	if !isFound {
		return agentcontract.VisibleContextMaterial{}, errors.New("attachment material is not visible in this conversation")
	}
	return resolver.importAttachmentMaterial(ctx, attachment)
}

func (resolver connectorAttachmentMaterialResolver) sentSourceMaterial(attachment InputAttachment) (agentcontract.VisibleContextMaterial, bool) {
	devicePath, isFound := resolver.sentSources.SourcePath(attachment.Platform, attachment.MessageID, attachment.Filename)
	if !isFound {
		return agentcontract.VisibleContextMaterial{}, false
	}
	scope := connectorInputAttachmentScope(resolver.personID, resolver.event)
	readablePath := connectorReadableAttachmentPath(devicePath, resolver.personID, scope)
	if readablePath == devicePath {
		return agentcontract.VisibleContextMaterial{}, false
	}
	sourceAttachment := attachment
	sourceAttachment.Path = readablePath
	sourceAttachment.IsAvailable = true
	sourceAttachment.ErrorCode = ""
	sourceAttachment.Message = ""
	materials := agentVisibleContextMaterials([]InputAttachment{sourceAttachment})
	if len(materials) == 0 {
		return agentcontract.VisibleContextMaterial{}, false
	}
	return materials[0], true
}

const attachmentHistoryPageLimit = 40

func (resolver connectorAttachmentMaterialResolver) findAttachmentMaterial(ctx context.Context, materialID string) (InputAttachment, bool, error) {
	if attachment, isFound := findAttachmentMaterialInContext(resolver.event.Context, materialID); isFound {
		return attachment, true, nil
	}
	historyCursor := strings.TrimSpace(resolver.event.Context.HistoryCursor)
	if historyCursor == "" {
		return InputAttachment{}, false, nil
	}
	for pageCount := 0; pageCount < attachmentHistoryPageLimit; pageCount++ {
		visibleContext, errorValue := resolver.adapter.FetchHistory(ctx, historyCursor, 50)
		if errorValue != nil {
			return InputAttachment{}, false, errors.New("attachment history lookup failed: " + errorValue.Error())
		}
		if attachment, isFound := findAttachmentMaterialInContext(visibleContext, materialID); isFound {
			return attachment, true, nil
		}
		if !visibleContext.HasMoreBefore {
			return InputAttachment{}, false, nil
		}
		nextHistoryCursor := strings.TrimSpace(visibleContext.HistoryCursor)
		if nextHistoryCursor == "" || nextHistoryCursor == historyCursor {
			return InputAttachment{}, false, nil
		}
		historyCursor = nextHistoryCursor
	}
	return InputAttachment{}, false, errors.New("attachment lookup stopped after " + strconv.Itoa(attachmentHistoryPageLimit) + " pages; the conversation is longer than the resolver reads")
}

// A reference is a material ID, or the attachment's exact URL: the URL is the
// one name the model always holds, because it stands in the message text
// itself, long after the message has scrolled out of the visible window.
func findAttachmentMaterialInContext(visibleContext VisibleContext, reference string) (InputAttachment, bool) {
	trimmedReference := strings.TrimSpace(reference)
	if trimmedReference == "" {
		return InputAttachment{}, false
	}
	for _, attachment := range visibleContextAttachmentMaterials(visibleContext) {
		if attachmentMaterialID(attachment) == trimmedReference {
			return attachment, true
		}
		if strings.TrimSpace(attachment.URL) == trimmedReference {
			return attachment, true
		}
	}
	return InputAttachment{}, false
}

func visibleContextAttachmentMaterials(visibleContext VisibleContext) []InputAttachment {
	attachments := []InputAttachment{}
	attachments = append(attachments, visibleContext.Materials...)
	attachments = append(attachments, visibleContext.InputAttachments...)
	for _, message := range visibleContext.Messages {
		attachments = append(attachments, message.InputAttachments...)
	}
	return connectorUniqueInputAttachments(attachments)
}

func (resolver connectorAttachmentMaterialResolver) importAttachmentMaterial(ctx context.Context, attachment InputAttachment) (agentcontract.VisibleContextMaterial, error) {
	if material, isResolved := resolver.sentSourceMaterial(attachment); isResolved {
		return material, nil
	}
	importingAdapter, isSupported := resolver.adapter.(InputAttachmentImportingAdapter)
	if isSupported && strings.TrimSpace(attachment.FileID) != "" {
		material, errorValue := resolver.importAttachmentWithAdapter(ctx, importingAdapter, attachment)
		if errorValue == nil {
			return material, nil
		}
		if strings.TrimSpace(attachment.Path) == "" || strings.TrimSpace(attachment.ErrorCode) != "" {
			return agentcontract.VisibleContextMaterial{}, errorValue
		}
	}
	if strings.TrimSpace(attachment.Path) != "" && strings.TrimSpace(attachment.ErrorCode) == "" {
		return connectorAttachmentToAgentMaterial(resolver.personID, resolver.event, attachment), nil
	}
	if !isSupported {
		return agentcontract.VisibleContextMaterial{}, errors.New("attachment import is unavailable for this platform")
	}
	return resolver.importAttachmentWithAdapter(ctx, importingAdapter, attachment)
}

func (resolver connectorAttachmentMaterialResolver) importAttachmentWithAdapter(ctx context.Context, importingAdapter InputAttachmentImportingAdapter, attachment InputAttachment) (agentcontract.VisibleContextMaterial, error) {
	scope := connectorInputAttachmentScope(resolver.personID, resolver.event)
	messageID := firstNonEmptyString(attachment.MessageID, resolver.event.MessageID)
	result, errorValue := importingAdapter.ImportInputAttachments(ctx, InputAttachmentImportRequest{
		MessageID:           messageID,
		TargetDirectoryPath: connectorInputAttachmentDirectory(scope, resolver.event),
		InputAttachments:    []InputAttachment{attachment},
	})
	if errorValue != nil {
		return agentcontract.VisibleContextMaterial{}, errorValue
	}
	if len(result.InputAttachments) == 0 {
		return agentcontract.VisibleContextMaterial{}, errors.New("attachment import returned no material")
	}
	writtenAttachments, writtenContents := resolver.attachmentWriter.writeAll(ctx, result.InputAttachments)
	result.InputParts = connectorInputPartsAtWrittenPaths(result.InputParts, writtenAttachments)
	result.InputParts = connectorImagePartsShowingTheirBytes(result.InputParts, writtenContents)
	importedAttachment := connectorReadableInputAttachments(writtenAttachments, resolver.personID, scope)[0]
	if strings.TrimSpace(importedAttachment.Path) == "" {
		return agentcontract.VisibleContextMaterial{}, errors.New("attachment import returned no readable path")
	}
	material := agentVisibleContextMaterials([]InputAttachment{importedAttachment})[0]
	return connectorMaterialWithPreview(material, result.InputParts), nil
}

func connectorMaterialWithPreview(material agentcontract.VisibleContextMaterial, parts []agentcontract.AgentPart) agentcontract.VisibleContextMaterial {
	materialID := strings.TrimSpace(material.MaterialID)
	path := strings.TrimSpace(material.Path)
	for _, part := range parts {
		if part.File == nil {
			continue
		}
		if materialID != "" && connectorAgentPartMaterialID(part) != materialID {
			continue
		}
		if materialID == "" && path != "" && strings.TrimSpace(part.File.Path) != path {
			continue
		}
		material.MarkdownPreview = strings.TrimSpace(part.File.MarkdownPreview)
		material.ConversionStatus = strings.TrimSpace(part.File.ConversionStatus)
		material.ConversionMessage = strings.TrimSpace(part.File.ConversionMessage)
		return material
	}
	return material
}

func connectorAgentPartMaterialID(part agentcontract.AgentPart) string {
	fileID := strings.TrimSpace(part.Source.FileID)
	platform := firstNonEmptyString(strings.TrimSpace(part.Source.Platform), "attachment")
	if fileID != "" {
		return platform + ":" + fileID
	}
	if part.File != nil && strings.TrimSpace(part.File.Path) != "" {
		return platform + ":" + connectorSafePathSegment(part.File.Path)
	}
	return ""
}

func connectorAttachmentToAgentMaterial(personID string, event PlatformInboundEvent, attachment InputAttachment) agentcontract.VisibleContextMaterial {
	scope := connectorInputAttachmentScope(personID, event)
	readableAttachments := connectorReadableInputAttachments([]InputAttachment{attachment}, personID, scope)
	materials := agentVisibleContextMaterials(readableAttachments)
	if len(materials) == 0 {
		return agentcontract.VisibleContextMaterial{}
	}
	return materials[0]
}

func connectorSafePathSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	result := strings.Builder{}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			result.WriteRune(character)
			continue
		}
		result.WriteRune('-')
	}
	segment := strings.Trim(result.String(), "-_")
	if segment == "" {
		return "unknown"
	}
	return segment
}
