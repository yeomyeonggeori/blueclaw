//go:build llmeval

package officevisualcheck

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/evaltest"
	"github.com/yeomyeonggeori/blueprotocol/model/decisions"
)

const manifestVariable = "VISUALCHECK_MANIFEST"

type diskDeck struct {
	manifest Manifest
}

func (deck diskDeck) Manifest(context.Context) (Manifest, error) {
	reviewOnly := deck.manifest
	reviewOnly.Rounds = 0
	return reviewOnly, nil
}

func (deck diskDeck) Image(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (deck diskDeck) Rebuild(context.Context, map[int]string) (Manifest, error) {
	return Manifest{}, errors.New("the live review does not rebuild")
}

func TestLiveReviewFlagsPlantedDefects(t *testing.T) {
	manifestPath := evaltest.RequireInput(t, manifestVariable, "point it at the build/review/visual-review.json of a deck with planted visual defects")
	evaltest.RequireInput(t, "BLUECOLLAR_DECISION_API_KEY", "the key of the decision endpoint")
	evaltest.RequireInput(t, "BLUECOLLAR_DECISION_MODEL", "the decision model, such as cloudflare/clef")
	endpoint, errorValue := decisions.EndpointFromEnvironment()
	evaltest.RequireConfigured(t, "the decision model", errorValue)
	content, errorValue := os.ReadFile(manifestPath)
	if errorValue != nil {
		t.Fatalf("read %s: %v", manifestPath, errorValue)
	}
	manifest, errorValue := ParseManifest(content)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	report, errorValue := Run(context.Background(), endpoint.DecisionModel(), nil, diskDeck{manifest: manifest})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, slide := range report.Slides {
		t.Logf("slide %d: findings %+v measured %v error %q", slide.Number, slide.Findings, slide.Measured, slide.Error)
	}
	t.Logf("flagged slides %v of %d, decision cost USD %.5f", report.Leftovers, len(report.Slides), report.Usage.Decision.CostUSD)
	if len(report.Leftovers) == 0 {
		t.Fatal("no slide was flagged in a deck with planted defects")
	}
}
