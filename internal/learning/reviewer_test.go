package learning

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/model"
)

type syntheticReviewerModel struct {
	responses []string
	calls     int
}

func (model *syntheticReviewerModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}
func (reviewModel *syntheticReviewerModel) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	response := reviewModel.responses[reviewModel.calls]
	reviewModel.calls++
	return model.StructuredResponse{Content: response}, nil
}

func TestReviewerKeepsWithoutMutationProposal(t *testing.T) {
	model := &syntheticReviewerModel{responses: []string{`{"action":"keep","skillID":"","expectedVersion":0,"replaceID":"","replaceVersion":0,"description":"","instruction":"","soulDocument":"","evidenceIDs":[],"reason":"no durable improvement"}`}}
	decision, traces, errorValue := (Reviewer{Model: model}).Review(context.Background(), ReviewInput{Experience: []Experience{{TaskID: "task-1", Audience: "company"}}, Soul: []byte(`{"schemaVersion":1}`)})
	if errorValue != nil || decision.Action != "keep" || len(traces) != 1 || model.calls != 1 {
		t.Fatalf("unexpected keep review: %+v %v %d %d", decision, errorValue, len(traces), model.calls)
	}
}

func TestReviewerRejectsUnsupportedEvidenceProposal(t *testing.T) {
	model := &syntheticReviewerModel{responses: []string{`{"action":"create","skillID":"new","expectedVersion":0,"replaceID":"","replaceVersion":0,"description":"procedure","instruction":"---\nname: procedure\n---\nsteps","soulDocument":"","evidenceIDs":["invented"],"reason":"supported"}`}}
	_, _, errorValue := (Reviewer{Model: model}).Review(context.Background(), ReviewInput{Experience: []Experience{{TaskID: "task-1", Audience: "company"}}, Soul: []byte(`{"schemaVersion":1}`)})
	if errorValue == nil {
		t.Fatal("expected invented evidence to be rejected")
	}
}

func TestReviewerSecondAssessmentRejectsProposal(t *testing.T) {
	model := &syntheticReviewerModel{responses: []string{
		`{"action":"soul","skillID":"","expectedVersion":0,"replaceID":"","replaceVersion":0,"description":"","instruction":"","soulDocument":"{\"schemaVersion\":1}","evidenceIDs":["task-1"],"reason":"observed pattern"}`,
		`{"supported":false,"reason":"isolated evidence"}`,
	}}
	decision, traces, errorValue := (Reviewer{Model: model}).Review(context.Background(), ReviewInput{Experience: []Experience{{TaskID: "task-1", Audience: "company"}}, Soul: []byte(`{"schemaVersion":1}`)})
	if errorValue != nil || decision.Action != "keep" || len(traces) != 2 || model.calls != 2 {
		t.Fatalf("unexpected assessment result: %+v %v", decision, errorValue)
	}
}
