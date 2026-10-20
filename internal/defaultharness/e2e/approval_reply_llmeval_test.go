//go:build appliance && llmeval && !nobundledharness

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

const approvalReplyCasesPath = "testdata/approval_reply_cases.jsonl"

const readAsApprove = "approve"

var mostMissedApprovals = map[string]int{"chat": 4, "acp": 4, "acp-host-named": 4}

type approvalReplyCase struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Reply    string `json:"reply"`
	Expected string `json:"expected"`
}

func TestApprovalReplyReadingLive(t *testing.T) {
	decisionModel, _ := routingDecisionModel(t)
	recording := &answerRecordingDecisionModel{inner: decisionModel}
	reader := approvalreply.NewDecisionModelReader(recording)
	cases := approvalReplyCases(t)
	for path, optionsOf := range approvalReplyPaths() {
		for run := 1; run <= routingRunCount(t); run++ {
			falseApprovals, missedApprovals := []string{}, []string{}
			for _, current := range cases {
				isApproval := readsAsApproval(t, reader, optionsOf(), current)
				t.Logf("%s run %d %s %q expected %s: approval=%v probabilities %v", path, run, current.ID, current.Reply, current.Expected, isApproval, recording.lastProbabilities())
				switch {
				case isApproval && current.Expected != readAsApprove:
					falseApprovals = append(falseApprovals, current.ID)
				case !isApproval && current.Expected == readAsApprove:
					missedApprovals = append(missedApprovals, current.ID)
				}
			}
			t.Logf("%s run %d: false approvals %v, missed approvals %v of %d", path, run, falseApprovals, missedApprovals, len(cases))
			if len(falseApprovals) > 0 {
				t.Errorf("%s run %d approved replies that do not approve: %v", path, run, falseApprovals)
			}
			if len(missedApprovals) > mostMissedApprovals[path] {
				t.Errorf("%s run %d missed %d approvals, more than the %d it may", path, run, len(missedApprovals), mostMissedApprovals[path])
			}
		}
	}
}

type answerRecordingDecisionModel struct {
	inner    model.DecisionModel
	mutex    sync.Mutex
	response model.DecisionResponse
}

func (recording *answerRecordingDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	response, errorValue := recording.inner.Decide(ctx, request)
	recording.mutex.Lock()
	recording.response = response
	recording.mutex.Unlock()
	return response, errorValue
}

func (recording *answerRecordingDecisionModel) lastProbabilities() map[string]float64 {
	recording.mutex.Lock()
	defer recording.mutex.Unlock()
	return recording.response.Answers[approvalreply.AnswerQuestionName].Probabilities
}

func approvalReplyPaths() map[string]func() []approvalreply.Option {
	return map[string]func() []approvalreply.Option{
		"chat":           chatApprovalOptions,
		"acp":            func() []approvalreply.Option { return acpApprovalOptions("Allow", "Reject") },
		"acp-host-named": func() []approvalreply.Option { return acpApprovalOptions("approve this call", "decline this call") },
	}
}

func chatApprovalOptions() []approvalreply.Option {
	return approvalreply.OptionsOf([]approvalreply.Offer{
		{ID: connectors.ApproveOptionID},
		{ID: connectors.RejectOptionID, IsDeclining: true},
	})
}

func acpApprovalOptions(allowName string, rejectName string) []approvalreply.Option {
	return approvalgate.ReplyOptionsOf([]acp.PermissionOption{
		{OptionId: "allow_once", Name: allowName, Kind: acp.PermissionOptionKindAllowOnce},
		{OptionId: "reject_once", Name: rejectName, Kind: acp.PermissionOptionKindRejectOnce},
	})
}

func readsAsApproval(t *testing.T, reader approvalreply.Reader, options []approvalreply.Option, current approvalReplyCase) bool {
	t.Helper()
	reading, errorValue := reader.Read(context.Background(), approvalreply.Question{Text: current.Question, Options: options}, current.Reply, nil)
	if errorValue != nil {
		t.Fatalf("%s: %v", current.ID, errorValue)
	}
	return reading.IsApproval()
}

func approvalReplyCases(t *testing.T) []approvalReplyCase {
	t.Helper()
	document, errorValue := os.ReadFile(approvalReplyCasesPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	cases := []approvalReplyCase{}
	scanner := bufio.NewScanner(bytes.NewReader(document))
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var current approvalReplyCase
		if errorValue := json.Unmarshal(scanner.Bytes(), &current); errorValue != nil {
			t.Fatal(errorValue)
		}
		cases = append(cases, current)
	}
	return cases
}
