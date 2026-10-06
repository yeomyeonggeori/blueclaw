//go:build appliance && llmeval && !nobundledharness

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
)

const approvalReplyCasesPath = "testdata/approval_reply_cases.jsonl"

const (
	readAsApprove = "approve"
	readAsReject  = "reject"
	readAsOther   = "other"
)

var minimumCorrectReadings = map[string]int{"chat": 98, "acp": 97, "acp-host-named": 97}

type approvalReplyCase struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Reply    string `json:"reply"`
	Expected string `json:"expected"`
}

func TestApprovalReplyReadingLive(t *testing.T) {
	decisionModel, _ := routingDecisionModel(t)
	reader := approvalreply.NewDecisionModelReader(decisionModel)
	cases := approvalReplyCases(t)
	for path, optionsOf := range approvalReplyPaths() {
		for run := 1; run <= routingRunCount(t); run++ {
			correct, wrongWay := 0, []string{}
			for _, current := range cases {
				reading := readApprovalReply(t, reader, optionsOf(), current)
				if reading == current.Expected {
					correct++
				} else if reading != readAsOther && current.Expected != readAsOther {
					wrongWay = append(wrongWay, current.ID)
				}
			}
			t.Logf("%s run %d: %d/%d correct, wrong-way %v", path, run, correct, len(cases), wrongWay)
			if len(wrongWay) > 0 {
				t.Errorf("%s run %d read an approval the wrong way round on %v", path, run, wrongWay)
			}
			if correct < minimumCorrectReadings[path] {
				t.Errorf("%s run %d read %d of %d, below the %d it must keep", path, run, correct, len(cases), minimumCorrectReadings[path])
			}
		}
	}
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

func readApprovalReply(t *testing.T, reader approvalreply.Reader, options []approvalreply.Option, current approvalReplyCase) string {
	t.Helper()
	optionID, isAnswer, errorValue := reader.Read(context.Background(), approvalreply.Question{Text: current.Question, Options: options}, current.Reply, nil)
	if errorValue != nil {
		t.Fatalf("%s: %v", current.ID, errorValue)
	}
	return meaningOfReading(options, optionID, isAnswer)
}

func meaningOfReading(options []approvalreply.Option, optionID string, isAnswer bool) string {
	if !isAnswer {
		return readAsOther
	}
	for _, option := range options {
		if option.ID == optionID && option.Meaning == approvalreply.RejectMeaning {
			return readAsReject
		}
	}
	return readAsApprove
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
