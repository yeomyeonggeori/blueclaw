package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/learning"
	"github.com/yeomyeonggeori/blueclaw/internal/skill"
)

const (
	skillSearchModeList   = "list"
	skillSearchModeSearch = "search"
	skillSearchModeName   = "name"

	maximumSkillSearchListCount   = 20
	maximumSkillSearchResultCount = 8
	defaultSkillSearchResultCount = 5
)

var skillSearchInputSchema = json.RawMessage(`{
	"type":"object",
	"properties":{
		"queries":{
			"type":"array",
			"maxItems":5,
			"items":{
				"type":"object",
				"properties":{
					"description":{"type":"string","pattern":"\\S"}
				},
				"required":["description"],
				"additionalProperties":false
			}
		},
		"limit":{"type":"integer","minimum":1,"maximum":20},
		"name":{"type":"string","pattern":"\\S"}
	},
	"additionalProperties":false,
	"allOf":[
		{"not":{"required":["name","queries"]}},
		{"not":{"required":["name","limit"]}}
	]
}`)

var skillSearchResultSchema = json.RawMessage(`{
	"type":"object",
	"properties":{
		"mode":{"type":"string","enum":["list","search","name"]},
		"skills":{
			"type":"array",
			"items":{
				"type":"object",
				"properties":{
					"name":{"type":"string","pattern":"\\S"},
					"description":{"type":"string"},
					"toolReferences":{"type":"array","items":{"type":"string","pattern":"\\S"},"uniqueItems":true},
					"prompt":{"type":"string"},
					"sourcePath":{"type":"string","pattern":"\\S"},
					"promptTruncated":{"type":"boolean"}
				},
				"required":["name","description","toolReferences"],
				"additionalProperties":false
			}
		},
		"totalCount":{"type":"integer","minimum":0},
		"hasMore":{"type":"boolean"}
	},
	"required":["mode","skills","totalCount","hasMore"],
	"additionalProperties":false,
	"allOf":[
		{
			"if":{"properties":{"mode":{"const":"name"}},"required":["mode"]},
			"then":{
				"properties":{
					"skills":{
						"minItems":1,
						"maxItems":1,
						"items":{"required":["prompt","sourcePath","promptTruncated"]}
					},
					"totalCount":{"const":1},
					"hasMore":{"const":false}
				}
			}
		},
		{
			"if":{"properties":{"mode":{"enum":["list","search"]}},"required":["mode"]},
			"then":{
				"properties":{
					"skills":{
						"items":{
							"not":{
								"anyOf":[
									{"required":["prompt"]},
									{"required":["sourcePath"]},
									{"required":["promptTruncated"]}
								]
							}
						}
					}
				}
			}
		}
	]
}`)

type skillSearchToolInput struct {
	Queries []agentcontract.SkillSearchQuery `json:"queries"`
	Limit   int                              `json:"limit"`
	Name    string                           `json:"name"`
}

type skillSearchToolOutput struct {
	Mode       string                  `json:"mode"`
	Skills     []skillSearchResultItem `json:"skills"`
	TotalCount int                     `json:"totalCount"`
	HasMore    bool                    `json:"hasMore"`
}

type skillSearchResultItem struct {
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	ToolReferences  []string `json:"toolReferences"`
	Prompt          string   `json:"prompt,omitempty"`
	SourcePath      string   `json:"sourcePath,omitempty"`
	PromptTruncated *bool    `json:"promptTruncated,omitempty"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerSkillSearchTool(toolRegistry *toolcontract.ToolSet, handlerContext toolHandlerContext, availableToolSet *toolcontract.ToolSet) {
	if toolCatalogBuilder.skillRetriever == nil || toolCatalogBuilder.instructionBundleLoader == nil {
		return
	}
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[skillSearchToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        toolcontract.SkillSearchToolName,
			Description: "Search available Blueclaw skills by concise skill-need descriptions. Call with no queries to list available skills. Call with name to fetch one available skill by exact name and include its full instructions.",
			InputSchema: skillSearchInputSchema,
		},
		Handler: func(toolContext context.Context, input skillSearchToolInput) (toolcontract.ToolResult, error) {
			return toolCatalogBuilder.searchSkills(toolContext, input, handlerContext, availableToolSet)
		},
		Result: toolcontract.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) searchSkills(toolContext context.Context, input skillSearchToolInput, handlerContext toolHandlerContext, availableToolSet *toolcontract.ToolSet) (toolcontract.ToolResult, error) {
	instructionBundle := toolCatalogBuilder.instructionBundleLoader()
	visibleInstructions := agentcontract.VisibleSkillInstructionsForRequester(instructionBundle.Skills, handlerContext.request.PersonAccess.Circles)
	visibleInstructions = mergeLearnedSkillInstructions(visibleInstructions, toolCatalogBuilder.learnedSkills(handlerContext.request.RequesterPersonID))
	availableInstructions := toolCatalogBuilder.skillRetriever.Available(agentcontract.AgentRequest{ToolSet: availableToolSet}, visibleInstructions)
	if strings.TrimSpace(input.Name) != "" {
		return skillSearchNameResult(availableInstructions, input.Name), nil
	}
	if len(input.Queries) == 0 {
		return successfulSkillSearchResult(listSkillSearchResult(availableInstructions, input.Limit)), nil
	}
	retrievalResult := toolCatalogBuilder.searchSkillInstructions(toolContext, input, handlerContext, availableToolSet, availableInstructions)
	retrievalResult = includeExactSkillNameMatches(availableInstructions, input.Queries, retrievalResult)
	return successfulSkillSearchResult(searchSkillSearchResult(availableInstructions, retrievalResult, searchSkillResultLimit(input.Limit))), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) learnedSkills(requesterPersonID string) []agentcontract.SkillInstruction {
	if toolCatalogBuilder.learnedSkillLoader == nil || strings.TrimSpace(requesterPersonID) == "" {
		return nil
	}
	audience := "person:" + strings.TrimSpace(requesterPersonID)
	return learnedSkillInstructions(toolCatalogBuilder.learnedSkillLoader(audience))
}

func learnedSkillInstructions(skills []learning.Skill) []agentcontract.SkillInstruction {
	instructions := make([]agentcontract.SkillInstruction, 0, len(skills))
	for _, learnedSkill := range skills {
		parsed, errorValue := skill.ParseDocument(learnedSkill.Instruction)
		if errorValue != nil {
			continue
		}
		instruction := strings.TrimSpace(parsed.Instruction)
		if learnedSkill.Status != "active" || strings.TrimSpace(learnedSkill.ID) == "" || instruction == "" {
			continue
		}
		digest := sha256.Sum256([]byte(instruction))
		instructions = append(instructions, agentcontract.SkillInstruction{
			Name:        "learned/" + learnedSkill.ID,
			Description: firstNonEmptyString(strings.TrimSpace(learnedSkill.Description), strings.TrimSpace(parsed.Description)),
			Prompt:      instruction,
			Source: agentcontract.InstructionSource{
				Path:      "learned://" + learnedSkill.Audience + "/" + learnedSkill.ID + "/SKILL.md",
				SkillName: "learned/" + learnedSkill.ID,
				ByteSize:  len([]byte(instruction)),
				SHA256:    fmt.Sprintf("%x", digest),
			},
		})
	}
	return instructions
}

func mergeLearnedSkillInstructions(installed, learned []agentcontract.SkillInstruction) []agentcontract.SkillInstruction {
	merged := append([]agentcontract.SkillInstruction{}, installed...)
	for _, learnedInstruction := range learned {
		found := false
		for _, installedInstruction := range merged {
			if strings.EqualFold(strings.TrimSpace(installedInstruction.Name), strings.TrimSpace(learnedInstruction.Name)) {
				found = true
				break
			}
		}
		if !found {
			merged = append(merged, learnedInstruction)
		}
	}
	return merged
}

func (toolCatalogBuilder *ToolCatalogBuilder) searchSkillInstructions(toolContext context.Context, input skillSearchToolInput, handlerContext toolHandlerContext, availableToolSet *toolcontract.ToolSet, skillInstructions []agentcontract.SkillInstruction) agentcontract.SkillRetrievalResult {
	request := agentcontract.AgentRequest{
		ProfileName:       handlerContext.request.ProfileName,
		Prompt:            handlerContext.request.Prompt,
		VisibleContext:    handlerContext.request.VisibleContext,
		RequesterPersonID: handlerContext.request.RequesterPersonID,
		RequesterName:     handlerContext.request.RequesterName,
		ToolSet:           availableToolSet,
	}
	querySet := agentcontract.SkillSearchQuerySet{Queries: append([]agentcontract.SkillSearchQuery{}, input.Queries...)}
	return toolCatalogBuilder.skillRetriever.Search(toolContext, request, skillInstructions, querySet, maximumSkillSearchResultCount)
}

func listSkillSearchResult(skillInstructions []agentcontract.SkillInstruction, requestedLimit int) skillSearchToolOutput {
	limit := requestedLimit
	if limit == 0 || limit > maximumSkillSearchListCount {
		limit = maximumSkillSearchListCount
	}
	items := make([]skillSearchResultItem, 0, min(limit, len(skillInstructions)))
	for _, skillInstruction := range skillInstructions[:min(limit, len(skillInstructions))] {
		items = append(items, publicSkillSearchItem(skillInstruction))
	}
	return skillSearchToolOutput{
		Mode:       skillSearchModeList,
		Skills:     items,
		TotalCount: len(skillInstructions),
		HasMore:    len(skillInstructions) > len(items),
	}
}

func skillSearchNameResult(skillInstructions []agentcontract.SkillInstruction, name string) toolcontract.ToolResult {
	skillInstruction, isFound := findSkillInstructionByName(skillInstructions, name)
	if !isFound {
		return toolcontract.ToolFailureResult(toolcontract.FailureNotFound, toolcontract.FailureCodes.NotFound, "skill_search", "visible skill was not found")
	}
	prompt, isTruncated := truncateSkillSearchPrompt(skillInstruction.Prompt)
	item := publicSkillSearchItem(skillInstruction)
	item.Prompt = prompt
	item.SourcePath = stableSkillSourcePath(skillInstruction)
	item.PromptTruncated = booleanPointer(isTruncated)
	return successfulSkillSearchResult(skillSearchToolOutput{
		Mode:       skillSearchModeName,
		Skills:     []skillSearchResultItem{item},
		TotalCount: 1,
	})
}

func searchSkillSearchResult(skillInstructions []agentcontract.SkillInstruction, retrievalResult agentcontract.SkillRetrievalResult, limit int) skillSearchToolOutput {
	boundedCandidates := retrievalResult.SelectedCandidates[:min(maximumSkillSearchResultCount, len(retrievalResult.SelectedCandidates))]
	items := make([]skillSearchResultItem, 0, min(limit, len(boundedCandidates)))
	seenNames := map[string]bool{}
	totalCount := 0
	for _, candidate := range boundedCandidates {
		skillInstruction, isFound := findSkillInstructionByName(skillInstructions, candidate.Name)
		if !isFound {
			continue
		}
		normalizedName := strings.ToLower(strings.TrimSpace(skillInstruction.Name))
		if seenNames[normalizedName] {
			continue
		}
		seenNames[normalizedName] = true
		totalCount++
		if len(items) < limit {
			items = append(items, publicSkillSearchItem(skillInstruction))
		}
	}
	return skillSearchToolOutput{
		Mode:       skillSearchModeSearch,
		Skills:     items,
		TotalCount: totalCount,
		HasMore:    totalCount > len(items),
	}
}

func publicSkillSearchItem(skillInstruction agentcontract.SkillInstruction) skillSearchResultItem {
	return skillSearchResultItem{
		Name:           strings.TrimSpace(skillInstruction.Name),
		Description:    strings.TrimSpace(skillInstruction.Description),
		ToolReferences: normalizedSkillToolReferences(skillInstruction.ToolReferences),
	}
}

func normalizedSkillToolReferences(toolReferences []string) []string {
	normalizedReferences := []string{}
	seenReferences := map[string]bool{}
	for _, toolReference := range toolReferences {
		normalizedReference := strings.TrimSpace(toolReference)
		if normalizedReference == "" || seenReferences[normalizedReference] {
			continue
		}
		seenReferences[normalizedReference] = true
		normalizedReferences = append(normalizedReferences, normalizedReference)
	}
	return normalizedReferences
}

func includeExactSkillNameMatches(skillInstructions []agentcontract.SkillInstruction, queries []agentcontract.SkillSearchQuery, retrievalResult agentcontract.SkillRetrievalResult) agentcontract.SkillRetrievalResult {
	for _, query := range queries {
		skillInstruction, isFound := findSkillInstructionByName(skillInstructions, query.Description)
		if !isFound {
			continue
		}
		retrievalResult.SelectedCandidates = prependSkillCandidate(retrievalResult.SelectedCandidates, agentcontract.SkillCandidate{
			Name:   skillInstruction.Name,
			Score:  1,
			Reason: "exact_name_match",
			Source: skillInstruction.Source,
		})
	}
	return retrievalResult
}

func prependSkillCandidate(candidates []agentcontract.SkillCandidate, candidate agentcontract.SkillCandidate) []agentcontract.SkillCandidate {
	result := []agentcontract.SkillCandidate{candidate}
	for _, existingCandidate := range candidates {
		if strings.EqualFold(strings.TrimSpace(existingCandidate.Name), strings.TrimSpace(candidate.Name)) {
			continue
		}
		result = append(result, existingCandidate)
	}
	return result
}

func findSkillInstructionByName(skillInstructions []agentcontract.SkillInstruction, name string) (agentcontract.SkillInstruction, bool) {
	trimmedName := strings.TrimSpace(name)
	var matchedInstruction agentcontract.SkillInstruction
	isFound := false
	for _, skillInstruction := range skillInstructions {
		if !strings.EqualFold(strings.TrimSpace(skillInstruction.Name), trimmedName) {
			continue
		}
		if isFound {
			return agentcontract.SkillInstruction{}, false
		}
		matchedInstruction = skillInstruction
		isFound = true
	}
	return matchedInstruction, isFound
}

func truncateSkillSearchPrompt(prompt string) (string, bool) {
	characters := []rune(prompt)
	if len(characters) <= maximumSkillSearchPromptLength {
		return prompt, false
	}
	return string(characters[:maximumSkillSearchPromptLength]), true
}

func stableSkillSourcePath(skillInstruction agentcontract.SkillInstruction) string {
	skillName := strings.TrimSpace(skillInstruction.Name)
	sourcePath := "/" + strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(skillInstruction.Source.Path), "\\", "/"), "/")
	if strings.HasPrefix(sourcePath, "/learned://") {
		return sourcePath[1:]
	}
	if strings.Contains(sourcePath, "/.agents/skills/") {
		return "/workspace/.agents/skills/" + skillName + "/SKILL.md"
	}
	return "/workspace/skills/" + skillName + "/SKILL.md"
}

func searchSkillResultLimit(requestedLimit int) int {
	if requestedLimit <= 0 {
		return defaultSkillSearchResultCount
	}
	return min(requestedLimit, maximumSkillSearchResultCount)
}

func successfulSkillSearchResult(output skillSearchToolOutput) toolcontract.ToolResult {
	document := json.RawMessage(marshalToolResult(output))
	return toolcontract.ToolSuccessData(string(document), document)
}

func booleanPointer(value bool) *bool {
	return &value
}
