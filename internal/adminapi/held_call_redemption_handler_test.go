package adminapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

func postRedemption(handler HeldCallRedemptionHandler, body string) *httptest.ResponseRecorder {
	responseRecorder := httptest.NewRecorder()
	handler.HandleRedeemHeldCall(responseRecorder, httptest.NewRequest(http.MethodPost, "/admin/api/held-call/redeem", bytes.NewBufferString(body)))
	return responseRecorder
}

func TestAHeldCallIsRedeemedOnceOverHTTPAndRefusedWithTheReasonAfter(t *testing.T) {
	taskRunService, _ := approvalTestTaskRunService(t)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "박예시에게 보내줘")
	holdID := holdrecord.Open(taskRunService, taskRun.TaskRunID, agentcontract.HeldCall{ToolName: "message_send", ToolInput: json.RawMessage(`{"message":"안녕하세요"}`)}, nil).ID
	holdrecord.Decide(taskRunService, taskRun.TaskRunID, holdID, holdrecord.DecisionApprove, "chat_reply")
	handler := HeldCallRedemptionHandler{TaskRunService: taskRunService}
	body := `{"taskRunID":"` + taskRun.TaskRunID + `","holdID":"` + holdID + `","requesterPersonID":"person-1","toolName":"message_send","toolInput":{"message":"안녕하세요"}}`

	if responseRecorder := postRedemption(handler, body); responseRecorder.Code != http.StatusOK {
		t.Fatalf("the approved call answered %d %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	responseRecorder := postRedemption(handler, body)
	if responseRecorder.Code != http.StatusConflict || !strings.Contains(responseRecorder.Body.String(), "already carried out") {
		t.Fatalf("a second redemption answered %d %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if responseRecorder := postRedemption(handler, `not json`); responseRecorder.Code != http.StatusBadRequest {
		t.Fatalf("a malformed redemption answered %d", responseRecorder.Code)
	}
}
