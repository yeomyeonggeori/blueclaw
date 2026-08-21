package e2e

import (
	"fmt"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"os"
	"path/filepath"
	"runtime"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
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

// ScenarioSkillNames are the workspace skills these scenarios drive. Blueclaw
// ships the ones that need nothing beyond its own kernel; the rest belong to the
// appliance whose capability tools they call, so a standalone checkout can only
// find some of them.
var ScenarioSkillNames = []string{"presentation", "scheduled-task", "calendar", "internkim-flow", "website"}

func rootWorkspaceSkillDirectoryPath(skillName string) string {
	skillDirectoryPath := findWorkspaceSkillDirectory(skillName)
	if skillDirectoryPath == "" {
		panic(fmt.Errorf("workspace skill %q is not bundled here or beside this checkout", skillName))
	}
	return skillDirectoryPath
}

// findWorkspaceSkillDirectory looks in Blueclaw's own bundle first, then walks
// up toward a consumer that ships the capability-backed skills, rather than
// assuming Blueclaw sits at a fixed depth beneath it.
func findWorkspaceSkillDirectory(skillName string) string {
	_, sourceFilePath, _, _ := runtime.Caller(0)
	directoryPath := filepath.Dir(filepath.Dir(filepath.Dir(sourceFilePath)))
	for range 5 {
		candidatePath := filepath.Join(directoryPath, "assets", "blueclaw-workspace", "skills", skillName)
		if information, errorValue := os.Stat(candidatePath); errorValue == nil && information.IsDir() {
			return candidatePath
		}
		parentPath := filepath.Dir(directoryPath)
		if parentPath == directoryPath {
			break
		}
		directoryPath = parentPath
	}
	return ""
}

func MissingScenarioSkills() []string {
	missing := []string{}
	for _, skillName := range ScenarioSkillNames {
		if findWorkspaceSkillDirectory(skillName) == "" {
			missing = append(missing, skillName)
		}
	}
	return missing
}

func completionJudgeSatisfiedResponse() string {
	return `{"satisfied":true,"missingWork":[],"reason":"요청한 작업 결과가 모두 기록되었습니다"}`
}

func PresentationLocalMultiturnSuccessScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "presentation_local_multiturn_success",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agentcontract.SkillInstruction{presentationSkill()},
		AllowedTools:          []string{"conversation_history", "memory_search", "terminal_run", "file_write", "file_deliver"},
		Turns: []VirtualTurn{{
			Prompt:                 "너 뭐 할 수 있는지 8장 피피티 만들어서 보내줘봐",
			ExpectedSelectedSkills: []string{"presentation"},
			ExpectedToolCalls:      []string{"terminal_run", "file_deliver"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.terminal_run.requested", BodyFragment: "NAME=", Count: 1},
				{Name: "tool.terminal_run.requested", BodyFragment: "/workspace/skills/presentation/scripts/build.sh", Count: 1},
				{Name: "tool.terminal_run.result", BodyFragment: "Building requested formats", Count: 1},
				{Name: "tool.terminal_run.result", BodyFragment: "Slide render review", Count: 1},
				{Name: "tool.file_deliver.result", BodyFragment: `"output"`, Count: 1},
			},
			ExpectedValidityReviewPassed: true,
			ExpectedAttachments:          []string{".pptx", ".pdf", ".html", "-notes.txt"},
			ExpectedWorkspaceFiles: []VirtualWorkspaceFileExpectation{
				{
					PathGlob:          "circles/staff/tmp/*/DESIGN.md",
					ContainsFragments: []string{"colors:", "Visual direction"},
				},
				{
					PathGlob:           "circles/staff/tmp/*/presentation.md",
					ContainsFragments:  []string{"design-source: DESIGN.md", "InternKim capability deck", "너 뭐 할 수 있는지"},
					ForbiddenFragments: []string{"Draft a presentation deck", "user_request:"},
				},
				{
					PathGlob:          "circles/staff/tmp/*/review/slide-review.json",
					ContainsFragments: []string{`"passed": true`, `"safeMargin": true`, `"edgeOverflow": true`, `"contactSheets"`},
				},
				{
					PathGlob:          "circles/staff/tmp/*/*.html",
					ContainsFragments: []string{"Paperlogy", "Freesentation", "--background", "InternKim capability deck"},
				},
			},
			ForbiddenReplyFragments: []string{
				"PPT 못",
				"PPT 파일을 직접 생성할 수",
				"credentials",
				"자격 증명",
			},
		}},
	}
}

func MemoryGuidedFollowupScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "memory_guided_followup",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Turns: []VirtualTurn{
			{
				Prompt:          "내 발표 자료는 항상 짧은 문장과 한국어 제목을 선호한다고 기억해줘",
				RouterTaskShape: agentcontract.TaskShapeImmediateReply,
				ActionResponses: []string{
					actionFinishMessage("기억해둘게요."),
				},
				ExpectedReplyFragments: []string{"기억"},
			},
			{
				Prompt:          "아까 말한 선호를 반영해서 다음 발표 스타일을 한 문장으로 정리해줘",
				RouterTaskShape: agentcontract.TaskShapeImmediateReply,
				ActionResponses: []string{
					actionFinishMessage("짧은 문장과 한국어 제목 중심으로 정리하겠습니다."),
				},
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
			ExpectedReplyFragments: []string{"결정사항", "담당자", "기한"},
		}},
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
				actionFinishMessage("검색 결과 BlueclawSearchStubToken 정보를 확인했습니다.", "obs-001:web_search:0"),
			},
			ExpectedToolCalls:      []string{"web_search"},
			ExpectedSequence:       []string{"tool.web_search.requested", "tool.web_search.result"},
			ForbiddenEvents:        []string{"agent.no_progress_loop_stopped"},
			ExpectedReplyFragments: []string{"BlueclawSearchStubToken"},
			ExpectedTaskStatus:     task.TaskStatusCompleted,
		}},
	}
}

func ToolPermissionHidesSkillScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "tool_permission_hides_skill",
		ArtifactDirectoryPath: artifactDirectoryPath,
		Skills:                []agentcontract.SkillInstruction{presentationSkill()},
		AllowedTools:          []string{"memory_search", "file_write"},
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
		AllowedTools:          []string{"file_write", "file_deliver"},
		InitialToolNames:      []string{"file_write", "file_deliver"},
		Turns: []VirtualTurn{{
			Prompt:                 "고객지원 FAQ 개편 작업용 JSON 메모 파일을 만들어줘. 제목은 'FAQ 개편', 담당은 '고객지원팀', 상태는 '검토 중'으로 적고 잘 저장됐는지 확인한 다음 완성된 파일을 이 DM에 첨부해줘.",
			RouterRequiredEvidence: []string{"file_write", "file_deliver"},
			ActionResponses: []string{
				actionCallTool("file_write", `{"path":"work/customer-support/faq-revision.json","content":"{\"title\":\"FAQ 개편\",\"owner\":\"고객지원팀\",\"status\":\"검토 중\"}\n"}`),
				actionCallTool("file_deliver", `{"path":"work/customer-support/faq-revision.json"}`),
				actionFinishMessage("JSON 메모 파일을 생성하고 첨부해 저장 결과를 확인했습니다.", "obs-002:file_deliver:0"),
			},
			CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
			ExpectedToolCalls:        []string{"file_write", "file_deliver"},
			ExpectedToolCallCounts:   map[string]int{"file_write": 1, "file_deliver": 1},
			ExpectedAttachmentFiles: []VirtualAttachmentFileExpectation{{
				Suffix:            ".json",
				ContainsFragments: []string{"FAQ 개편", "고객지원팀", "검토 중"},
			}},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.file_write.requested", BodyFragment: "FAQ 개편", Count: 1},
				{Name: "tool.file_write.requested", BodyFragment: "고객지원팀", Count: 1},
				{Name: "tool.file_write.requested", BodyFragment: "검토 중", Count: 1},
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

func DocumentCreateAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "document_create_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search", "terminal_run", "document_read", "file_write", "file_deliver"},
		CapabilityToolNames:   []string{"document_read"},
		InitialToolNames:      []string{"terminal_run", "document_read", "file_write", "file_deliver"},
		Turns: []VirtualTurn{{
			Prompt:                 "운영팀과 재무팀이 함께 검토할 '분기 결산 운영 검토'라는 짧은 DOCX 문서를 작성해서 이 DM에 첨부해줘. 검토 목적과 다음 단계를 간단히 적고, 현재 상태는 초안, 담당은 운영팀이라고 표시해줘.",
			ExpectedSelectedSkills: []string{"document"},
			ExpectedToolCalls:      []string{"file_write", "terminal_run", "file_deliver"},
			ExpectedToolCallCounts: map[string]int{"file_deliver": 1},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.file_deliver.result", BodyFragment: ".docx", Count: 1},
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
		MessageID:   "root-message",
		Filename:    "mascot.png",
		ContentType: "image/png",
		SizeBytes:   13,
	}
	return VirtualSessionScenario{
		Name:                  "attachment_material_read",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search", "terminal_run", "image_read", "document_read"},
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
				actionCallTool("image_read", `{"path":"/workspace/circles/staff/inbox/virtual/virtual-conversation-1/virtual-message-001/mascot.png"}`),
				actionFinishMessage("이미지를 확인했습니다.", "obs-001:image_read:0"),
			},
			ExpectedToolCalls:      []string{"image_read"},
			ExpectedToolCallCounts: map[string]int{"terminal_run": 0},
			ExpectedExposedTools:   []string{"image_read", "document_read"},
			ExpectedModelContexts: []string{
				"materialID=mattermost:file-1",
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
		MessageID:   "message-html",
		Filename:    "kim-intern-automation.html",
		ContentType: "text/html",
		SizeBytes:   691000,
	}
	return VirtualSessionScenario{
		Name:                  "attachment_html_preview_recovery",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search", "terminal_run", "file_preview", "file_read", "image_read"},
		Turns: []VirtualTurn{{
			Prompt:           "이거 파일 내용 보고 어떻게 개선하면 좋을지 말해줘봐",
			RouterTaskShape:  agentcontract.TaskShapeResearchTask,
			InputAttachments: []connectors.InputAttachment{attachment},
			ActionResponses: []string{
				actionCallTool("file_preview", `{"path":"home/inbox/mattermost/thread-1/kim-intern-automation.html"}`),
				actionFinishMessage("첨부 HTML을 확인했습니다. 자동화 섹션의 정보 구조와 CTA를 더 선명하게 다듬으면 좋겠습니다.", "obs-001:file_preview:0"),
			},
			ExpectedToolCalls: []string{"file_preview"},
			ExpectedToolCallCounts: map[string]int{
				"terminal_run": 0,
				"file_read":    0,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.file_preview.requested", BodyFragment: `"path":"home/inbox/mattermost/thread-1/kim-intern-automation.html"`, Count: 1},
				{Name: "tool.file_preview.result", BodyFragment: "Virtual HTML Title", Count: 1},
			},
			ExpectedModelContexts: []string{
				"materialID=mattermost:file-html",
				"availableTools=file_preview,file_read",
			},
			ExpectedReplyFragments: []string{"첨부 HTML", "정보 구조"},
		}},
	}
}

func AttachmentHTMLPreviousPreviewRecoveryScenario(artifactDirectoryPath string) VirtualSessionScenario {
	attachment := connectors.InputAttachment{
		Platform:    "mattermost",
		FileID:      "file-html",
		MessageID:   "root-message",
		Filename:    "kim-intern-automation.html",
		ContentType: "text/html",
		SizeBytes:   691000,
	}
	return VirtualSessionScenario{
		Name:                  "attachment_html_previous_preview_recovery",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search", "terminal_run", "file_preview", "file_read", "image_read"},
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
				actionCallTool("file_preview", `{"path":"home/inbox/mattermost/thread-1/kim-intern-automation.html"}`),
				actionFinishMessage("이전 첨부 HTML을 확인했습니다. 자동화 흐름의 핵심 CTA와 섹션 우선순위를 더 명확히 잡으면 좋겠습니다.", "obs-001:file_preview:0"),
			},
			ExpectedToolCalls: []string{"file_preview"},
			ExpectedToolCallCounts: map[string]int{
				"terminal_run": 0,
				"file_read":    0,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.file_preview.requested", BodyFragment: `"path":"home/inbox/mattermost/thread-1/kim-intern-automation.html"`, Count: 1},
				{Name: "tool.file_preview.result", BodyFragment: "Virtual HTML Title", Count: 1},
			},
			ExpectedModelContexts: []string{
				"Previous attachments:",
				"materialID=mattermost:file-html",
				"availableTools=file_preview,file_read",
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
		MessageID:   "virtual-message-001",
		Filename:    "mascot.png",
		ContentType: "image/png",
		SizeBytes:   13,
	}
	return VirtualSessionScenario{
		Name:                  "attachment_current_image_input",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search", "terminal_run", "image_read", "document_read"},
		CapabilityToolNames:   []string{"image_read", "document_read"},
		Turns: []VirtualTurn{{
			Prompt:           "이거 보여? 묘사 좀 자세히 해봐.",
			RouterTaskShape:  agentcontract.TaskShapeImmediateReply,
			InputAttachments: []connectors.InputAttachment{attachment},
			ActionResponses: []string{
				actionFinishWithReplyPart(
					"이미지를 상세하게 설명드렸습니다.",
					"이미지에는 흰색 고양이 형태의 김인턴 마스코트 인형이 서 있습니다. 얼굴에는 검은색으로 윙크하는 눈과 동그란 눈, 작은 입 모양이 붙어 있고, 목에는 '김인턴'이라고 적힌 이름표가 걸려 있습니다. 흰 셔츠와 청바지, 운동화를 착용했고 검은 가방끈과 꼬리가 보여 캐릭터 상품처럼 연출된 사진입니다.",
				),
			},
			ExpectedToolCallCounts: map[string]int{
				"image_read":    0,
				"terminal_run":  0,
				"document_read": 0,
			},
			ExpectedModelContexts: []string{
				"materialID=mattermost:file-current",
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
				"materialID=mattermost:file-code-shot",
				"login_handler.png",
			},
			ExpectedReplyFragments: []string{"리팩터링", "비밀번호"},
			MinimumReplyLength:     80,
			ExpectedTaskStatus:     task.TaskStatusCompleted,
		}},
	}
}

func GWSDisabledScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "gws_disabled",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"memory_search", "terminal_run", "file_write"},
		Turns: []VirtualTurn{{
			Prompt:          "구글 드라이브에 파일 올릴 수 있는지 확인해줘",
			RouterTaskShape: agentcontract.TaskShapeImmediateReply,
			ActionResponses: []string{
				actionFinishMessage("Google Workspace 도구는 현재 사용할 수 없습니다. 로컬 파일 작업은 가능합니다."),
			},
			ForbiddenModelContexts: []string{"google.drive.import_pptx"},
			ExpectedReplyFragments: []string{"사용할 수 없습니다"},
		}},
	}
}

func ScheduleCreateAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   "schedule_create_acceptance",
		SkillSearchQueries:     []string{"schedule a recurring interval reminder"},
		ArtifactDirectoryPath:  artifactDirectoryPath,
		Skills:                 []agentcontract.SkillInstruction{scheduledTaskSkill()},
		AllowedTools:           append(toolcontract.KernelToolNames(), "schedule_create", "schedule_cancel"),
		InitialToolNames:       []string{"schedule_create", "schedule_cancel"},
		RouterRequiredEvidence: []string{"schedule_create"},
		Turns: []VirtualTurn{{
			Prompt: "1분마다 \"1분 지났습니다\"라고 보내줘",
			ActionResponses: []string{
				actionInvokeCapabilityTool("schedule_create", `{"name":"1분 알림","taskInstruction":"현재 대화에 \"1분 지났습니다\"라고 보낸다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"repeatPolicy":"finite","timeZone":"Asia/Seoul"}`),
				actionFinishMessage("1분마다 알림을 보내도록 예약해둘게요.", "obs-001:schedule_create:0"),
			},
			CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
			ExpectedSelectedSkills:   []string{"scheduled-task"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.schedule_create.requested", BodyFragment: "schedule_create", Count: 1},
				{Name: "tool.schedule_create.result", BodyFragment: "intervalSecond", Count: 1},
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
		AllowedTools:          append(toolcontract.KernelToolNames(), "schedule_create", "schedule_update", "schedule_cancel"),
		InitialToolNames:      []string{"schedule_create", "schedule_update", "schedule_cancel"},
		Turns: []VirtualTurn{
			{
				Prompt:                 "30분마다 상태 확인하라고 알려줘. 세 번만 해줘",
				RouterRequiredEvidence: []string{"schedule_create"},
				ActionResponses: []string{
					actionInvokeCapabilityTool("schedule_create", `{"name":"상태 확인 알림","taskInstruction":"현재 대화에 \"상태를 확인하세요\"라고 보낸다.","kind":"interval","intervalSecond":1800,"maxRunCount":3,"repeatPolicy":"finite","timeZone":"Asia/Seoul"}`),
					actionFinishMessage("30분마다 세 번 상태 확인 알림을 보내도록 예약해둘게요.", "obs-001:schedule_create:0"),
				},
				CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
				ExpectedSelectedSkills:   []string{"scheduled-task"},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.schedule_create.requested", BodyFragment: "schedule_create", Count: 1},
					{Name: "tool.schedule_create.result", BodyFragment: "intervalSecond", Count: 1},
				},
				ExpectedModelContexts: []string{"schedule_create", "taskInstruction", "30분마다"},
			},
			{
				Prompt:                 "그 예약을 1시간마다 다섯 번으로 바꿔줘",
				RouterRequiredEvidence: []string{"schedule_update"},
				ActionResponses: []string{
					actionInvokeCapabilityTool("schedule_update", `{"scheduleID":"virtual-schedule-001","intervalSecond":3600,"maxRunCount":5,"repeatPolicy":"finite"}`),
					actionFinishMessage("예약을 1시간마다 다섯 번으로 수정했습니다.", "obs-001:schedule_update:0"),
				},
				CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.schedule_update.requested", BodyFragment: "schedule_update", Count: 1},
					{Name: "tool.schedule_update.result", BodyFragment: "intervalSecond", Count: 1},
				},
			},
			{
				Prompt:                 "그 예약 삭제해줘",
				RouterRequiredEvidence: []string{"schedule_cancel"},
				ActionResponses: []string{
					actionInvokeCapabilityTool("schedule_cancel", `{"scope":"mine"}`),
					actionFinishMessage("예약을 삭제했습니다.", "obs-001:schedule_cancel:0"),
				},
				CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.schedule_cancel.requested", BodyFragment: "schedule_cancel", Count: 1},
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
		AllowedTools:          append(toolcontract.KernelToolNames(), "calendar_add", "calendar_update", "calendar_delete"),
		CapabilityToolNames:   []string{"calendar_add", "calendar_update", "calendar_delete"},
		InitialToolNames:      []string{"calendar_add"},
		Turns: []VirtualTurn{
			{
				Prompt:                 "내일 오전 10시에 제품 회고 일정을 캘린더에 추가해줘",
				RouterRequiredEvidence: []string{"calendar_add"},
				ActionResponses: []string{
					actionInvokeCapabilityTool("calendar_add", `{"title":"제품 회고","startISO":"2026-06-13T10:00:00+09:00","endISO":"2026-06-13T11:00:00+09:00","timeZone":"Asia/Seoul"}`),
					actionFinishMessage("내일 오전 10시에 제품 회고 일정을 추가했습니다.", "obs-001:calendar_add:0"),
				},
				CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
				ExpectedSelectedSkills:   []string{"calendar"},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.calendar_add.requested", BodyFragment: "calendar_add", Count: 1},
				},
			},
			{
				Prompt:                 "그 일정을 내일 오후 2시로 바꿔줘",
				RouterRequiredEvidence: []string{"calendar_update"},
				ActionResponses: []string{
					actionCallTool("calendar_update", `{"eventHint":"calendar-event-001","title":"제품 회고","startISO":"2026-06-13T14:00:00+09:00","endISO":"2026-06-13T15:00:00+09:00","timeZone":"Asia/Seoul"}`),
					actionFinishMessage("제품 회고 일정을 내일 오후 2시로 변경했습니다.", "obs-001:calendar_update:0"),
				},
				CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.calendar_update.requested", BodyFragment: "calendar_update", Count: 1},
					{Name: "tool.calendar_update.requested", BodyFragment: "2026-06-13T14:00:00+09:00", Count: 1},
					{Name: "tool.calendar_update.result", BodyFragment: "updated virtual calendar event", Count: 1},
				},
			},
			{
				Prompt:                 "그 일정 삭제해줘",
				RouterRequiredEvidence: []string{"calendar_delete"},
				ActionResponses: []string{
					actionInvokeCapabilityTool("calendar_delete", `{"eventHint":"calendar-event-001"}`),
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.calendar_delete.requested", BodyFragment: "calendar_delete", Count: 1},
					{Name: "approval.pending_call", BodyFragment: `"calendar_delete"`, Count: 1},
				},
				ExpectedEvents:     []string{"confirmation.requested"},
				ExpectedTaskStatus: task.TaskStatusWaitingApproval,
			},
			{
				Prompt:         "확인",
				RouterApproval: "approve",
				ActionResponses: []string{
					actionFinishMessage("제품 회고 일정을 삭제했습니다.", "obs-002:calendar_delete:0"),
				},
				CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "approval.executed", BodyFragment: `"calendar_delete"`, Count: 1},
				},
				ExpectedEvents:         []string{"confirmation.reply_classified"},
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
		AllowedTools:          []string{"conversation_history", "memory_search", "calendar_add"},
		CapabilityToolNames:   []string{"calendar_add"},
		InitialToolNames:      []string{"calendar_add"},
		Turns: []VirtualTurn{{
			Prompt:                 "7월 13일에 샨보장 미팅을 오전 10시부터 11시까지 등록해줘",
			RouterRequiredEvidence: []string{"calendar_add"},
			ActionResponses: []string{
				actionFinishMessage("7월 13일 미팅을 오전 10시~11시로 등록했습니다."),
				actionInvokeCapabilityTool("calendar_add", `{"title":"샨보장 미팅","startISO":"2026-07-13T10:00:00+09:00","endISO":"2026-07-13T11:00:00+09:00","timeZone":"Asia/Seoul"}`),
				actionFinishMessage("7월 13일 미팅을 오전 10시~11시로 등록했습니다.", "obs-002:calendar_add:0"),
			},
			ExpectedSelectedSkills: []string{"calendar"},
			ExpectedToolCalls:      []string{"calendar_add"},
			ExpectedToolCallCounts: map[string]int{
				"calendar_add": 1,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "agent.evidence_missing", BodyFragment: "calendar_add", Count: 1},
				{Name: "agent.completion_required", BodyFragment: "calendar_add", Count: 1},
				{Name: "tool.calendar_add.requested", BodyFragment: "2026-07-13T10:00:00+09:00", Count: 1},
			},
			ExpectedReplyFragments: []string{"등록했습니다"},
			ForbiddenEvents:        []string{"agent.no_progress_loop_stopped"},
		}},
	}
}

func AmbientDutyCalendarAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   "ambient_duty_calendar_acceptance",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RouterRequiredEvidence: []string{"calendar_add"},
		AddressingResponse:     `{"target":"human","shouldRespond":false,"dutyMatch":true,"dutyName":"calendar_upkeep","dutyConfidence":0.93}`,
		Skills:                 []agentcontract.SkillInstruction{calendarSkill()},
		AllowedTools:           []string{"conversation_history", "memory_search", "calendar_add"},
		CapabilityToolNames:    []string{"calendar_add"},
		InitialToolNames:       []string{"calendar_add"},
		Turns: []VirtualTurn{{
			Prompt:           "@박예시 님 오늘 오후 5시 정기회의에 최견본, 이샘플 님도 참석자로 추가해주세요",
			ExpectedResponse: VirtualResponseBackgroundAction,
			ConversationType: "channel",
			ChannelID:        "town-square",
			ChannelName:      "town-square",
			ReplyTargetID:    "virtual-message-001",
			Addressing:       connectors.AddressingMetadata{},
			ActionResponses: []string{
				actionInvokeCapabilityTool("calendar_add", `{"title":"정기회의","startISO":"2026-06-12T17:00:00+09:00","endISO":"2026-06-12T18:00:00+09:00","timeZone":"Asia/Seoul","people":["최견본","이샘플"]}`),
				actionFinishMessage("정기회의 일정을 추가했습니다.", "obs-001:calendar_add:0"),
			},
			CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
			ExpectedSelectedSkills:   []string{"calendar"},
			ExpectedToolCalls:        []string{"calendar_add"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "agent.ambient_duty_launch", BodyFragment: `"dutyName":"calendar_upkeep"`, Count: 1},
				{Name: "tool.calendar_add.requested", BodyFragment: "2026-06-12T17:00:00+09:00", Count: 1},
				{Name: "tool.calendar_add.requested", BodyFragment: "최견본", Count: 1},
				{Name: "tool.calendar_add.requested", BodyFragment: "이샘플", Count: 1},
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
		RouterRequiredEvidence: []string{"calendar_add"},
		AddressingResponse:     `{"target":"human","shouldRespond":false,"dutyMatch":true,"dutyName":"calendar_upkeep","dutyConfidence":0.92}`,
		Skills:                 []agentcontract.SkillInstruction{calendarSkill(), mattermostSkill()},
		AllowedTools:           []string{"conversation_history", "memory_search", "calendar_add", "message_send"},
		CapabilityToolNames:    []string{"calendar_add", "message_send"},
		InitialToolNames:       []string{"calendar_add"},
		Turns: []VirtualTurn{{
			Prompt:           announcement,
			ExpectedResponse: VirtualResponseBackgroundAction,
			ConversationType: "channel",
			ChannelID:        "town-square",
			ChannelName:      "town-square",
			ReplyTargetID:    "virtual-message-001",
			Addressing:       connectors.AddressingMetadata{},
			ActionResponses: []string{
				actionInvokeCapabilityTool("calendar_add", `{"title":"라운지 촬영","startISO":"2026-09-02T07:00:00+09:00","endISO":"2026-09-02T10:00:00+09:00","timeZone":"Asia/Seoul"}`),
				actionFinishMessage("촬영 일정을 캘린더에 기록했습니다.", "obs-001:calendar_add:0"),
			},
			CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
			ExpectedToolCalls:        []string{"calendar_add"},
			ExpectedToolCallCounts:   map[string]int{"message_send": 0},
			ForbiddenEvents:          []string{"tool.message_send.requested"},
			ForbiddenModelContexts:   []string{"message_send"},
			ExpectedModelContexts: []string{
				"Ambient duty context",
				"Overheard message from",
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "agent.ambient_duty_launch", BodyFragment: `"dutyName":"calendar_upkeep"`, Count: 1},
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
		AllowedTools:          []string{"conversation_history", "memory_search", "calendar_add"},
		CapabilityToolNames:   []string{"calendar_add"},
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
			ExpectedToolCallCounts: map[string]int{"calendar_add": 0},
			ExpectedTaskStatus:     task.TaskStatusCompleted,
		}},
	}
}

func flowTaskSkill() agentcontract.SkillInstruction {
	return workspaceSkillInstruction("internkim-flow")
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
				actionInvokeCapabilityTool("task_add", `{"title":"신규 가입 플로우 점검","targetPersonHint":"예시"}`),
				actionFinishMessage("예시 님 업무로 추가했습니다.", "obs-001:task_add:0"),
			},
			CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
			ExpectedToolCalls:        []string{"task_add"},
			ExpectedToolCallCounts: map[string]int{
				"task_add":     1,
				"terminal_run": 0,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "agent.ambient_duty_launch", BodyFragment: `"dutyName":"team_flow_update"`, Count: 1},
				{Name: "tool.task_add.requested", BodyFragment: "예시", Count: 1},
				{Name: "tool.task_add.result", BodyFragment: `"ownerName":"예시"`, Count: 1},
				{Name: "tool.task_add.result", BodyFragment: `"effect":"created"`, Count: 1},
			},
			ExpectedTaskStatus: task.TaskStatusCompleted,
			ExpectedModelContexts: []string{
				"Ambient duty context",
				"Overheard message from",
			},
			ForbiddenEvents: []string{"tool.terminal_run.requested"},
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
				actionInvokeCapabilityTool("task_update", `{"taskHint":"task-1","endDate":"2026-06-24"}`),
				actionFinishMessage("예시 님 업무 마감을 수요일로 변경했습니다.", "obs-001:task_update:0"),
			},
			CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
			ExpectedToolCalls:        []string{"task_update"},
			ExpectedToolCallCounts: map[string]int{
				"task_add":    0,
				"task_update": 1,
			},
			ExpectedTaskStatus: task.TaskStatusCompleted,
		}},
	}
}

func CompletionJudgeRecoveryAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "completion_judge_recovery_acceptance",
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
				actionFinishMessage("업무를 추가했습니다.", "obs-001:task_add:0"),
				actionInvokeCapabilityTool("task_update", `{"taskHint":"task-1","endDate":"2026-07-24"}`),
				actionFinishMessage("마감일을 포함해 업무를 추가했습니다.", "obs-003:task_update:0"),
			},
			CompletionJudgeResponses: []string{
				`{"satisfied":false,"missingWork":["마감일(endDate)이 누락되었습니다"],"reason":"업무에 요청된 마감일이 기록되지 않았습니다"}`,
				`{"satisfied":true,"missingWork":[],"reason":"요청한 제목과 마감일이 모두 기록되었습니다"}`,
			},
			ExpectedToolCalls: []string{"task_add", "task_update"},
			ExpectedToolCallCounts: map[string]int{
				"task_add":    1,
				"task_update": 1,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "completion_judge.verdict", BodyFragment: `"satisfied":false`, Count: 1},
				{Name: "completion_judge.verdict", BodyFragment: `"satisfied":true`, Count: 1},
				{Name: "agent.evidence_missing", BodyFragment: "마감일", Count: 1},
				{Name: "agent.completion_required", BodyFragment: "마감일", Count: 1},
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
					actionFinishMessage("memo-helper skill을 등록했습니다.", "obs-001:skill_add:0"),
				},
				CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
				ExpectedToolCalls:        []string{"skill_add"},
				ExpectedToolCallCounts: map[string]int{
					"skill_add":    1,
					"skill_remove": 0,
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.skill_add.result", BodyFragment: "created", Count: 1},
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
					actionFinishMessage("memo-helper skill을 삭제했습니다.", "obs-001:skill_remove:0"),
				},
				CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
				ExpectedToolCalls:        []string{"skill_remove"},
				ExpectedToolCallCounts: map[string]int{
					"skill_add":    0,
					"skill_remove": 1,
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.skill_remove.result", BodyFragment: "removed", Count: 1},
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
		Skills:                []agentcontract.SkillInstruction{presentationSkill(), scheduledTaskSkill(), sitePrototypeSkill()},
		AllowedTools:          []string{"conversation_history", "memory_search", "skill_search"},
		Turns: []VirtualTurn{{
			Prompt:          "너는 무엇을 할 수 있어?",
			RouterTaskShape: agentcontract.TaskShapeResearchTask,
			ActionResponses: []string{
				actionCallTool("skill_search", `{}`),
				actionFinishMessage("사용 가능한 skill에는 presentation, scheduled-task, site-prototype이 있습니다.", "obs-001:skill_search:0"),
			},
			ExpectedToolCalls: []string{"skill_search"},
			ExpectedToolCallCounts: map[string]int{
				"skill_search": 1,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.skill_search.result", BodyFragment: "presentation", Count: 1},
			},
			ExpectedReplyFragments: []string{"presentation"},
		}},
	}
}

func TaskHistoryQuestionAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "task_history_question_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search"},
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
					actionCallTool("conversation_history", `{"limit":20}`),
					actionFinishMessage("최근에는 계약서 확인 요약 작업을 완료했습니다.", "obs-001:conversation_history:0"),
				},
				ExpectedToolCalls: []string{"conversation_history"},
				ExpectedToolCallCounts: map[string]int{
					"conversation_history": 1,
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.conversation_history.result", BodyFragment: "계약서 확인 요약 작업", Count: 1},
				},
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
					actionFinishMessage("Remembered: your preferred language is Korean.", "obs-001:memory_remember:0"),
				},
				CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
				ExpectedToolCalls:        []string{"memory_remember"},
				ExpectedToolCallCounts: map[string]int{
					"memory_remember": 1,
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.memory_remember.requested", BodyFragment: "Korean", Count: 1},
				},
				ExpectedReplyFragments: []string{"Korean"},
			},
			{
				Prompt:                 "What language do I prefer?",
				RouterTaskShape:        agentcontract.TaskShapeResearchTask,
				RouterRequiredEvidence: []string{"memory_search"},
				ActionResponses: []string{
					actionCallTool("memory_search", `{"query":"preferred language"}`),
					actionFinishMessage("Your preferred language is Korean.", "obs-001:memory_search:0"),
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

func FailureExplanationAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "failure_explanation_acceptance",
		ArtifactDirectoryPath: artifactDirectoryPath,
		AllowedTools:          []string{"conversation_history", "memory_search", "terminal_run"},
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
				RouterRequiredEvidence: []string{"terminal_run"},
				ActionResponses: []string{
					actionCallTool("terminal_run", `{"command":"printf 'permission denied blocked_by_captcha' >&2; exit 126","workingDirectoryPath":"~","timeoutSecond":30}`),
					actionFailMessage("terminal_run: permission denied"),
				},
				ExpectedSequence: []string{"tool.terminal_run.requested", "tool.terminal_run.result"},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.terminal_run.result", BodyFragment: "permission denied", Count: 1},
				},
				ExpectedTaskStatus: task.TaskStatusFailed,
			},
			{
				Prompt:          "왜 실패했어?",
				RouterTaskShape: agentcontract.TaskShapeResearchTask,
				ActionResponses: []string{
					actionCallTool("conversation_history", `{"limit":20}`),
					actionFinishMessage("terminal_run 실행이 permission denied 때문에 실패했습니다.", "obs-001:conversation_history:0"),
				},
				ExpectedToolCalls: []string{"conversation_history"},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.conversation_history.result", BodyFragment: "permission denied", Count: 1},
				},
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
		Turns: []VirtualTurn{{
			Prompt:                 "2027년 1월 15일 오전 9시에 계약서 확인 알림을 한 번만 예약해줘",
			RouterRequiredEvidence: []string{"schedule_create"},
			ActionResponses: []string{
				actionInvokeCapabilityTool("schedule_create", `{"name":"계약서 확인 알림","taskInstruction":"현재 대화에 \"계약서를 확인하세요\"라고 보낸다.","kind":"once","runAt":"2027-01-15T00:00:00Z","timeZone":"Asia/Seoul"}`),
				actionFinishMessage("2027년 1월 15일 오전 9시에 한 번 알림을 보내도록 예약해둘게요.", "obs-001:schedule_create:0"),
			},
			CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
			ExpectedSelectedSkills:   []string{"scheduled-task"},
			ExpectedToolCalls:        []string{"schedule_create"},
			ExpectedModelContexts:    []string{"schedule_create", "runAt", "once"},
			ExpectedReplyFragments:   []string{"2027년 1월 15일", "한 번"},
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

func mattermostSkill() agentcontract.SkillInstruction {
	return workspaceSkillInstruction("mattermost")
}

func scheduledTaskSkill() agentcontract.SkillInstruction {
	return workspaceSkillInstruction("scheduled-task")
}

func SitePrototypeAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   "site_artifact_acceptance",
		SkillSearchQueries:     []string{"create and publish a website prototype"},
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RouterRequiredEvidence: []string{"site_serve"},
		RouterSiteEvidence:     "Local Fleet Studio",
		Skills:                 []agentcontract.SkillInstruction{sitePrototypeSkill()},
		AllowedTools:           append(toolcontract.KernelToolNames(), sitePrototypeCapabilityToolNames()...),
		CapabilityToolNames:    sitePrototypeCapabilityToolNames(),
		InitialToolNames:       []string{"file_write", "site_serve"},
		Turns: []VirtualTurn{{
			Prompt: "테스트용 'Local Fleet Studio' 단일 페이지 소개 웹사이트를 만들어서 배포해줘. 첫 화면 제목은 'Local Fleet Studio', 보조 문구는 '로컬 플릿 웹사이트 생성 배포 테스트', 섹션은 서비스 소개, 장점 3개, 문의 CTA만 넣어줘. 추가 질문하지 말고 합리적인 기본값으로 진행해줘.",
			ActionResponses: []string{
				actionCallTool("file_write", `{"path":"/workspace/circles/staff/sites/local-fleet-studio/draft/app/public/site-content.json","content":"{\"siteName\":\"Local Fleet Studio\",\"tagline\":\"로컬 플릿 웹사이트 생성 배포 테스트\",\"blocks\":[{\"variant\":\"hero\",\"title\":\"Local Fleet Studio\",\"body\":\"로컬 플릿 웹사이트 생성 배포 테스트\"},{\"variant\":\"prose\",\"title\":\"서비스 소개\",\"body\":\"Local Fleet Studio는 로컬 플릿 환경에서 웹사이트 생성과 배포 과정을 검증하는 테스트 서비스입니다.\"},{\"variant\":\"features\",\"title\":\"장점\",\"items\":[{\"title\":\"빠른 프로토타입\",\"body\":\"빠른 프로토타입 생성\"},{\"title\":\"안전한 검증\",\"body\":\"안전한 배포 검증\"},{\"title\":\"손쉬운 재배포\",\"body\":\"손쉬운 재배포\"}]},{\"variant\":\"cta\",\"title\":\"문의\",\"body\":\"자세한 내용이 궁금하시면 지금 바로 문의해 주세요.\"}]}"}`),
				actionInvokeCapabilityTool("site_serve", `{"title":"Local Fleet Studio","sourceWorkspacePath":"/workspace/circles/staff/sites/local-fleet-studio/draft","mode":"publish"}`),
				actionFinishMessage("Local Fleet Studio 웹사이트 프로토타입을 배포했습니다: https://local-fleet-studio.device.example.test", "obs-002:site_serve:0"),
			},
			CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
			ExpectedSelectedSkills:   []string{"website"},
			ExpectedToolCallCounts:   map[string]int{"terminal_run": 0},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.site_serve.requested", BodyFragment: "site_serve", Count: 1},
				{Name: "tool.site_serve.result", BodyFragment: "device.example.test", Count: 1},
			},
			ExpectedModelContexts:  []string{"site_serve", "Local Fleet Studio"},
			ForbiddenModelContexts: []string{"home/sites/site-1"},
			ExpectedReplyFragments: []string{"https://local-fleet-studio.device.example.test"},
			ForbiddenReplyFragments: []string{
				"죄송",
				"완료하지 못",
				"기능은 제공",
				"오류가 발생",
				"다시 한번",
				"어떤 웹사이트",
				"무슨 웹사이트",
			},
		}},
	}
}

func SiteEditRedeployAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   "site_edit_redeploy_acceptance",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RouterRequiredEvidence: []string{"site_serve"},
		RouterSiteEvidence:     "Local Fleet Studio website",
		Skills:                 []agentcontract.SkillInstruction{sitePrototypeSkill()},
		AllowedTools:           append(toolcontract.KernelToolNames(), sitePrototypeCapabilityToolNames()...),
		CapabilityToolNames:    sitePrototypeCapabilityToolNames(),
		InitialToolNames:       []string{"site_serve", "site_list", "file_write"},
		Turns: []VirtualTurn{
			{
				Prompt: "Build and deploy a single-page Local Fleet Studio website. Use the heading 'Local Fleet Studio' and subtitle 'Local fleet create deploy test'. Include a short service overview and three feature bullets. Do not ask follow-up questions.",
				ActionResponses: []string{
					actionCallTool("file_write", `{"path":"/workspace/circles/staff/sites/local-fleet-studio/draft/app/public/site-content.json","content":"{\"siteName\":\"Local Fleet Studio\",\"tagline\":\"Local fleet create deploy test\",\"blocks\":[{\"variant\":\"hero\",\"title\":\"Local Fleet Studio\",\"body\":\"Local fleet create deploy test\"},{\"variant\":\"prose\",\"title\":\"Overview\",\"body\":\"Local Fleet Studio validates local fleet website creation and deployment.\"},{\"variant\":\"features\",\"title\":\"Features\",\"items\":[{\"title\":\"Fast prototyping\",\"body\":\"Fast prototyping\"},{\"title\":\"Safe verification\",\"body\":\"Safe deploy verification\"},{\"title\":\"Easy redeploys\",\"body\":\"Easy redeploys\"}]}]}"}`),
					actionInvokeCapabilityTool("site_serve", `{"title":"Local Fleet Studio","sourceWorkspacePath":"/workspace/circles/staff/sites/local-fleet-studio/draft","mode":"publish"}`),
					actionFinishMessage("Deployed the Local Fleet Studio site: https://local-fleet-studio.device.example.test", "obs-002:site_serve:0"),
				},
				CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
				ExpectedSelectedSkills:   []string{"website"},
				ExpectedToolCallCounts:   map[string]int{"terminal_run": 0},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.site_serve.requested", BodyFragment: "site_serve", Count: 1},
					{Name: "tool.site_serve.result", BodyFragment: "device.example.test", Count: 1},
				},
				ForbiddenModelContexts: []string{"home/sites/site-1"},
				ExpectedReplyFragments: []string{"https://local-fleet-studio.device.example.test"},
			},
			{
				Prompt: "Update the same Local Fleet Studio website heading to say 'Local Fleet Studio Updated' and add the subtitle 'Redeploy verification passed', then redeploy the same site. Do not create a new site.",
				ActionResponses: []string{
					actionCallTool("site_list", `{}`),
					actionCallTool("file_write", `{"path":"/workspace/circles/staff/sites/local-fleet-studio/draft/app/public/site-content.json","content":"{\"siteName\":\"Local Fleet Studio Updated\",\"tagline\":\"Redeploy verification passed\",\"blocks\":[{\"variant\":\"hero\",\"title\":\"Local Fleet Studio Updated\",\"body\":\"Redeploy verification passed\"}]}"}`),
					actionInvokeCapabilityTool("site_serve", `{"title":"Local Fleet Studio Updated","sourceWorkspacePath":"/workspace/circles/staff/sites/local-fleet-studio/draft","mode":"publish","siteReference":"local-fleet-studio"}`),
					actionFinishMessage("Updated and redeployed the site: https://local-fleet-studio.device.example.test", "obs-002:file_write:0", "obs-003:site_serve:0"),
				},
				CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
				ExpectedToolCallCounts:   map[string]int{"terminal_run": 0},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.site_list.requested", BodyFragment: "site_list", Count: 1},
					{Name: "tool.file_write.requested", BodyFragment: "Local Fleet Studio Updated", Count: 1},
					{Name: "tool.file_write.requested", BodyFragment: "blocks", Count: 1},
					{Name: "tool.site_serve.requested", BodyFragment: "site_serve", Count: 1},
					{Name: "tool.site_serve.result", BodyFragment: "device.example.test", Count: 1},
				},
				ForbiddenModelContexts: []string{"home/sites/site-1"},
				ExpectedReplyFragments: []string{"https://local-fleet-studio.device.example.test"},
			},
		},
	}
}

func SiteCustomStructureAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                   "site_custom_structure_acceptance",
		ArtifactDirectoryPath:  artifactDirectoryPath,
		RouterRequiredEvidence: []string{"site_serve"},
		RouterSiteEvidence:     "Local Fleet Studio",
		Skills:                 []agentcontract.SkillInstruction{sitePrototypeSkill()},
		AllowedTools:           append(toolcontract.KernelToolNames(), sitePrototypeCapabilityToolNames()...),
		CapabilityToolNames:    sitePrototypeCapabilityToolNames(),
		InitialToolNames:       []string{"site_serve", "file_write", "terminal_run"},
		InitialSite: &VirtualSiteFixture{
			SiteID:      "site-1",
			Slug:        "demo",
			Title:       "Local Fleet Studio",
			IsPublished: true,
		},
		Turns: []VirtualTurn{{
			Prompt: "Local Fleet Studio 웹사이트 레이아웃을 두 칼럼 커스텀 구조로 바꿔서 다시 배포해줘.",
			ActionResponses: []string{
				actionCallTool("file_write", `{"path":"/workspace/circles/staff/sites/demo/draft/app/src/App.tsx","content":"export default function App() {\n  return <main className=\"custom-layout\"><section className=\"column\">Local Fleet Studio</section><section className=\"column\">Two-column custom layout</section></main>;\n}\n"}`),
				actionCallTool("site_serve", `{"title":"Local Fleet Studio","sourceWorkspacePath":"/workspace/circles/staff/sites/demo/draft","mode":"publish","siteReference":"demo"}`),
				actionCallTool("terminal_run", `{"command":"mkdir -p dist && printf '<!doctype html><html><body><main class=\"custom-layout\"><section>Local Fleet Studio</section><section>Two-column custom layout</section></main></body></html>' > dist/index.html","workingDirectoryPath":"/workspace/circles/staff/sites/demo/draft/app","timeoutSecond":120}`),
				actionCallTool("site_serve", `{"title":"Local Fleet Studio","sourceWorkspacePath":"/workspace/circles/staff/sites/demo/draft","mode":"publish","siteReference":"demo"}`),
				actionFinishMessage("커스텀 레이아웃을 빌드하고 다시 배포했습니다: https://demo.device.example.test", "obs-005:site_serve:0"),
			},
			CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
			ExpectedToolCallCounts:   map[string]int{"terminal_run": 1, "file_write": 1, "site_serve": 1},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.file_write.requested", BodyFragment: "custom-layout", Count: 1},
				{Name: "tool.terminal_run.requested", BodyFragment: "dist/index.html", Count: 1},
				{Name: "tool.site_serve.result", BodyFragment: "app/dist", Count: 1},
				{Name: "tool.site_serve.result", BodyFragment: "device.example.test", Count: 1},
			},
			ExpectedModelContexts:  []string{"app/dist", "bun scripts/build.ts"},
			ForbiddenModelContexts: []string{"home/sites/site-1"},
			ExpectedReplyFragments: []string{"https://demo.device.example.test"},
			ForbiddenReplyFragments: []string{
				"죄송",
				"완료하지 못",
				"오류가 발생",
			},
		}},
	}
}

func SiteLifecycleAcceptanceScenario(artifactDirectoryPath string) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                      "site_lifecycle_acceptance",
		SkillSearchQueries:        []string{"create and publish a website prototype"},
		ArtifactDirectoryPath:     artifactDirectoryPath,
		RouterSiteEvidence:        "Local Fleet Studio",
		Skills:                    []agentcontract.SkillInstruction{sitePrototypeSkill()},
		AllowedTools:              append(toolcontract.KernelToolNames(), sitePrototypeCapabilityToolNames()...),
		CapabilityToolNames:       sitePrototypeCapabilityToolNames(),
		CapabilityToolDescriptors: []agentruntime.CapabilityToolDescriptor{{Name: "site_unserve", RequiresApproval: true}},
		InitialToolNames:          []string{"site_serve", "site_list", "site_unserve", "file_write", "terminal_run"},
		Turns: []VirtualTurn{
			{
				Prompt: "테스트용 'Local Fleet Studio' 단일 페이지 소개 웹사이트를 만들어서 배포해줘. 첫 화면 제목은 'Local Fleet Studio', 보조 문구는 '로컬 플릿 웹사이트 CRUD 테스트', 섹션은 서비스 소개, 장점 3개, 문의 CTA만 넣어줘. 추가 질문하지 말고 합리적인 기본값으로 진행해줘.",
				RouterRequiredEvidence: []string{
					"site_serve",
				},
				ActionResponses: []string{
					actionCallTool("terminal_run", `{"command":"mkdir -p dist && printf '<!doctype html><html><body><main><h1>Local Fleet Studio</h1><p>로컬 플릿 웹사이트 CRUD 테스트</p></main></body></html>' > dist/index.html","workingDirectoryPath":"/workspace/circles/staff/sites/local-fleet-studio/draft/app","timeoutSecond":120}`),
					actionInvokeCapabilityTool("site_serve", `{"title":"Local Fleet Studio","sourceWorkspacePath":"/workspace/circles/staff/sites/local-fleet-studio/draft","mode":"publish"}`),
					actionFinishMessage("Local Fleet Studio 웹사이트를 배포했습니다: https://local-fleet-studio.device.example.test", "obs-002:site_serve:0"),
				},
				CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
				ExpectedSelectedSkills:   []string{"website"},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.site_serve.requested", BodyFragment: "site_serve", Count: 1},
					{Name: "tool.site_serve.result", BodyFragment: "device.example.test", Count: 1},
				},
				ExpectedReplyFragments: []string{"https://local-fleet-studio.device.example.test"},
				ForbiddenReplyFragments: []string{
					"어떤 웹사이트",
					"무슨 웹사이트",
				},
			},
			{
				Prompt: "방금 만든 Local Fleet Studio 웹사이트의 첫 화면 제목을 'Local Fleet Studio Updated'로 바꾸고 보조 문구 '재배포 검증 완료'를 추가한 뒤 같은 사이트를 다시 배포해줘. 새 사이트는 만들지 마.",
				RouterRequiredEvidence: []string{
					"site_serve",
				},
				ActionResponses: []string{
					actionCallTool("site_list", `{}`),
					actionCallTool("file_write", `{"path":"/workspace/circles/staff/sites/local-fleet-studio/draft/app/src/App.tsx","content":"export default function App() {\n  return <main><h1>Local Fleet Studio Updated</h1><p>재배포 검증 완료</p></main>;\n}\n"}`),
					actionCallTool("terminal_run", `{"command":"mkdir -p dist && printf '<!doctype html><html><body><main><h1>Local Fleet Studio Updated</h1><p>재배포 검증 완료</p></main></body></html>' > dist/index.html","workingDirectoryPath":"/workspace/circles/staff/sites/local-fleet-studio/draft/app","timeoutSecond":120}`),
					actionInvokeCapabilityTool("site_serve", `{"title":"Local Fleet Studio","sourceWorkspacePath":"/workspace/circles/staff/sites/local-fleet-studio/draft","mode":"publish","siteReference":"local-fleet-studio"}`),
					actionFinishMessage("Local Fleet Studio 웹사이트를 수정하고 다시 배포했습니다: https://local-fleet-studio.device.example.test", "obs-002:file_write:0", "obs-004:site_serve:0"),
				},
				CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.site_list.requested", BodyFragment: "site_list", Count: 1},
					{Name: "tool.file_write.requested", BodyFragment: "Local Fleet Studio Updated", Count: 1},
					{Name: "tool.terminal_run.requested", BodyFragment: "dist/index.html", Count: 1},
					{Name: "tool.site_serve.requested", BodyFragment: "site_serve", Count: 1},
				},
				ExpectedReplyFragments: []string{"https://local-fleet-studio.device.example.test"},
			},
			{
				Prompt: "방금 배포한 Local Fleet Studio 테스트 웹사이트를 삭제해줘.",
				RouterRequiredEvidence: []string{
					"site_unserve",
				},
				ActionResponses: []string{
					actionCallTool("site_list", `{}`),
					actionCallToolWithMessage("site_unserve", "Local Fleet Studio 테스트 웹사이트를 삭제합니다.", `{"siteReference":"local-fleet-studio"}`),
				},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.site_list.requested", BodyFragment: "site_list", Count: 1},
					{Name: "tool.site_unserve.requested", BodyFragment: "site_unserve", Count: 1},
					{Name: "tool.site_unserve.result", BodyFragment: "interaction_required", Count: 1},
					{Name: "approval.pending_call", BodyFragment: `"site_unserve"`, Count: 1},
					{Name: "agent.failure_debt_created", BodyFragment: "", Count: 0},
				},
				ExpectedEvents:         []string{"confirmation.requested"},
				ExpectedReplyFragments: []string{"삭제"},
				ExpectedTaskStatus:     task.TaskStatusWaitingApproval,
			},
			{
				Prompt:         "확인",
				RouterApproval: "approve",
				ActionResponses: []string{
					actionFinishMessage("Local Fleet Studio 테스트 웹사이트를 삭제했습니다.", "obs-004:site_unserve:0"),
				},
				CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
				ExpectedEventCounts: []VirtualEventCount{
					{Name: "tool.site_unserve.requested", BodyFragment: "site_unserve", Count: 2},
					{Name: "tool.site_unserve.result", BodyFragment: "unserved", Count: 1},
					{Name: "approval.executed", BodyFragment: `"site_unserve"`, Count: 1},
				},
				ExpectedEvents:         []string{"confirmation.reply_classified"},
				ExpectedReplyFragments: []string{"삭제했습니다"},
			},
		},
	}
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
				actionCallToolWithMessage("ask_input", "어느 쪽으로 진행할까요?", `{"question":"어느 쪽으로 진행할까요?","choices":["첫 번째","두 번째"]}`),
			},
			ExpectedToolCalls:      []string{"ask_input"},
			ExpectedEvents:         []string{"ask.requested"},
			ExpectedReplyFragments: []string{"어느 쪽으로 진행할까요?"},
			ExpectedModelContexts:  []string{"choices"},
		}, {
			Prompt:          "두 번째",
			RouterTaskShape: agentcontract.TaskShapeImmediateReply,
			ActionResponses: []string{
				actionFinishMessage("두 번째로 진행하겠습니다."),
			},
			CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
			ExpectedEvents:           []string{"ask.resolved"},
			ExpectedReplyFragments:   []string{"두 번째"},
		}},
	}
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
		AllowedTools:     append(toolcontract.KernelToolNames(), "message_send"),
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
				{Name: "tool.message_send.requested", BodyFragment: `"targetType":"directMessage"`, Count: 1},
				{Name: "approval.pending_call", BodyFragment: `"message_send"`, Count: 1},
				{Name: "agent.failure_debt_created", BodyFragment: "", Count: 0},
			},
			ExpectedEvents:         []string{"confirmation.requested"},
			ExpectedReplyFragments: []string{"테스트", "오늘 오후 3시에 확인하자"},
			ExpectedTaskStatus:     task.TaskStatusWaitingApproval,
		}, {
			Prompt:         "확인",
			RouterApproval: "approve",
			ActionResponses: []string{
				actionFinishMessage("테스트이에게 DM을 보냈습니다.", "obs-001:message_send:0"),
			},
			CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
			ExpectedToolCalls:        []string{"message_send"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.message_send.requested", BodyFragment: `"targetType":"directMessage"`, Count: 2},
				{Name: "tool.message_send.result", BodyFragment: "virtual-platform-message-001", Count: 1},
				{Name: "approval.executed", BodyFragment: `"message_send"`, Count: 1},
			},
			ExpectedEvents:         []string{"confirmation.reply_classified"},
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
		AllowedTools:              append(toolcontract.KernelToolNames(), "message_send"),
		CapabilityToolNames:       []string{"message_send"},
		InitialToolNames:          []string{"message_send"},
		CapabilityToolDescriptors: []agentruntime.CapabilityToolDescriptor{{Name: "message_send", RequiresApproval: true}},
		Turns: []VirtualTurn{{
			Prompt: "announcements 채널에 오늘 5시에 전체 공지 회의 있다고 올려줘",
			ActionResponses: []string{
				actionCallTool("message_send", `{"targetType":"channel","channelName":"announcements","message":"오늘 5시에 전체 공지 회의가 있습니다."}`),
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.message_send.requested", BodyFragment: `"targetType":"channel"`, Count: 1},
				{Name: "approval.pending_call", BodyFragment: `"message_send"`, Count: 1},
			},
			ExpectedEvents:         []string{"confirmation.requested"},
			ExpectedReplyFragments: []string{"announcements", "오늘 5시"},
			ExpectedTaskStatus:     task.TaskStatusWaitingApproval,
		}, {
			Prompt:         "확인",
			RouterApproval: "approve",
			ActionResponses: []string{
				actionFinishMessage("announcements 채널에 공지를 올렸습니다.", "obs-001:message_send:0"),
			},
			CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
			ExpectedToolCalls:        []string{"message_send"},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.message_send.requested", BodyFragment: `"targetType":"channel"`, Count: 2},
				{Name: "tool.message_send.requested", BodyFragment: `"channelName":"announcements"`, Count: 2},
				{Name: "tool.message_send.requested", BodyFragment: `"targetType":"directMessage"`, Count: 0},
				{Name: "approval.executed", BodyFragment: `"message_send"`, Count: 1},
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
		AllowedTools:           append(toolcontract.KernelToolNames(), "message_search", "message_update"),
		CapabilityToolNames:    []string{"message_search", "message_update"},
		InitialToolNames:       []string{"message_search", "message_update"},
		Turns: []VirtualTurn{{
			Prompt: "방금 올린 공지 message virtual-platform-message-001 에서 '오후 5시'를 '오후 6시'로 바꿔줘",
			ActionResponses: []string{
				actionCallTool("message_search", `{"scope":"currentChannel","messageIDs":["virtual-platform-message-001"]}`),
				actionCallToolWithMessage("message_update", "공지 메시지 문구를 수정합니다.", `{"messageID":"virtual-platform-message-001","oldText":"오후 5시","newText":"오후 6시"}`),
				actionFinishMessage("공지 메시지 문구를 수정했습니다.", "obs-002:message_update:0"),
			},
			CompletionJudgeResponses: []string{completionJudgeSatisfiedResponse()},
			ExpectedToolCalls:        []string{"message_search", "message_update"},
			ExpectedToolCallCounts: map[string]int{
				"message_update": 1,
			},
			ExpectedEventCounts: []VirtualEventCount{
				{Name: "tool.message_search.result", BodyFragment: `회의실은 3층입니다.`, Count: 1},
				{Name: "tool.message_update.requested", BodyFragment: `"messageID":"virtual-platform-message-001"`, Count: 1},
				{Name: "tool.message_update.requested", BodyFragment: `"oldText":"오후 5시"`, Count: 1},
				{Name: "tool.message_update.requested", BodyFragment: `"newText":"오후 6시"`, Count: 1},
				{Name: "tool.message_update.result", BodyFragment: `"messageUpdated":true`, Count: 1},
			},
			ForbiddenEvents:        []string{"confirmation.requested", "approval.pending_call"},
			ExpectedReplyFragments: []string{"수정했습니다"},
			ExpectedTaskStatus:     task.TaskStatusCompleted,
		}},
	}
}

func presentationSkill() agentcontract.SkillInstruction {
	return workspaceSkillInstruction("presentation")
}

func sitePrototypeSkill() agentcontract.SkillInstruction {
	return workspaceSkillInstruction("website")
}

func sitePrototypeToolNames() []string {
	return []string{
		"terminal_run",
		"file_read",
		"file_write",
		"file_edit",
		"site_serve",
		"site_list",
		"site_unserve",
		"user_confirm",
	}
}

func sitePrototypeCapabilityToolNames() []string {
	return []string{
		"site_serve",
		"site_list",
		"site_unserve",
	}
}
