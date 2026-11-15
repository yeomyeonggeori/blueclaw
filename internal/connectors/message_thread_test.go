package connectors

import (
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

func TestAReplyAnswersAQuestionOnlyInItsPlace(t *testing.T) {
	const conversationID = "buzz:channel"
	rootQuestion := PostedQuestion{ConversationID: conversationID, ReplyTargetID: conversationID, MessageID: "question"}
	threadQuestion := PostedQuestion{ConversationID: conversationID, ReplyTargetID: conversationID + ":task-root", MessageID: "question"}
	testCases := []struct {
		name     string
		question PostedQuestion
		reply    MessagePlacement
		expected bool
	}{
		{"root question, root reply", rootQuestion, MessagePlacement{ConversationID: conversationID, ReplyTargetID: conversationID + ":reply"}, true},
		{"root question, reply in its thread", rootQuestion, MessagePlacement{ConversationID: conversationID, ReplyTargetID: conversationID + ":question", IsThread: true}, true},
		{"thread question, same-thread reply", threadQuestion, MessagePlacement{ConversationID: conversationID, ReplyTargetID: conversationID + ":task-root", IsThread: true}, true},
		{"thread question, root reply", threadQuestion, MessagePlacement{ConversationID: conversationID, ReplyTargetID: conversationID + ":reply"}, false},
		{"thread question, other-thread reply", threadQuestion, MessagePlacement{ConversationID: conversationID, ReplyTargetID: conversationID + ":other-root", IsThread: true}, false},
		{"root question, reply in another thread", rootQuestion, MessagePlacement{ConversationID: conversationID, ReplyTargetID: conversationID + ":other-root", IsThread: true}, false},
		{"root question, root reply in another conversation", rootQuestion, MessagePlacement{ConversationID: "buzz:elsewhere", ReplyTargetID: "buzz:elsewhere:reply"}, false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if answered := testCase.question.IsAnsweredBy(testCase.reply); answered != testCase.expected {
				t.Fatalf("answered = %v, expected %v", answered, testCase.expected)
			}
		})
	}
}

func TestAQuestionPostedForAnEarlierCallIsNotThePostedQuestionOfTheCurrentOne(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunID := taskRunService.CreateTaskRun("person-1", "conversation-1", "지워줘").TaskRunID
	earlier := holdrecord.Open(taskRunService, taskRunID, agentcontract.HeldCall{ToolName: "event_delete"}, nil)
	taskRunService.AppendTaskEvent(taskRunID, TaskEventQuestionPosted, `{"holdID":"`+earlier.ID+`","messageID":"question-1"}`)

	if messageID := PostedApprovalQuestionMessageID(taskRunService.ListTaskEvent(taskRunID)); messageID != "question-1" {
		t.Fatalf("the posted question read as %q", messageID)
	}
	holdrecord.Open(taskRunService, taskRunID, agentcontract.HeldCall{ToolName: "event_delete"}, nil)
	if messageID := PostedApprovalQuestionMessageID(taskRunService.ListTaskEvent(taskRunID)); messageID != "" {
		t.Fatalf("the earlier call's question read as %q", messageID)
	}
}
