package e2e

import (
	"encoding/json"
	"fmt"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"os"
	"path/filepath"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/persona"
	"github.com/yeomyeonggeori/blueclaw/internal/skill"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func actionInvokeCapabilityTool(toolName string, input string) string {
	return actionCallTool(toolName, input)
}

func workspaceSkillInstruction(skillName string) agentcontract.SkillInstruction {
	skillBundle, errorValue := (skill.SkillLoader{}).LoadSkillBundle(rootWorkspaceSkillDirectoryPath(skillName))
	if errorValue != nil {
		panic(fmt.Errorf("load root workspace skill %q: %w", skillName, errorValue))
	}
	return skillInstructionFromBundle(skillBundle)
}

// ScenarioSkillNames are the skills these scenarios drive. They are the host's
// to supply, so a standalone checkout finds none of them.
var ScenarioSkillNames = []string{"office", "scheduled-task", "calendar", "internkim-task", "messages"}

func rootWorkspaceSkillDirectoryPath(skillName string) string {
	skillDirectoryPath := findScenarioSkillDirectory(skillName)
	if skillDirectoryPath == "" {
		panic(fmt.Errorf("no skill root in %s carries %q", ScenarioSkillRootsVariable, skillName))
	}
	return skillDirectoryPath
}

// ScenarioSkillRootsVariable names the directories a host offers these scenarios,
// separated by the list separator.
const ScenarioSkillRootsVariable = "BLUECLAW_SCENARIO_SKILL_ROOTS"

func ScenarioSkillRootPaths() []string {
	skillRootPaths := []string{}
	for _, skillRootPath := range filepath.SplitList(os.Getenv(ScenarioSkillRootsVariable)) {
		if trimmedPath := strings.TrimSpace(skillRootPath); trimmedPath != "" {
			skillRootPaths = append(skillRootPaths, trimmedPath)
		}
	}
	return skillRootPaths
}

func findScenarioSkillDirectory(skillName string) string {
	for _, skillRootPath := range ScenarioSkillRootPaths() {
		candidatePath := filepath.Join(skillRootPath, skillName)
		if isExistingDirectory(candidatePath) {
			return candidatePath
		}
	}
	return ""
}

func isExistingDirectory(path string) bool {
	information, errorValue := os.Stat(path)
	return errorValue == nil && information.IsDir()
}

func MissingScenarioSkills() []string {
	_, missingSkills := ScenarioSkillAvailability()
	return missingSkills
}

// ScenarioSkillAvailability separates a host that offered no skill roots, which
// finds none of these bundles, from one that offered roots carrying some and not
// the rest.
func ScenarioSkillAvailability() (found []string, missing []string) {
	found = []string{}
	missing = []string{}
	for _, skillName := range ScenarioSkillNames {
		if findScenarioSkillDirectory(skillName) == "" {
			missing = append(missing, skillName)
			continue
		}
		found = append(found, skillName)
	}
	return found, missing
}

func expectedChangeResponse(change string, asked string) string {
	document, _ := json.Marshal(map[string]any{"expectedChanges": []map[string]string{{"change": change, "asked": asked}}})
	return string(document)
}

func PresentationLocalMultiturnSuccessScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "presentation_local_multiturn_success",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agentcontract.SkillInstruction{officeSkill()},
		AllowedTools:          []string{"conversation_history", "memory_search", "bash", "write", "file_deliver"},
		Turns: []VirtualTurn{{
			Prompt:                 "너 뭐 할 수 있는지 8장 피피티 만들어서 보내줘봐",
			ExpectedSelectedSkills: []string{"office"},
			ExpectedToolCalls:      []string{"bash", "file_deliver"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: toolRequestedEventName("bash"), BodyFragment: "NAME=", Count: 1},
				{Name: toolRequestedEventName("bash"), BodyFragment: "scripts/office deck build", MinCount: 1},
				{Name: toolResultEventName("bash"), BodyFragment: "Building requested formats", MinCount: 1},
				{Name: toolResultEventName("bash"), BodyFragment: "Slide render review", Count: 1},
				{Name: toolResultEventName("file_deliver"), BodyFragment: `"output"`, Count: 1},
			},
			ExpectedValidityReviewPassed: true,
			ExpectedAttachments:          []string{".pptx", ".pdf", ".html", "-notes.txt"},
			ExpectedWorkspaceFiles: []VirtualWorkspaceFileExpectation{
				{
					PathGlob:          "circles/member/tmp/*/DESIGN.md",
					ContainsFragments: []string{"colors:", "Visual direction"},
				},
				{
					PathGlob:           "circles/member/tmp/*/presentation.md",
					ContainsFragments:  []string{"design-source: DESIGN.md", "InternKim capability deck", "너 뭐 할 수 있는지"},
					ForbiddenFragments: []string{"Draft a presentation deck", "user_request:"},
				},
				{
					PathGlob:          "circles/member/tmp/*/review/slide-review.json",
					ContainsFragments: []string{`"passed": true`, `"safeMargin": true`, `"edgeOverflow": true`, `"contactSheets"`},
				},
				{
					PathGlob:          "circles/member/tmp/*/*.html",
					ContainsFragments: []string{"Paperlogy", "Freesentation", "--background", "InternKim capability deck"},
				},
			},
		}},
	}
}

func MemoryGuidedFollowupScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "memory_guided_followup",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search", "memory_remember"},
		Turns: []VirtualTurn{
			{
				Prompt:                 "내 발표 자료는 항상 짧은 문장과 한국어 제목을 선호한다고 기억해줘",
				RouterRequiredEvidence: []string{"memory_remember"},
				ActionResponses: []string{
					actionCallTool("memory_remember", `{"content":"발표 자료는 짧은 문장과 한국어 제목을 선호한다"}`),
					actionFinishMessage("기억해둘게요.", "obs-001"),
				},
				ExpectedToolCalls: []string{"memory_remember"},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.memory_remember.requested", BodyFragment: "한국어 제목", Count: 1},
				},
				ExpectedReplyFragments: []string{"기억"},
			},
			{
				Prompt:          "아까 말한 선호를 반영해서 다음 발표 스타일을 한 문장으로 정리해줘",
				RouterTaskShape: agentcontract.TaskShapeImmediateReply,
				ActionResponses: []string{
					actionFinishMessage("짧은 문장과 한국어 제목 중심으로 정리하겠습니다."),
				},
				ExpectedEvents:         []string{"memory.recall_injected"},
				ExpectedModelContexts:  []string{"발표 자료는 짧은 문장과 한국어 제목을 선호한다"},
				ExpectedReplyFragments: []string{"짧은 문장", "한국어 제목"},
			},
		},
	}
}

func PlainQuestionAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "plain_question_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Turns: []VirtualTurn{{
			Prompt:          "도구 없이 짧게 답해줘. 좋은 회의록의 핵심은 뭐야?",
			RouterTaskShape: agentcontract.TaskShapeImmediateReply,
			ActionResponses: []string{
				actionFinishMessage("좋은 회의록의 핵심은 결정사항, 담당자, 기한을 분명히 남기는 것입니다."),
			},
			ExpectedTaskStatus: task.TaskStatusCompleted,
			ExpectedResponse:   VirtualResponseReply,
			MinimumReplyLength: 1,
			ForbidToolCalls:    true,
		}},
	}
}

func RequestRevisionAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	isThread := false
	return VirtualSessionScenario{
		Name:                  "request_revision_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		RouterTaskShape:       agentcontract.TaskShapeImmediateReply,
		Turns: []VirtualTurn{
			{
				Prompt:                 "출근 시각을 HH:MM으로만 알려줘. 9시 10분",
				BeforeReplyMessages:    []string{"아니 10시", "10분"},
				ReplyTargetID:          "virtual-revision-root",
				ActionResponses:        []string{actionFinishMessage("10:10")},
				ExpectedReplyFragments: []string{"10:10"},
				ExpectedTaskStatus:     task.TaskStatusCompleted,
			},
			{
				Prompt:                 "다음 메시지들로 정하는 주말 계획을 한 문장으로 알려줘",
				IsThread:               &isThread,
				BeforeReplyMessages:    []string{"등산", "토요일 오전"},
				ActionResponses:        []string{actionFinishMessage("토요일 오전에 등산을 갑니다.")},
				ExpectedReplyFragments: []string{"토요일", "등산"},
				ExpectedTaskStatus:     task.TaskStatusCompleted,
			},
		},
	}
}

func WebSearchAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "web_search_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search", "web_search"},
		CapabilityToolNames:   []string{"web_search"},
		InitialToolNames:      []string{"web_search"},
		RouterTaskShape:       agentcontract.TaskShapeResearchTask,
		Turns: []VirtualTurn{{
			Prompt:                 "오늘 기준으로 외부 검색이 필요한 정보를 찾아서 핵심만 알려줘",
			RouterRequiredEvidence: []string{"web_search"},
			ActionResponses: []string{
				actionCallTool("web_search", `{"query":"current external information acceptance test","limit":1}`),
				actionFinishMessage("검색 결과 BlueclawSearchStubToken 정보를 확인했습니다.", "obs-001"),
			},
			ExpectedToolCalls:      []string{"web_search"},
			ExpectedSequence:       []string{toolRequestedEventName("web_search"), toolResultEventName("web_search")},
			ForbiddenEvents:        []string{agentcontract.TaskEventAgentNoProgressLoopStopped},
			ExpectedReplyFragments: []string{"BlueclawSearchStubToken"},
			ExpectedTaskStatus:     task.TaskStatusCompleted,
		}},
	}
}

func BrowserFormAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	browserToolNames := []string{"browser_open", "browser_fill", "browser_select", "browser_press", "browser_wait"}
	return VirtualSessionScenario{
		Name:                  "browser_form_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          browserToolNames,
		CapabilityToolNames:   browserToolNames,
		InitialToolNames:      browserToolNames,
		RouterTaskShape:       agentcontract.TaskShapeResearchTask,
		Turns: []VirtualTurn{{
			Prompt:                 "https://forms.example.test/signup 신청서에 이름은 이샘플, 팀은 지원으로 골라서 제출하고 접수 문구가 뜰 때까지 기다려줘",
			RouterRequiredEvidence: browserToolNames,
			ActionResponses: []string{
				actionCallTool("browser_open", `{"url":"https://forms.example.test/signup"}`),
				actionCallTool("browser_fill", `{"target":"@e2","text":"이샘플"}`),
				actionCallTool("browser_select", `{"target":"@e3","value":"support"}`),
				actionCallTool("browser_press", `{"key":"Enter"}`),
				actionCallTool("browser_wait", `{"selector":"#received"}`),
				actionFinishMessage("이샘플 이름으로 지원팀을 골라 제출했고 접수 문구를 확인했습니다.", "obs-002", "obs-003", "obs-004", "obs-005"),
			},
			ExpectedToolCalls: browserToolNames,
			ExpectedSequence: []string{
				toolRequestedEventName("browser_open"), toolResultEventName("browser_open"),
				toolRequestedEventName("browser_fill"), toolResultEventName("browser_fill"),
				toolRequestedEventName("browser_select"), toolResultEventName("browser_select"),
				toolRequestedEventName("browser_press"), toolResultEventName("browser_press"),
				toolRequestedEventName("browser_wait"), toolResultEventName("browser_wait"),
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: toolRequestedEventName("browser_fill"), BodyFragment: "이샘플", Count: 1},
				{Name: toolRequestedEventName("browser_select"), BodyFragment: "support", Count: 1},
			},
			ForbiddenEvents:        []string{agentcontract.TaskEventAgentNoProgressLoopStopped},
			ExpectedReplyFragments: []string{"접수"},
			ExpectedTaskStatus:     task.TaskStatusCompleted,
		}},
	}
}

func ToolPermissionHidesSkillScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "tool_permission_hides_skill",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agentcontract.SkillInstruction{officeSkill()},
		AllowedTools:          []string{"memory_search", "write"},
		Turns: []VirtualTurn{{
			Prompt:          "피피티 만들어줘",
			RouterTaskShape: agentcontract.TaskShapeImmediateReply,
			ActionResponses: []string{
				actionFinishMessage("현재 profile에서는 필요한 도구가 없어 슬라이드 생성 skill을 실행하지 않았습니다."),
			},
			ExpectedReplyFragments: []string{"필요한 도구"},
		}},
	}
}

func FileWriteAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "file_write_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"write", "file_deliver"},
		InitialToolNames:      []string{"write", "file_deliver"},
		Turns: []VirtualTurn{{
			Prompt:                 "고객지원 FAQ 개편 작업용 JSON 메모 파일을 만들어줘. 제목은 'FAQ 개편', 담당은 '고객지원팀', 상태는 '검토 중'으로 적고 잘 저장됐는지 확인한 다음 완성된 파일을 이 DM에 첨부해줘.",
			RouterRequiredEvidence: []string{"write", "file_deliver"},
			ActionResponses: []string{
				actionCallTool("write", `{"path":"work/customer-support/faq-revision.json","content":"{\"title\":\"FAQ 개편\",\"owner\":\"고객지원팀\",\"status\":\"검토 중\"}\n"}`),
				actionFinalReplyWithAttachment("JSON 메모 파일을 생성하고 첨부해 저장 결과를 확인했습니다.", "work/customer-support/faq-revision.json"),
			},
			ExpectedToolCalls:      []string{"write", "file_deliver"},
			ExpectedToolCallCounts: map[string]int{"write": 1, "file_deliver": 1},
			ExpectedAttachmentFiles: []VirtualAttachmentFileExpectation{{
				Suffix:            ".json",
				ContainsFragments: []string{"FAQ 개편", "고객지원팀", "검토 중"},
			}},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: toolRequestedEventName("write"), BodyFragment: "FAQ 개편", Count: 1},
				{Name: toolRequestedEventName("write"), BodyFragment: "고객지원팀", Count: 1},
				{Name: toolRequestedEventName("write"), BodyFragment: "검토 중", Count: 1},
			},
			ExpectedReplyFragments: []string{"첨부"},
			ForbiddenReplyFragments: []string{
				"permission denied",
				"권한",
				"완료하지 못",
			},
		}},
	}
}

func FileAttachmentChangeCheckScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "file_attachment_change_check",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"write", "file_deliver"},
		InitialToolNames:      []string{"write"},
		Turns: []VirtualTurn{{
			Prompt: "FAQ 개편 JSON 메모 파일을 만들어서 이 DM에 첨부해줘",
			ActionResponses: []string{
				actionCallTool("write", `{"path":"work/customer-support/faq-revision.json","content":"{\"title\":\"FAQ 개편\"}\n"}`),
				actionFinishMessage("JSON 메모 파일을 만들었습니다.", "obs-001"),
				actionFinalReplyWithAttachment("JSON 메모 파일을 첨부했습니다.", "work/customer-support/faq-revision.json"),
			},
			ExpectedChangesResponses: []string{`{"expectedChanges":[{"change":"file created","asked":"FAQ 개편 JSON 메모 파일을 만들어서"},{"change":"file attached","asked":"이 DM에 첨부해줘"}]}`},
			ChangeCheckAnswers:       []map[string]float64{{"expected0": 0.9, "expected1": 0.1}, {"expected0": 0.9, "expected1": 0.9}},
			ExpectedToolCallCounts:   map[string]int{"write": 1, "file_deliver": 1},
			ExpectedAttachmentFiles:  []VirtualAttachmentFileExpectation{{Suffix: ".json", ContainsFragments: []string{"FAQ 개편"}}},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: agentcontract.TaskEventCompletionChangeCheck, BodyFragment: `"unmet":[{"change":"file attached"`, Count: 1},
				{Name: agentcontract.TaskEventAgentCompletionRequired, BodyFragment: "이 DM에 첨부해줘", Count: 1},
			},
		}},
	}
}

func DocumentCreateAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "document_create_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search", "bash", "read", "document_read", "write", "file_deliver"},
		CapabilityToolNames:   []string{"document_read"},
		InitialToolNames:      []string{"bash", "read", "write", "file_deliver"},
		Turns: []VirtualTurn{{
			Prompt:                 "운영팀과 재무팀이 함께 검토할 '분기 결산 운영 검토'라는 짧은 DOCX 문서를 작성해서 이 DM에 첨부해줘. 검토 목적과 다음 단계를 간단히 적고, 현재 상태는 초안, 담당은 운영팀이라고 표시해줘.",
			ExpectedSelectedSkills: []string{"office"},
			ExpectedToolCalls:      []string{"write", "bash", "file_deliver"},
			ExpectedToolCallCounts: map[string]int{"file_deliver": 1},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: toolResultEventName("file_deliver"), BodyFragment: ".docx", Count: 1},
			},
			ExpectedAttachments: []string{".docx"},
			ExpectedWorkspaceFiles: []VirtualWorkspaceFileExpectation{{
				PathGlob: "private/people/*/documents/*.docx",
			}},
			ExpectedReplyFragments: []string{"분기 결산 운영 검토"},
			ExpectedTaskStatus:     task.TaskStatusCompleted,
		}},
	}
}

func AttachmentMaterialReadScenario(artifactDirectoryPath string) VirtualSessionScenario {
	attachment := connectors.InputAttachment{
		Platform:    "mattermost",
		FileID:      "file-1",
		URL:         "https://mattermost.local/api/v4/files/file-1",
		MessageID:   "root-message",
		Filename:    "mascot.png",
		ContentType: "image/png",
		SizeBytes:   13,
	}
	return VirtualSessionScenario{
		Name:                  "attachment_material_read",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search", "bash", "read", "image_read", "document_read"},
		CapabilityToolNames:   []string{"image_read", "document_read"},
		Turns: []VirtualTurn{{
			Prompt:          "다시 이미지 내가 첨부한 거 봐봐",
			RouterTaskShape: agentcontract.TaskShapeResearchTask,
			ContextMessages: []connectors.VisibleContextMessage{{
				Speaker:            "샘플",
				SpeakerCallingName: "샘플 님",
				SpeakerHandle:      "sample",
				Text:               "이거 뭔지 알아?",
				InputAttachments:   []connectors.InputAttachment{attachment},
			}},
			ContextMaterials: []connectors.InputAttachment{attachment},
			ActionResponses: []string{
				actionCallTool("read", `{"path":"https://mattermost.local/api/v4/files/file-1"}`),
				actionFinishMessage("이미지를 확인했습니다.", "obs-001"),
			},
			ExpectedToolCalls:      []string{"read"},
			ExpectedToolCallCounts: map[string]int{"bash": 0},
			ExpectedExposedTools:   []string{"read"},
			ForbiddenExposedTools:  []string{"file_read", "file_preview", "document_read", "image_read"},
			ExpectedModelContexts: []string{
				"url=https://mattermost.local/api/v4/files/file-1",
				"mascot.png",
			},
			ForbiddenModelContexts: []string{
				"mail_message_search",
				"message_send",
			},
			ExpectedReplyFragments: []string{"이미지"},
		}},
	}
}

func AttachmentHTMLPreviewRecoveryScenario(artifactDirectoryPath string) VirtualSessionScenario {
	attachment := connectors.InputAttachment{
		Platform:    "mattermost",
		FileID:      "file-html",
		URL:         "https://mattermost.local/api/v4/files/file-html",
		MessageID:   "message-html",
		Filename:    "kim-intern-automation.html",
		ContentType: "text/html",
		SizeBytes:   691000,
	}
	return VirtualSessionScenario{
		Name:                  "attachment_html_preview_recovery",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search", "bash", "read", "file_preview", "file_read", "image_read"},
		Turns: []VirtualTurn{{
			Prompt:           "이거 파일 내용 보고 어떻게 개선하면 좋을지 말해줘봐",
			RouterTaskShape:  agentcontract.TaskShapeResearchTask,
			InputAttachments: []connectors.InputAttachment{attachment},
			ActionResponses: []string{
				actionCallTool("read", `{"path":"https://mattermost.local/api/v4/files/file-html"}`),
				actionFinishMessage("첨부 HTML을 확인했습니다. 자동화 섹션의 정보 구조와 CTA를 더 선명하게 다듬으면 좋겠습니다.", "obs-001"),
			},
			ExpectedToolCalls: []string{"read"},
			ExpectedToolCallCounts: map[string]int{
				"bash":      0,
				"file_read": 0,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: toolRequestedEventName("read"), BodyFragment: `"path":"https://mattermost.local/api/v4/files/file-html"`, Count: 1},
				{Name: toolResultEventName("read"), BodyFragment: "Virtual HTML Title", Count: 1},
			},
			ExpectedModelContexts: []string{
				"url=https://mattermost.local/api/v4/files/file-html",
				"availableTools=read",
			},
			ExpectedReplyFragments: []string{"첨부 HTML", "정보 구조"},
		}},
	}
}

func AttachmentHTMLPreviousPreviewRecoveryScenario(artifactDirectoryPath string) VirtualSessionScenario {
	attachment := connectors.InputAttachment{
		Platform:    "mattermost",
		FileID:      "file-html",
		URL:         "https://mattermost.local/api/v4/files/file-html",
		MessageID:   "root-message",
		Filename:    "kim-intern-automation.html",
		ContentType: "text/html",
		SizeBytes:   691000,
	}
	return VirtualSessionScenario{
		Name:                  "attachment_html_previous_preview_recovery",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search", "bash", "read", "file_preview", "file_read", "image_read"},
		Turns: []VirtualTurn{{
			Prompt:          "다시",
			RouterTaskShape: agentcontract.TaskShapeResearchTask,
			ContextMessages: []connectors.VisibleContextMessage{{
				Speaker:            "샘플",
				SpeakerCallingName: "샘플 님",
				SpeakerHandle:      "sample",
				Text:               "이거 파일 내용 보고 어떻게 개선하면 좋을지 말해줘봐",
				InputAttachments:   []connectors.InputAttachment{attachment},
			}},
			ContextMaterials: []connectors.InputAttachment{attachment},
			ActionResponses: []string{
				actionCallTool("read", `{"path":"https://mattermost.local/api/v4/files/file-html"}`),
				actionFinishMessage("이전 첨부 HTML을 확인했습니다. 자동화 흐름의 핵심 CTA와 섹션 우선순위를 더 명확히 잡으면 좋겠습니다.", "obs-001"),
			},
			ExpectedToolCalls: []string{"read"},
			ExpectedToolCallCounts: map[string]int{
				"bash":      0,
				"file_read": 0,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: toolRequestedEventName("read"), BodyFragment: `"path":"https://mattermost.local/api/v4/files/file-html"`, Count: 1},
				{Name: toolResultEventName("read"), BodyFragment: "Virtual HTML Title", Count: 1},
			},
			ExpectedModelContexts: []string{
				"Previous attachments:",
				"url=https://mattermost.local/api/v4/files/file-html",
				"availableTools=read",
			},
			ForbiddenReplyFragments: []string{"파일을 찾을 수", "다시 확인", "직접 공유"},
			ExpectedReplyFragments:  []string{"이전 첨부 HTML", "CTA"},
		}},
	}
}

func AttachmentCurrentImageInputScenario(artifactDirectoryPath string) VirtualSessionScenario {
	attachment := connectors.InputAttachment{
		Platform:    "mattermost",
		FileID:      "file-current",
		URL:         "https://mattermost.local/api/v4/files/file-current",
		MessageID:   "virtual-message-001",
		Filename:    "mascot.png",
		ContentType: "image/png",
		SizeBytes:   13,
	}
	return VirtualSessionScenario{
		Name:                  "attachment_current_image_input",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search", "bash", "read", "image_read", "document_read"},
		CapabilityToolNames:   []string{"image_read", "document_read"},
		Turns: []VirtualTurn{{
			Prompt:           "이거 보여? 묘사 좀 자세히 해봐.",
			RouterTaskShape:  agentcontract.TaskShapeImmediateReply,
			InputAttachments: []connectors.InputAttachment{attachment},
			ActionResponses: []string{
				actionFinishMessage(
					"이미지에는 흰색 고양이 형태의 김인턴 마스코트 인형이 서 있습니다. 얼굴에는 검은색으로 윙크하는 눈과 동그란 눈, 작은 입 모양이 붙어 있고, 목에는 '김인턴'이라고 적힌 이름표가 걸려 있습니다. 흰 셔츠와 청바지, 운동화를 착용했고 검은 가방끈과 꼬리가 보여 캐릭터 상품처럼 연출된 사진입니다.",
				),
			},
			ExpectedToolCallCounts: map[string]int{
				"read":          0,
				"image_read":    0,
				"bash":          0,
				"document_read": 0,
			},
			ExpectedModelContexts: []string{
				"url=https://mattermost.local/api/v4/files/file-current",
				"mascot.png",
			},
			ExpectedReplyFragments: []string{"흰색 고양이", "김인턴", "이름표"},
			ForbiddenReplyFragments: []string{
				"상세하게 설명드렸습니다",
			},
			MinimumReplyLength: 80,
		}},
	}
}

func XLowImageVisionFallbackScenario(artifactDirectoryPath string) VirtualSessionScenario {
	attachment := connectors.InputAttachment{
		Platform:    "mattermost",
		FileID:      "file-code-shot",
		URL:         "https://mattermost.local/api/v4/files/file-code-shot",
		MessageID:   "virtual-message-001",
		Filename:    "login_handler.png",
		ContentType: "image/png",
		SizeBytes:   13,
	}
	return VirtualSessionScenario{
		Name:                   "xlow_image_vision_fallback",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RouterTaskLevel:        "xlow",
		XLowTierVisionFallback: true,
		AllowedTools:           []string{"conversation_history", "memory_search"},
		Turns: []VirtualTurn{{
			Prompt:           "이 스크린샷에 있는 로그인 핸들러 코드 리뷰하고 리팩터링 방향 알려줘.",
			InputAttachments: []connectors.InputAttachment{attachment},
			ActionResponses: []string{
				actionFinishMessage("스크린샷의 로그인 핸들러는 비밀번호를 평문 비교하고 에러를 한꺼번에 삼키고 있습니다. 비밀번호 검증은 상수 시간 해시 비교로 바꾸고, 인증 실패와 입력 검증 실패를 분리해 각각의 에러로 올려보내며, 토큰 발급 로직을 별도 함수로 추출해 핸들러는 흐름만 조율하도록 리팩터링하시길 권합니다."),
			},
			ExpectedModelContexts: []string{
				"url=https://mattermost.local/api/v4/files/file-code-shot",
				"login_handler.png",
			},
			ExpectedReplyFragments: []string{"리팩터링", "비밀번호"},
			MinimumReplyLength:     80,
			ExpectedTaskStatus:     task.TaskStatusCompleted,
		}},
	}
}

func ScheduleCreateAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   "schedule_create_acceptance",
		SkillSearchQueries:     []string{"schedule a recurring interval reminder"},
		ArtifactDirectoryPath:  artifactDirectoryPath,
		Skills:                 []agentcontract.SkillInstruction{scheduledTaskSkill()},
		AllowedTools:           append(agentruntime.KernelToolNames(), "schedule_create", "schedule_cancel"),
		CapabilityToolNames:    []string{"schedule_create", "schedule_cancel"},
		InitialToolNames:       []string{"schedule_create", "schedule_cancel"},
		RouterRequiredEvidence: []string{"schedule_create"},
		Turns: []VirtualTurn{{
			Prompt: "1분마다 \"1분 지났습니다\"라고 보내줘",
			ActionResponses: []string{
				actionInvokeCapabilityTool("schedule_create", `{"description":"1분 알림","taskInstruction":"현재 대화에 \"1분 지났습니다\"라고 보낸다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"repeatPolicy":"finite"}`),
				actionFinishMessage("1분마다 알림을 보내도록 예약해둘게요.", "obs-001"),
			},
			ExpectedSelectedSkills: []string{"scheduled-task"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: toolRequestedEventName("schedule_create"), BodyFragment: "schedule_create", Count: 1},
				{Name: toolResultEventName("schedule_create"), BodyFragment: "intervalSecond", Count: 1},
			},
			ExpectedModelContexts: []string{"schedule_create", "taskInstruction", "1분마다"},
			ForbiddenReplyFragments: []string{
				"죄송",
				"제공하고 있지",
				"기능은 제공",
				"못합니다",
			},
		}},
	}
}

func ScheduleLifecycleAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "schedule_lifecycle_acceptance",
		SkillSearchQueries:    []string{"schedule a recurring interval reminder"},
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agentcontract.SkillInstruction{scheduledTaskSkill()},
		AllowedTools:          append(agentruntime.KernelToolNames(), "schedule_create", "schedule_update", "schedule_cancel"),
		CapabilityToolNames:   []string{"schedule_create", "schedule_update", "schedule_cancel"},
		InitialToolNames:      []string{"schedule_create", "schedule_update", "schedule_cancel"},
		Turns: []VirtualTurn{
			{
				Prompt:                 "30분마다 상태 확인하라고 알려줘. 세 번만 해줘",
				RouterRequiredEvidence: []string{"schedule_create"},
				ActionResponses: []string{
					actionInvokeCapabilityTool("schedule_create", `{"description":"상태 확인 알림","taskInstruction":"현재 대화에 \"상태를 확인하세요\"라고 보낸다.","kind":"interval","intervalSecond":1800,"maxRunCount":3,"repeatPolicy":"finite"}`),
					actionFinishMessage("30분마다 세 번 상태 확인 알림을 보내도록 예약해둘게요.", "obs-001"),
				},
				ExpectedSelectedSkills: []string{"scheduled-task"},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: toolRequestedEventName("schedule_create"), BodyFragment: "schedule_create", Count: 1},
					{Name: toolResultEventName("schedule_create"), BodyFragment: "intervalSecond", Count: 1},
				},
				ExpectedModelContexts: []string{"schedule_create", "taskInstruction", "30분마다"},
			},
			{
				Prompt:                 "그 예약을 1시간마다 다섯 번으로 바꿔줘",
				RouterRequiredEvidence: []string{"schedule_update"},
				ActionResponses: []string{
					actionInvokeCapabilityTool("schedule_update", `{"scheduleHint":"virtual-schedule-001","intervalSecond":3600,"maxRunCount":5,"repeatPolicy":"finite"}`),
					actionFinishMessage("예약을 1시간마다 다섯 번으로 수정했습니다.", "obs-001"),
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: toolRequestedEventName("schedule_update"), BodyFragment: "schedule_update", Count: 1},
					{Name: toolResultEventName("schedule_update"), BodyFragment: "intervalSecond", Count: 1},
				},
			},
			{
				Prompt:                 "그 예약 삭제해줘",
				RouterRequiredEvidence: []string{"schedule_cancel"},
				ActionResponses: []string{
					actionInvokeCapabilityTool("schedule_cancel", `{"scheduleHints":["virtual-schedule-001"]}`),
					actionFinishMessage("예약을 삭제했습니다.", "obs-001"),
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: toolRequestedEventName("schedule_cancel"), BodyFragment: "schedule_cancel", Count: 1},
				},
			},
		},
	}
}

func CalendarEventLifecycleAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "calendar_event_lifecycle_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agentcontract.SkillInstruction{calendarSkill()},
		AllowedTools:          append(agentruntime.KernelToolNames(), "event_add", "event_update", "event_delete"),
		CapabilityToolNames:   []string{"event_add", "event_update", "event_delete"},
		InitialToolNames:      []string{"event_add"},
		Turns: []VirtualTurn{
			{
				Prompt:                 "내일 오전 10시에 제품 회고 일정을 캘린더에 추가해줘",
				RouterRequiredEvidence: []string{"event_add"},
				ActionResponses: []string{
					actionInvokeCapabilityTool("event_add", `{"title":"제품 회고","startsAt":"2026-06-13T10:00:00+09:00","endsAt":"2026-06-13T11:00:00+09:00"}`),
					actionFinishMessage("내일 오전 10시에 제품 회고 일정을 추가했습니다.", "obs-001"),
				},
				ExpectedSelectedSkills: []string{"calendar"},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: toolRequestedEventName("event_add"), BodyFragment: "event_add", Count: 1},
				},
			},
			{
				Prompt:                 "그 일정을 내일 오후 2시로 바꿔줘",
				RouterRequiredEvidence: []string{"event_update"},
				ActionResponses: []string{
					actionCallTool("event_update", `{"eventHint":"calendar-event-001","title":"제품 회고","startsAt":"2026-06-13T14:00:00+09:00","endsAt":"2026-06-13T15:00:00+09:00"}`),
					actionFinishMessage("제품 회고 일정을 내일 오후 2시로 변경했습니다.", "obs-001"),
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: toolRequestedEventName("event_update"), BodyFragment: "event_update", Count: 1},
					{Name: toolRequestedEventName("event_update"), BodyFragment: "2026-06-13T14:00:00+09:00", Count: 1},
					{Name: toolResultEventName("event_update"), BodyFragment: "updated virtual calendar event", Count: 1},
				},
			},
			{
				Prompt:                 "그 일정 삭제해줘",
				RouterRequiredEvidence: []string{"event_delete"},
				ActionResponses: []string{
					actionInvokeCapabilityTool("event_delete", `{"eventHint":"calendar-event-001"}`),
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: toolRequestedEventName("event_delete"), BodyFragment: "event_delete", Count: 1},
					{Name: agentcontract.TaskEventApprovalHoldOpened, BodyFragment: `"event_delete"`, Count: 1},
				},
				ExpectedEvents:     []string{agentcontract.TaskEventConfirmationRequested},
				ExpectedTaskStatus: task.TaskStatusWaitingApproval,
			},
			{
				Prompt:         "확인",
				ReplyTargetID:  "virtual-message-003",
				IsThread:       threadReply(),
				RouterApproval: "approve",
				ActionResponses: []string{
					actionFinishMessage("제품 회고 일정을 삭제했습니다.", "obs-002"),
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: agentcontract.TaskEventApprovalHoldSpent, BodyFragment: `"event_delete"`, Count: 1},
				},
				ExpectedEvents:         []string{agentcontract.TaskEventConfirmationReplyClassified},
				ExpectedReplyFragments: []string{"삭제했습니다"},
			},
		},
	}
}

func CalendarFalseFinishRecoveryAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "calendar_false_finish_recovery_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agentcontract.SkillInstruction{calendarSkill()},
		AllowedTools:          []string{"conversation_history", "memory_search", "event_add"},
		CapabilityToolNames:   []string{"event_add"},
		InitialToolNames:      []string{"event_add"},
		Turns: []VirtualTurn{{
			Prompt:                 "7월 13일에 샨보장 미팅을 오전 10시부터 11시까지 등록해줘",
			RouterRequiredEvidence: []string{"event_add"},
			ActionResponses: []string{
				actionFinishMessage("7월 13일 미팅을 오전 10시~11시로 등록했습니다."),
				actionInvokeCapabilityTool("event_add", `{"title":"샨보장 미팅","startsAt":"2026-07-13T10:00:00+09:00","endsAt":"2026-07-13T11:00:00+09:00"}`),
				actionFinishMessage("7월 13일 미팅을 오전 10시~11시로 등록했습니다.", "obs-002"),
			},
			ExpectedChangesResponses: []string{expectedChangeResponse("calendar created", "샨보장 미팅을 오전 10시부터 11시까지 등록해줘")},
			ChangeCheckAnswers:       []map[string]float64{{"expected0": 0.9}},
			ExpectedSelectedSkills:   []string{"calendar"},
			ExpectedToolCalls:        []string{"event_add"},
			ExpectedToolCallCounts: map[string]int{
				"event_add": 1,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: agentcontract.TaskEventCompletionChangeCheck, BodyFragment: `"unrecorded":[{"change":"calendar created"`, Count: 1},
				{Name: agentcontract.TaskEventAgentEvidenceMissing, BodyFragment: "nothing recorded changed this kind of record", Count: 1},
				{Name: agentcontract.TaskEventAgentCompletionRequired, BodyFragment: "nothing recorded changed this kind of record", Count: 1},
				{Name: toolRequestedEventName("event_add"), BodyFragment: "2026-07-13T10:00:00+09:00", Count: 1},
				{Name: agentcontract.TaskEventCompletionChangeCheck, BodyFragment: `"carriedOut":{"expected0":0.9}`, Count: 1},
			},
			ExpectedReplyFragments: []string{"등록했습니다"},
			ForbiddenEvents:        []string{agentcontract.TaskEventAgentNoProgressLoopStopped},
		}},
	}
}

func CalendarChangeAlreadyInPlaceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "calendar_change_already_in_place",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agentcontract.SkillInstruction{calendarSkill()},
		AllowedTools:          append(agentruntime.KernelToolNames(), "event_add", "event_list", "event_update"),
		CapabilityToolNames:   []string{"event_add", "event_list", "event_update"},
		InitialToolNames:      []string{"event_add", "event_list", "event_update"},
		Turns: []VirtualTurn{
			{
				Prompt:                 "10월 5일부터 17일까지 미국 출장 일정 잡아줘",
				RouterRequiredEvidence: []string{"event_add"},
				ActionResponses: []string{
					actionInvokeCapabilityTool("event_add", `{"title":"미국 출장","startsAt":"2026-10-05T09:00:00+09:00","endsAt":"2026-10-17T18:00:00+09:00"}`),
					actionFinishMessage("미국 출장 일정을 10월 5일부터 17일까지로 등록했습니다.", "obs-001"),
				},
				ExpectedSelectedSkills: []string{"calendar"},
			},
			{
				Prompt:                 "다시. 근데 미국 시간으로 15일 비행기라 한국 돌아오면 결국 17일이긴 하더라.",
				RouterRequiredEvidence: []string{"event_update"},
				ActionResponses: []string{
					actionInvokeCapabilityTool("event_list", `{"startsAt":"2026-10-01","endsAt":"2026-10-31"}`),
					actionFinishMessage("미국 출장 일정은 이미 10월 17일까지로 되어 있습니다.", "obs-001"),
				},
				ExpectedChangesResponses: []string{expectedChangeResponse("calendar updated", "한국 돌아오면 결국 17일이긴 하더라")},
				ChangeCheckAnswers:       []map[string]float64{{"expected0": 0.9}},
				ExpectedToolCallCounts: map[string]int{
					"event_list":   1,
					"event_update": 0,
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: agentcontract.TaskEventCompletionChangeCheck, BodyFragment: `"carriedOut":{"expected0":0.9}`, Count: 1},
				},
				ExpectedTaskStatus:     task.TaskStatusCompleted,
				ExpectedReplyFragments: []string{"이미 10월 17일까지"},
				ForbiddenEvents: []string{
					agentcontract.TaskEventAgentCompletionRequired,
					agentcontract.TaskEventAgentNoProgressLoopStopped,
				},
			},
		},
	}
}

// Intake hands a read question a working set that carries the write tool next to
// the read one. Nothing in the request asks for a change and the answer is the
// reply itself, so no evidence rule may stand between this turn and finishing.
func CalendarReadQuestionWithWriteHintScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "calendar_read_question_with_write_hint",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agentcontract.SkillInstruction{calendarSkill()},
		AllowedTools:          []string{"conversation_history", "memory_search", "event_list", "event_add"},
		CapabilityToolNames:   []string{"event_list", "event_add"},
		InitialToolNames:      []string{"event_list", "event_add"},
		Turns: []VirtualTurn{{
			Prompt:                 "7월 13일에 미팅 있어?",
			RouterRequiredEvidence: []string{"event_list"},
			ActionResponses: []string{
				actionInvokeCapabilityTool("event_list", `{"startsAt":"2026-07-13","endsAt":"2026-07-14"}`),
				actionFinishMessage("7월 13일에는 등록된 미팅이 없습니다.", "obs-001"),
			},
			ExpectedSelectedSkills: []string{"calendar"},
			ExpectedToolCalls:      []string{"event_list"},
			ExpectedToolCallCounts: map[string]int{
				"event_list": 1,
				"event_add":  0,
			},
			ExpectedReplyFragments: []string{"없습니다"},
			ExpectedTaskStatus:     task.TaskStatusCompleted,
			ForbiddenEvents:        []string{agentcontract.TaskEventAgentEvidenceMissing, agentcontract.TaskEventAgentCompletionRequired},
		}},
	}
}

func AmbientDutyCalendarAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   "ambient_duty_calendar_acceptance",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RouterRequiredEvidence: []string{"event_add"},
		AddressingResponse:     `{"target":"human","shouldRespond":false,"dutyMatch":true,"dutyName":"calendar_upkeep","dutyConfidence":0.93}`,
		Skills:                 []agentcontract.SkillInstruction{calendarSkill()},
		AllowedTools:           []string{"conversation_history", "memory_search", "event_add"},
		CapabilityToolNames:    []string{"event_add"},
		InitialToolNames:       []string{"event_add"},
		Turns: []VirtualTurn{{
			Prompt:           "@박예시 님 오늘 오후 5시 정기회의에 최견본, 이샘플 님도 참석자로 추가해주세요",
			ExpectedResponse: VirtualResponseBackgroundAction,
			ConversationType: "channel",
			ChannelID:        "town-square",
			ChannelName:      "town-square",
			ReplyTargetID:    "virtual-message-001",
			Addressing:       connectors.AddressingMetadata{},
			ActionResponses: []string{
				actionInvokeCapabilityTool("event_add", `{"title":"정기회의","startsAt":"2026-06-12T17:00:00+09:00","endsAt":"2026-06-12T18:00:00+09:00","participantPersonHints":["최견본","이샘플"]}`),
				actionFinishMessage("정기회의 일정을 추가했습니다.", "obs-001"),
			},
			ExpectedSelectedSkills: []string{"calendar"},
			ExpectedToolCalls:      []string{"event_add"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: agentcontract.TaskEventAgentAmbientDutyLaunch, BodyFragment: `"dutyName":"calendar_upkeep"`, Count: 1},
				{Name: toolRequestedEventName("event_add"), BodyFragment: "2026-06-12T17:00:00+09:00", Count: 1},
				{Name: toolRequestedEventName("event_add"), BodyFragment: "최견본", Count: 1},
				{Name: toolRequestedEventName("event_add"), BodyFragment: "이샘플", Count: 1},
			},
			ExpectedModelContexts: []string{
				"Ambient duty context",
				"Overheard message from",
			},
		}},
	}
}

func AmbientDutyAnnouncementNoEchoScenario(artifactDirectoryPath string) VirtualSessionScenario {
	announcement := "[라운지 이용 안내]\n\n안녕하세요. 이샘플 연구원입니다.\n\n9월 2일(수) 오전 7시부터 10시까지 라운지에서 촬영이 진행될 예정입니다.\n\n양해와 협조 부탁드립니다."
	return VirtualSessionScenario{
		Name:                   "ambient_duty_announcement_no_echo",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RouterRequiredEvidence: []string{"event_add"},
		AddressingResponse:     `{"target":"human","shouldRespond":false,"dutyMatch":true,"dutyName":"calendar_upkeep","dutyConfidence":0.92}`,
		Skills:                 []agentcontract.SkillInstruction{calendarSkill(), messagesSkill()},
		AllowedTools:           []string{"conversation_history", "memory_search", "event_add", "message_send"},
		CapabilityToolNames:    []string{"event_add", "message_send"},
		InitialToolNames:       []string{"event_add"},
		Turns: []VirtualTurn{{
			Prompt:           announcement,
			ExpectedResponse: VirtualResponseBackgroundAction,
			ConversationType: "channel",
			ChannelID:        "town-square",
			ChannelName:      "town-square",
			ReplyTargetID:    "virtual-message-001",
			Addressing:       connectors.AddressingMetadata{},
			ActionResponses: []string{
				actionInvokeCapabilityTool("event_add", `{"title":"라운지 촬영","startsAt":"2026-09-02T07:00:00+09:00","endsAt":"2026-09-02T10:00:00+09:00"}`),
				actionFinishMessage("촬영 일정을 캘린더에 기록했습니다.", "obs-001"),
			},
			ExpectedToolCalls:      []string{"event_add"},
			ExpectedToolCallCounts: map[string]int{"message_send": 0},
			ForbiddenEvents:        []string{toolRequestedEventName("message_send")},
			ForbiddenModelContexts: []string{"message_send"},
			ExpectedModelContexts: []string{
				"Ambient duty context",
				"Overheard message from",
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: agentcontract.TaskEventAgentAmbientDutyLaunch, BodyFragment: `"dutyName":"calendar_upkeep"`, Count: 1},
			},
		}},
	}
}

func AmbientDutyNothingToRecordScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "ambient_duty_nothing_to_record",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AddressingResponse:    `{"target":"human","shouldRespond":false,"dutyMatch":true,"dutyName":"calendar_upkeep","dutyConfidence":0.71}`,
		Skills:                []agentcontract.SkillInstruction{calendarSkill()},
		AllowedTools:          []string{"conversation_history", "memory_search", "event_add"},
		CapabilityToolNames:   []string{"event_add"},
		Turns: []VirtualTurn{{
			Prompt:           "라운지 커피머신 원두 바뀐 거 아세요? 훨씬 낫네요",
			ExpectedResponse: VirtualResponseBackgroundAction,
			ConversationType: "channel",
			ChannelID:        "town-square",
			ChannelName:      "town-square",
			ReplyTargetID:    "virtual-message-001",
			Addressing:       connectors.AddressingMetadata{},
			ActionResponses: []string{
				actionFinishMessage("기록할 일정이 없습니다."),
			},
			ExpectedToolCallCounts: map[string]int{"event_add": 0},
			ExpectedTaskStatus:     task.TaskStatusCompleted,
		}},
	}
}

func flowTaskSkill() agentcontract.SkillInstruction {
	return workspaceSkillInstruction("internkim-task")
}

func AmbientTaskCaptureAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "ambient_task_capture_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AddressingResponse:    `{"target":"human","shouldRespond":false,"dutyMatch":true,"dutyName":"team_flow_update","dutyConfidence":0.9}`,
		Skills:                []agentcontract.SkillInstruction{flowTaskSkill()},
		AllowedTools:          []string{"conversation_history", "memory_search", "task_add", "task_list", "task_update"},
		CapabilityToolNames:   []string{"task_add", "task_list", "task_update"},
		InitialToolNames:      []string{"task_add"},
		Turns: []VirtualTurn{{
			Prompt:                 "@박예시 님 월요일까지 신규 가입 플로우 점검 작업 해주세요",
			ExpectedResponse:       VirtualResponseBackgroundAction,
			RouterRequiredEvidence: []string{"task_add"},
			ConversationType:       "channel",
			ChannelID:              "town-square",
			ChannelName:            "town-square",
			ReplyTargetID:          "virtual-message-010",
			Addressing:             connectors.AddressingMetadata{OtherPersonMentioned: true},
			ActionResponses: []string{
				actionInvokeCapabilityTool("task_add", `{"title":"신규 가입 플로우 점검","participantPersonHints":["예시"]}`),
				actionFinishMessage("예시 님 업무로 추가했습니다.", "obs-001"),
			},
			ExpectedToolCalls: []string{"task_add"},
			ExpectedToolCallCounts: map[string]int{
				"task_add": 1,
				"bash":     0,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: agentcontract.TaskEventAgentAmbientDutyLaunch, BodyFragment: `"dutyName":"team_flow_update"`, Count: 1},
				{Name: toolRequestedEventName("task_add"), BodyFragment: "예시", Count: 1},
				{Name: toolResultEventName("task_add"), BodyFragment: `"ownerName":"예시"`, Count: 1},
				{Name: toolResultEventName("task_add"), BodyFragment: `"effect":"created"`, Count: 1},
			},
			ExpectedTaskStatus: task.TaskStatusCompleted,
			ExpectedModelContexts: []string{
				"Ambient duty context",
				"Overheard message from",
			},
			ForbiddenEvents: []string{toolRequestedEventName("bash")},
		}, {
			Prompt:                 "@박예시 님 그 작업 마감은 수요일로 변경해주세요",
			ExpectedResponse:       VirtualResponseBackgroundAction,
			RouterRequiredEvidence: []string{"task_update"},
			ConversationType:       "channel",
			ChannelID:              "town-square",
			ChannelName:            "town-square",
			ReplyTargetID:          "virtual-message-011",
			Addressing:             connectors.AddressingMetadata{OtherPersonMentioned: true},
			ActionResponses: []string{
				actionInvokeCapabilityTool("task_update", `{"taskHint":"task-1","endsAt":"2026-06-24"}`),
				actionFinishMessage("예시 님 업무 마감을 수요일로 변경했습니다.", "obs-001"),
			},
			ExpectedToolCalls: []string{"task_update"},
			ExpectedToolCallCounts: map[string]int{
				"task_add":    0,
				"task_update": 1,
			},
			ExpectedTaskStatus: task.TaskStatusCompleted,
		}},
	}
}

func ChangeCheckRecoveryAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "change_check_recovery_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agentcontract.SkillInstruction{flowTaskSkill()},
		AllowedTools:          []string{"conversation_history", "memory_search", "task_add", "task_list", "task_update"},
		CapabilityToolNames:   []string{"task_add", "task_list", "task_update"},
		InitialToolNames:      []string{"task_add"},
		Turns: []VirtualTurn{{
			Prompt:                 "분기 결산 누락 확인 업무를 7월 24일 마감으로 추가해줘",
			RouterRequiredEvidence: []string{"task_add", "task_update"},
			ActionResponses: []string{
				actionInvokeCapabilityTool("task_add", `{"title":"분기 결산 누락 확인"}`),
				actionFinishMessage("업무를 추가했습니다.", "obs-001"),
				actionInvokeCapabilityTool("task_update", `{"taskHint":"task-1","endsAt":"2026-07-24"}`),
				actionFinishMessage("마감일을 포함해 업무를 추가했습니다.", "obs-003"),
			},
			ExpectedChangesResponses: []string{expectedChangeResponse("task created", "분기 결산 누락 확인 업무를 7월 24일 마감으로 추가해줘")},
			ChangeCheckAnswers:       []map[string]float64{{"expected0": 0.1}, {"expected0": 0.9}},
			ExpectedToolCalls:        []string{"task_add", "task_update"},
			ExpectedToolCallCounts: map[string]int{
				"task_add":    1,
				"task_update": 1,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: agentcontract.TaskEventCompletionChangeCheck, BodyFragment: `"unmet":[{"change":"task created"`, Count: 1},
				{Name: agentcontract.TaskEventCompletionChangeCheck, BodyFragment: `"carriedOut":{"expected0":0.9}`, Count: 1},
				{Name: agentcontract.TaskEventAgentEvidenceMissing, BodyFragment: "7월 24일 마감", Count: 1},
				{Name: agentcontract.TaskEventAgentCompletionRequired, BodyFragment: "7월 24일 마감", Count: 1},
			},
			ExpectedTaskStatus: task.TaskStatusCompleted,
		}},
	}
}

func SkillLifecycleAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	skillName := "memo-helper"
	skillContent := userManagedSkillDocument(skillName)
	return VirtualSessionScenario{
		Name:                  "skill_lifecycle_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search", "skill_add", "skill_remove"},
		Turns: []VirtualTurn{
			{
				Prompt:                 "간단한 메모 정리 custom skill을 등록해줘",
				RouterRequiredEvidence: []string{"skill_add"},
				ActionResponses: []string{
					actionCallTool("skill_add", skillAddToolInput(skillName, skillContent)),
					actionFinishMessage("memo-helper skill을 등록했습니다.", "obs-001"),
				},
				ExpectedToolCalls: []string{"skill_add"},
				ExpectedToolCallCounts: map[string]int{
					"skill_add":    1,
					"skill_remove": 0,
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: toolResultEventName("skill_add"), BodyFragment: "created", Count: 1},
				},
				ExpectedWorkspaceFiles: []VirtualWorkspaceFileExpectation{{
					PathGlob:          ".agents/skills/memo-helper/SKILL.md",
					ContainsFragments: []string{"name: memo-helper", "Organize short notes into concise memos"},
				}},
				ExpectedReplyFragments: []string{"memo-helper", "등록"},
			},
			{
				Prompt:                 "방금 등록한 memo-helper skill 삭제해줘",
				RouterRequiredEvidence: []string{"skill_remove"},
				ActionResponses: []string{
					actionCallTool("skill_remove", `{"name":"memo-helper"}`),
					actionFinishMessage("memo-helper skill을 삭제했습니다.", "obs-001"),
				},
				ExpectedToolCalls: []string{"skill_remove"},
				ExpectedToolCallCounts: map[string]int{
					"skill_add":    0,
					"skill_remove": 1,
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: toolResultEventName("skill_remove"), BodyFragment: "removed", Count: 1},
				},
				ExpectedReplyFragments: []string{"memo-helper", "삭제"},
			},
		},
	}
}

func CapabilityQuestionAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "capability_question_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agentcontract.SkillInstruction{officeSkill(), scheduledTaskSkill()},
		AllowedTools:          []string{"memory_search"},
		Turns: []VirtualTurn{{
			Prompt:          "너는 무엇을 할 수 있어?",
			RouterTaskShape: agentcontract.TaskShapeResearchTask,
			ActionResponses: []string{
				actionFinishMessage("발표 자료 같은 산출물과 일정 예약을 할 수 있습니다."),
			},
			ForbiddenExposedTools:  []string{"skill_search"},
			ForbidToolCalls:        true,
			ForbiddenEvents:        []string{agentcontract.TaskEventAgentEvidenceMissing},
			ExpectedModelContexts:  []string{"- skills: memory, office, scheduled-task"},
			ExpectedReplyFragments: []string{"일정 예약"},
		}},
	}
}

func TaskHistoryQuestionAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "task_history_question_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"memory_search"},
		Turns: []VirtualTurn{
			{
				Prompt:          "계약서 확인 요약 작업을 완료했다고 답해줘",
				RouterTaskShape: agentcontract.TaskShapeImmediateReply,
				ActionResponses: []string{
					actionFinishMessage("계약서 확인 요약 작업을 완료했습니다."),
				},
				ExpectedToolCallCounts: map[string]int{
					"task_list": 0,
				},
				ExpectedReplyFragments: []string{"계약서 확인 요약", "완료"},
			},
			{
				Prompt:          "최근에 어떤 작업을 했는지 알려줘",
				RouterTaskShape: agentcontract.TaskShapeResearchTask,
				ActionResponses: []string{
					actionFinishMessage("최근에는 계약서 확인 요약 작업을 완료했습니다."),
				},
				ForbiddenExposedTools:  []string{"conversation_history"},
				ForbidToolCalls:        true,
				ForbiddenEvents:        []string{agentcontract.TaskEventAgentEvidenceMissing},
				ExpectedModelContexts:  []string{"계약서 확인 요약 작업을 완료했습니다."},
				ExpectedReplyFragments: []string{"계약서 확인 요약"},
			},
		},
	}
}

func MemoryExplicitToolAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "memory_explicit_tool_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search", "memory_remember"},
		Turns: []VirtualTurn{
			{
				Prompt:                 "Please remember that my preferred language is Korean.",
				RouterRequiredEvidence: []string{"memory_remember"},
				ActionResponses: []string{
					actionCallTool("memory_remember", `{"content":"preferred language is Korean"}`),
					actionFinishMessage("Remembered: your preferred language is Korean.", "obs-001"),
				},
				ExpectedToolCalls: []string{"memory_remember"},
				ExpectedToolCallCounts: map[string]int{
					"memory_remember": 1,
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: toolRequestedEventName("memory_remember"), BodyFragment: "Korean", Count: 1},
				},
				ExpectedReplyFragments: []string{"Korean"},
			},
			{
				Prompt:                 "What language do I prefer?",
				RouterTaskShape:        agentcontract.TaskShapeResearchTask,
				RouterRequiredEvidence: []string{"memory_search"},
				ActionResponses: []string{
					actionCallTool("memory_search", `{"query":"preferred language"}`),
					actionFinishMessage("Your preferred language is Korean.", "obs-001"),
				},
				ExpectedToolCalls: []string{"memory_search"},
				ExpectedToolCallCounts: map[string]int{
					"memory_search": 1,
				},
				ExpectedReplyFragments: []string{"Korean"},
			},
		},
	}
}

func PersonaProfileUpdateAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   "persona_profile_update_acceptance",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		AllowedTools:           []string{"persona_read", "persona_update"},
		WritableWorkspacePaths: []string{"private/people/person-1/.internkim/user.json", ".blueclaw/state/persona-backup/people/person-1/user.json"},
		InitialWorkspaceFiles:  map[string]string{persona.SoulFileName: `{"schemaVersion":1}`},
		Turns: []VirtualTurn{
			{
				Prompt:                 "내가 앞으로 한국어로 답변받기를 원한다는 설정을 저장해줘",
				RouterRequiredEvidence: []string{"persona_update"},
				ActionResponses: []string{
					actionCallTool("persona_update", `{"target":"user","patch":{"language":{"default":"ko"}}}`),
					actionFinishMessage("한국어 답변 설정을 저장했습니다.", "obs-001"),
				},
				ExpectedToolCalls: []string{"persona_update"},
				ExpectedToolCallCounts: map[string]int{
					"persona_update": 1,
				},
				ExpectedWorkspaceFiles: []VirtualWorkspaceFileExpectation{{
					PathGlob:          "private/people/person-1/.internkim/user.json",
					ContainsFragments: []string{`"default": "ko"`},
				}},
				ExpectedReplyFragments: []string{"한국어", "저장"},
			},
			{
				Prompt:                 "저장된 내 언어 설정을 읽어줘",
				RouterTaskShape:        agentcontract.TaskShapeResearchTask,
				RouterRequiredEvidence: []string{"persona_read"},
				ActionResponses: []string{
					actionCallTool("persona_read", `{"target":"user"}`),
					actionFinishMessage("저장된 언어 설정은 한국어입니다.", "obs-001"),
				},
				ExpectedToolCalls: []string{"persona_read"},
				ExpectedEventCounts: []VirtualEventCount{{
					Name:         toolResultEventName("persona_read"),
					BodyFragment: `"default":"ko"`,
					Count:        1,
				}},
				ExpectedReplyFragments: []string{"한국어"},
			},
			{
				Prompt:                 "공유 영혼 원칙도 한국어로 바꿔줘",
				RouterTaskShape:        agentcontract.TaskShapeResearchTask,
				RouterRequiredEvidence: []string{"persona_read"},
				ActionResponses: []string{
					actionCallTool("persona_update", `{"target":"soul","patch":{"language":{"default":"ko"}}}`),
					actionCallTool("persona_read", `{"target":"soul"}`),
					actionFinishMessage("공유 원칙은 foreground에서 변경할 수 없습니다. 현재 원칙을 다시 확인했습니다.", "obs-002"),
				},
				ExpectedToolCalls: []string{"persona_read"},
				ForbiddenEvents:   []string{toolRequestedEventName("persona_update"), toolResultEventName("persona_update")},
				ExpectedEventCounts: []VirtualEventCount{{
					Name:         "agent.tool_input_malformed",
					BodyFragment: `"tool":"persona_update"`,
					Count:        1,
				}},
				ExpectedReplyFragments: []string{"변경할 수 없습니다"},
			},
		},
	}
}

func FailureExplanationAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "failure_explanation_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"memory_search", "bash"},
		TurnOptions: agentcontract.TurnOptions{
			RecoveryBudget: agentcontract.RecoveryBudget{
				CorrectedRetry: -1,
				AlternateRoute: -1,
				AdjacentTool:   -1,
				NoToolFallback: -1,
			},
		},
		Turns: []VirtualTurn{
			{
				Prompt:                 "Run the analysis.",
				RouterRequiredEvidence: []string{"bash"},
				ActionResponses: []string{
					actionCallTool("bash", `{"command":"printf 'permission denied blocked_by_captcha' >&2; exit 126","workingDirectoryPath":"~","timeoutSecond":30}`),
					actionFailMessage("shell: permission denied"),
				},
				ExpectedSequence: []string{toolRequestedEventName("bash"), toolResultEventName("bash")},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: toolResultEventName("bash"), BodyFragment: "permission denied", Count: 1},
				},
				ExpectedTaskStatus: task.TaskStatusFailed,
			},
			{
				Prompt:          "왜 실패했어?",
				RouterTaskShape: agentcontract.TaskShapeResearchTask,
				ActionResponses: []string{
					actionFinishMessage("shell 실행이 permission denied 때문에 실패했습니다."),
				},
				ForbiddenExposedTools:  []string{"conversation_history"},
				ForbidToolCalls:        true,
				ForbiddenEvents:        []string{agentcontract.TaskEventAgentEvidenceMissing},
				ExpectedModelContexts:  []string{"permission denied"},
				ExpectedReplyFragments: []string{"permission denied"},
			},
		},
	}
}

func OneTimeScheduleAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "one_time_schedule_acceptance",
		SkillSearchQueries:    []string{"schedule a one-time reminder"},
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agentcontract.SkillInstruction{scheduledTaskSkill()},
		AllowedTools:          []string{"conversation_history", "memory_search", "schedule_create", "schedule_cancel"},
		CapabilityToolNames:   []string{"schedule_create", "schedule_cancel"},
		Turns: []VirtualTurn{{
			Prompt:                 "2027년 1월 15일 오전 9시에 계약서 확인 알림을 한 번만 예약해줘",
			RouterRequiredEvidence: []string{"schedule_create"},
			ActionResponses: []string{
				actionInvokeCapabilityTool("schedule_create", `{"description":"계약서 확인 알림","taskInstruction":"현재 대화에 \"계약서를 확인하세요\"라고 보낸다.","kind":"once","runAt":"2027-01-15T00:00:00Z"}`),
				actionFinishMessage("2027년 1월 15일 오전 9시에 한 번 알림을 보내도록 예약해둘게요.", "obs-001"),
			},
			ExpectedSelectedSkills: []string{"scheduled-task"},
			ExpectedToolCalls:      []string{"schedule_create"},
			ExpectedModelContexts:  []string{"schedule_create", "runAt", "once"},
			ExpectedReplyFragments: []string{"2027년 1월 15일", "한 번"},
		}},
	}
}

func skillAddToolInput(skillName string, skillContent string) string {
	return `{"name":` + quote(skillName) + `,"content":` + quote(skillContent) + `}`
}

func userManagedSkillDocument(skillName string) string {
	return `---
name: ` + skillName + `
description: Organize short notes into concise memos and extract action items when the user asks for memo help.
---
Organize notes into concise memos with action items and owners.`
}

func calendarSkill() agentcontract.SkillInstruction {
	return workspaceSkillInstruction("calendar")
}

func messagesSkill() agentcontract.SkillInstruction {
	return workspaceSkillInstruction("messages")
}

func scheduledTaskSkill() agentcontract.SkillInstruction {
	return workspaceSkillInstruction("scheduled-task")
}

func AskChoiceReplyAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "ask_choice_reply_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search", "ask_input"},
		Turns: []VirtualTurn{{
			Prompt:                 "둘 중 하나 고르게 해줘",
			RouterRequiredEvidence: []string{toolcontract.AskInputToolName},
			ActionResponses: []string{
				actionReplyExpectingAnswer("어느 쪽으로 진행할까요? 첫 번째 또는 두 번째 중에서 알려 주세요."),
			},
			ExpectedToolCalls:      []string{"ask_input"},
			ExpectedEvents:         []string{agentcontract.TaskEventAskRequested},
			ExpectedReplyFragments: []string{"어느 쪽으로 진행할까요?"},
		}, {
			Prompt:          "두 번째",
			ReplyTargetID:   "virtual-message-001",
			IsThread:        threadReply(),
			RouterTaskShape: agentcontract.TaskShapeImmediateReply,
			ActionResponses: []string{
				actionFinishMessage("두 번째로 진행하겠습니다."),
			},
			ExpectedEvents:         []string{agentcontract.TaskEventAskResolved},
			ExpectedReplyFragments: []string{"두 번째"},
			ExpectedModelContexts:  []string{"어느 쪽으로 진행할까요?"},
		}},
	}
}

func threadReply() *bool {
	isThread := true
	return &isThread
}

func AskChoiceReplyOverACPScenario(artifactDirectoryPath string) VirtualSessionScenario {
	scenario := AskChoiceReplyAcceptanceScenario(artifactDirectoryPath)
	scenario.Name = "ask_choice_reply_over_acp"
	scenario.IsDeliveredOverACP = true
	scenario.Turns[1].ReadsNoIntakeDecision = true
	return scenario
}

func AskRootMessageStartsATaskScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "ask_root_message_starts_a_task",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search", "ask_input"},
		Turns: []VirtualTurn{{
			Prompt:                 "둘 중 하나 고르게 해줘",
			RouterRequiredEvidence: []string{toolcontract.AskInputToolName},
			ActionResponses: []string{
				actionReplyExpectingAnswer("어느 쪽으로 진행할까요? 첫 번째 또는 두 번째 중에서 알려 주세요."),
			},
			ExpectedToolCalls:  []string{"ask_input"},
			ExpectedEvents:     []string{agentcontract.TaskEventAskRequested},
			ExpectedTaskStatus: task.TaskStatusWaitingUserInput,
		}, {
			Prompt:       "두 번째",
			RouterChoice: "두 번째",
			ActionResponses: []string{
				actionFinishMessage("새 요청으로 받았습니다."),
			},
			ForbiddenEvents:        []string{agentcontract.TaskEventAskResolved},
			ExpectedReplyFragments: []string{"새 요청"},
			ExpectedTaskStatus:     task.TaskStatusCompleted,
		}, {
			Prompt:          "두 번째",
			ReplyTargetID:   "virtual-message-001",
			IsThread:        threadReply(),
			RouterTaskShape: agentcontract.TaskShapeImmediateReply,
			ActionResponses: []string{
				actionFinishMessage("두 번째로 진행하겠습니다."),
			},
			ExpectedEvents:         []string{agentcontract.TaskEventAskResolved},
			ExpectedReplyFragments: []string{"두 번째"},
			ExpectedTaskStatus:     task.TaskStatusCompleted,
		}},
	}
}

func AskRootMessageStartsATaskOverACPScenario(artifactDirectoryPath string) VirtualSessionScenario {
	scenario := AskRootMessageStartsATaskScenario(artifactDirectoryPath)
	scenario.Name = "ask_root_message_starts_a_task_over_acp"
	scenario.IsDeliveredOverACP = true
	scenario.Turns[2].ReadsNoIntakeDecision = true
	return scenario
}

func DirectMessageSendConfirmAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   "dm_send_confirm_acceptance",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RouterRequiredEvidence: []string{"message_send"},
		ScriptedExecutionPlan: &agentcontract.ExecutionPlan{
			OriginalInstruction:     "테스트이한테 DM으로 오늘 오후 3시에 확인하자고 보내줘",
			Summary:                 "테스트이에게 오늘 오후 3시 확인 요청을 DM으로 보낸다",
			Targets:                 []string{"테스트"},
			ExternalSend:            true,
			ThirdPartyExternalSend:  true,
			MissingInformation:      []string{},
			ContinuationInstruction: "테스트이에게 오늘 오후 3시에 확인하자는 DM을 보낸다",
		},
		AllowedTools:     append(agentruntime.KernelToolNames(), "message_send"),
		InitialToolNames: []string{"message_send"},
		CapabilityToolDescriptors: []agentruntime.CapabilityToolDescriptor{{
			Name:             "message_send",
			RequiresApproval: true,
		}},
		Turns: []VirtualTurn{{
			Prompt: "테스트이한테 DM으로 오늘 오후 3시에 확인하자고 보내줘",
			ActionResponses: []string{
				actionCallTool("message_send", `{"targetType":"directMessage","personHint":"테스트","message":"오늘 오후 3시에 확인하자"}`),
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: toolRequestedEventName("message_send"), BodyFragment: `"targetType":"directMessage"`, Count: 1},
				{Name: agentcontract.TaskEventApprovalHoldOpened, BodyFragment: `"message_send"`, Count: 1},
				{Name: agentcontract.TaskEventAgentFailureDebtCreated, BodyFragment: "", Count: 0},
			},
			ExpectedEvents:         []string{agentcontract.TaskEventConfirmationRequested},
			ExpectedReplyFragments: []string{"테스트", "오늘 오후 3시에 확인하자"},
			ExpectedTaskStatus:     task.TaskStatusWaitingApproval,
		}, {
			Prompt:         "확인",
			ReplyTargetID:  "virtual-message-001",
			IsThread:       threadReply(),
			RouterApproval: "approve",
			ActionResponses: []string{
				actionFinishMessage("테스트이에게 DM을 보냈습니다.", "obs-002"),
			},
			ExpectedToolCalls: []string{"message_send"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: toolRequestedEventName("message_send"), BodyFragment: `"targetType":"directMessage"`, Count: 2},
				{Name: toolResultEventName("message_send"), BodyFragment: "virtual-platform-message-001", Count: 1},
				{Name: agentcontract.TaskEventApprovalHoldSpent, BodyFragment: `"message_send"`, Count: 1},
			},
			ExpectedEvents:         []string{agentcontract.TaskEventConfirmationReplyClassified},
			ExpectedModelContexts:  []string{"virtual-platform-message-001"},
			ExpectedReplyFragments: []string{"DM", "보냈습니다"},
		}},
	}
}

func ChannelPostAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   "channel_post_acceptance",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RouterRequiredEvidence: []string{"message_send"},
		ScriptedExecutionPlan: &agentcontract.ExecutionPlan{
			OriginalInstruction:     "announcements 채널에 오늘 5시에 전체 공지 회의 있다고 올려줘",
			Summary:                 "announcements 채널에 오늘 5시 전체 공지 회의를 게시한다",
			Targets:                 []string{"announcements"},
			ExternalSend:            true,
			ThirdPartyExternalSend:  true,
			MissingInformation:      []string{},
			ContinuationInstruction: "announcements 채널에 오늘 5시 전체 공지 회의를 게시한다",
		},
		AllowedTools:              append(agentruntime.KernelToolNames(), "message_send"),
		CapabilityToolNames:       []string{"message_send"},
		InitialToolNames:          []string{"message_send"},
		CapabilityToolDescriptors: []agentruntime.CapabilityToolDescriptor{{Name: "message_send", RequiresApproval: true}},
		Turns: []VirtualTurn{{
			Prompt: "announcements 채널에 오늘 5시에 전체 공지 회의 있다고 올려줘",
			ActionResponses: []string{
				actionCallTool("message_send", `{"targetType":"channel","channelName":"announcements","message":"오늘 5시에 전체 공지 회의가 있습니다."}`),
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: toolRequestedEventName("message_send"), BodyFragment: `"targetType":"channel"`, Count: 1},
				{Name: agentcontract.TaskEventApprovalHoldOpened, BodyFragment: `"message_send"`, Count: 1},
			},
			ExpectedEvents:         []string{agentcontract.TaskEventConfirmationRequested},
			ExpectedReplyFragments: []string{"announcements", "오늘 5시"},
			ExpectedTaskStatus:     task.TaskStatusWaitingApproval,
		}, {
			Prompt:         "확인",
			ReplyTargetID:  "virtual-message-001",
			IsThread:       threadReply(),
			RouterApproval: "approve",
			ActionResponses: []string{
				actionFinishMessage("announcements 채널에 공지를 올렸습니다.", "obs-002"),
			},
			ExpectedToolCalls: []string{"message_send"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: toolRequestedEventName("message_send"), BodyFragment: `"targetType":"channel"`, Count: 2},
				{Name: toolRequestedEventName("message_send"), BodyFragment: `"channelName":"announcements"`, Count: 2},
				{Name: toolRequestedEventName("message_send"), BodyFragment: `"targetType":"directMessage"`, Count: 0},
				{Name: agentcontract.TaskEventApprovalHoldSpent, BodyFragment: `"message_send"`, Count: 1},
			},
			ExpectedReplyFragments: []string{"채널", "올렸습니다"},
		}},
	}
}

func PlatformMessageEditAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   "platform_message_edit_acceptance",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RouterRequiredEvidence: []string{"message_update"},
		AllowedTools:           append(agentruntime.KernelToolNames(), "message_search", "message_update"),
		CapabilityToolNames:    []string{"message_search", "message_update"},
		InitialToolNames:       []string{"message_search", "message_update"},
		Turns: []VirtualTurn{{
			Prompt: "방금 올린 공지 message virtual-platform-message-001 에서 '오후 5시'를 '오후 6시'로 바꿔줘",
			ActionResponses: []string{
				actionCallTool("message_search", `{"scope":"currentChannel","messageIDs":["virtual-platform-message-001"]}`),
				actionCallToolWithMessage("message_update", "공지 메시지 문구를 수정합니다.", `{"messageID":"virtual-platform-message-001","oldText":"오후 5시","newText":"오후 6시"}`),
				actionFinishMessage("공지 메시지 문구를 수정했습니다.", "obs-002"),
			},
			ExpectedToolCalls: []string{"message_search", "message_update"},
			ExpectedToolCallCounts: map[string]int{
				"message_update": 1,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: toolResultEventName("message_search"), BodyFragment: `회의실은 3층입니다.`, Count: 1},
				{Name: toolRequestedEventName("message_update"), BodyFragment: `"messageID":"virtual-platform-message-001"`, Count: 1},
				{Name: toolRequestedEventName("message_update"), BodyFragment: `"oldText":"오후 5시"`, Count: 1},
				{Name: toolRequestedEventName("message_update"), BodyFragment: `"newText":"오후 6시"`, Count: 1},
				{Name: toolResultEventName("message_update"), BodyFragment: `"messageUpdated":true`, Count: 1},
			},
			ForbiddenEvents:        []string{agentcontract.TaskEventConfirmationRequested, agentcontract.TaskEventApprovalHoldOpened},
			ExpectedReplyFragments: []string{"수정했습니다"},
			ExpectedTaskStatus:     task.TaskStatusCompleted,
		}},
	}
}

func officeSkill() agentcontract.SkillInstruction {
	return workspaceSkillInstruction("office")
}
