package officeclaimcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/model"
)

const (
	ClaimThreshold     = 0.5
	claimsPerCall      = 60
	attachmentMaxRunes = 6000
)

const (
	KindSource     = "source"
	KindDerived    = "derived"
	KindExpression = "expression"
	KindClaim      = "claim"
	KindMistake    = "mistake"
	KindError      = "error"
	KindHollow     = "hollow"
)

type Treatment string

const (
	TreatmentBlank   Treatment = "blank"
	TreatmentRewrite Treatment = "rewrite"
)

type Profile struct {
	Name       string
	Kinds      map[string]string
	Guard      string
	Treatments map[string]Treatment
	Rewrites   map[string]string
}

func (profile Profile) isDefect(kind string) bool {
	_, isDefect := profile.Treatments[kind]
	return isDefect
}

const about = "The sources are request (the requester's own words), attachments (the text of the files they attached) " +
	"and runtimeFacts (what the system knew when it made the document: today, the requester, the document number and the company profile). " +
	"Each claim is one value the writer put into a delivered document; at says where it sits. All claims belong to one document, so a claim can be checked against the others. " +
	"removed, when present, lists values that were taken out of the document because the sources did not support them; the document no longer holds them."

const sourceKind = "says what the request, attachments or runtimeFacts say, in other words, shortened or merged, with the same values, owners and status; a figure credited to an origin the sources name as its origin is source"
const derivedKind = "follows correctly from the sources by arithmetic or the calendar (a total, a share, a multiple, a duration, a weekday), is a heading or label naming what the sources hold, or summarizes them adding nothing"
const expressionKind = "a unit with a relational role: a greeting, thanks, an apology, sympathy, an invitation to get in touch, a closing wish; a formulaic line that does a social or legal job (a business-letter greeting or closing, a contract recital, an agreement preamble, an offer-letter welcome, a proposal closing); or emphasis on, or an interpretation of, this document's own facts that adds no fact of its own (growth called fast when the sources give the figures, demand called proven when the sources show it). A line that does none of these jobs and only sounds weighty is hollow"

const originGuard = "A credit to an origin, such as a Source line, is judged on its own: choose claim when the sources do not name that origin, even when the figures beside it are in the sources; a heading or label that names what the sources hold without crediting an origin is not claim. "

const guardToday = "Wording, tone, emphasis and formatting are never claim, and whether the tone suits the kind of document is not this question. " +
	originGuard + "Choose claim only for content the sources do not hold, or hold with a weaker status."

const guardKinds = "Wording, tone, emphasis, formatting and where a value sits in a list are never a defect: choose source, derived or expression when the claim says what the sources say, however differently. " +
	"Choose claim for content the sources do not hold, and do not require a unit to be wrong to be a claim. " + originGuard +
	"Choose mistake or error only when you can name the exact source value or other claim it conflicts with; do not add a requirement the sources do not state."

const inventedFraming = "an origin named as where a figure comes from (a source line, a report, survey, dataset, internal system or metrics) that the sources do not state as that figure's origin, even when the figure itself is in the sources: a figure the request gives has no origin unless the request names one; " +
	"a slogan or tagline presented as the company's or product's own that the sources do not state; "

const removedReference = "a count, total, ordinal or reference that includes or names a value listed under removed, since the document no longer holds that value"

const claimKind = "a specific, checkable fact, number, name, customer, ranking, date, promise, condition, cause or availability that no source holds and that no source contradicts; " + inventedFraming + "a new obligation; an evaluation of a person or of a quality stated as attested fact; or " + removedReference

const hollowKind = "a unit that spends the reader's attention and trust while giving nothing back: it conveys none of this document's facts, grounds for judgment, requests or relational gestures, and instead asserts a vague value or significance that cannot be checked, or manufactures weight through rhetorical form alone. " +
	"Such text dilutes the real information, reads as machine-written so that the reader trusts even the true facts less, and signals that the writer did not think about this reader and situation. " +
	"It is hollow only if all three hold: (1) interchangeable: with only the names swapped it would fit any other document unchanged; " +
	"(2) functionless: removing it loses the reader nothing; " +
	"(3) uncheckable weight: it asserts an evaluation that cannot be verified, or its form is the message. " +
	"For a borderline unit ask: without this unit, would the document read as more trustworthy and faster to the reader? If yes, it is hollow. If removing it makes the document colder or impolite (greetings, thanks, a formal letter's conventions), it is expression, not hollow. " +
	"A specific checkable fact with no source is claim, and a real fact of this document restated wrongly or with its status changed is mistake. " +
	"Never choose hollow for tone, length, style or formality, or for any reason outside these criteria."

var (
	TodayProfile = Profile{
		Name: "today",
		Kinds: map[string]string{
			KindSource:     sourceKind,
			KindDerived:    derivedKind,
			KindExpression: expressionKind,
			KindClaim: "anything else: a fact, number, name, customer, ranking, date, promise, condition, cause or availability the sources lack; " +
				"a source fact stated with a stronger status (a forecast or target as achieved or confirmed, some as all, planned as done, an estimate or average as exact or guaranteed); " +
				inventedFraming + "a new obligation; an evaluation of a person or of a quality stated as attested fact; or " + removedReference,
		},
		Guard:      guardToday,
		Treatments: map[string]Treatment{KindClaim: TreatmentBlank},
	}
	CompactProfile = Profile{
		Name: "compact",
		Kinds: map[string]string{
			KindSource:     sourceKind,
			KindDerived:    derivedKind,
			KindExpression: expressionKind,
			KindClaim:      claimKind,
			KindMistake: "restates something the sources give but gets it wrong: a number, date, name, amount, owner or party that differs from the source, " +
				"or a status or scope the source does not give (a forecast stated as fact, a plan as done, some as all, an estimate as exact)",
			KindError: "derives or reasons from the sources and gets it wrong: a sum, share, date offset, unit conversion or conclusion that does not follow from the values the sources give, " +
				"or a claim that cannot be true together with another claim of the same document",
			KindHollow: hollowKind,
		},
		Guard: guardKinds,
		Treatments: map[string]Treatment{
			KindClaim: TreatmentBlank, KindMistake: TreatmentBlank, KindError: TreatmentBlank, KindHollow: TreatmentRewrite,
		},
		Rewrites: map[string]string{KindHollow: "Replace it with this document's own facts that the sources give, stated plainly, or answer with an empty text when none fits. Never add plausible-sounding new content: no number, name, date or specific the sources do not hold."},
	}
)

type Claim struct {
	Path   string `json:"path"`
	At     string `json:"at,omitempty"`
	Text   string `json:"text"`
	IsFree bool   `json:"free,omitempty"`
}

type Attachment struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

type Sources struct {
	Request      []string
	Attachments  []Attachment
	RuntimeFacts json.RawMessage
	Removed      []Claim
}

type Verdict struct {
	Claim
	Kind          string             `json:"kind"`
	Defect        string             `json:"defect,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	IsCopied      bool               `json:"copied,omitempty"`
	Treatment     Treatment          `json:"treatment,omitempty"`
}

type Judgment struct {
	Verdicts []Verdict   `json:"verdicts"`
	Usage    model.Usage `json:"usage"`
}

type shownClaim struct {
	At   string `json:"at,omitempty"`
	Text string `json:"text"`
}

func Judge(ctx context.Context, decisionModel model.DecisionModel, sources Sources, claims []Claim) (Judgment, error) {
	return JudgeWith(ctx, CompactProfile, decisionModel, sources, claims)
}

func JudgeWith(ctx context.Context, profile Profile, decisionModel model.DecisionModel, sources Sources, claims []Claim) (Judgment, error) {
	judgment := Judgment{}
	copies := copySources(sources)
	asked := []Claim{}
	for _, claim := range claims {
		if isCopiedFrom(claim.Text, copies) {
			judgment.Verdicts = append(judgment.Verdicts, Verdict{Claim: claim, Kind: KindSource, IsCopied: true})
			continue
		}
		asked = append(asked, claim)
	}
	for start := 0; start < len(asked); start += claimsPerCall {
		batch := asked[start:min(start+claimsPerCall, len(asked))]
		verdicts, usage, errorValue := judgeBatch(ctx, profile, decisionModel, sources, batch)
		if errorValue != nil {
			return Judgment{}, errorValue
		}
		judgment.Verdicts = append(judgment.Verdicts, verdicts...)
		judgment.Usage = addedUsage(judgment.Usage, usage)
	}
	return judgment, nil
}

func (judgment Judgment) Flagged() []Verdict {
	flagged := []Verdict{}
	for _, verdict := range judgment.Verdicts {
		if verdict.Defect != "" {
			flagged = append(flagged, verdict)
		}
	}
	return flagged
}

func (judgment Judgment) Treated(treatment Treatment) []Verdict {
	treated := []Verdict{}
	for _, verdict := range judgment.Flagged() {
		if verdict.Treatment == treatment {
			treated = append(treated, verdict)
		}
	}
	return treated
}

func (profile Profile) defectOf(probabilities map[string]float64, threshold float64) string {
	total, strongest, strongestKind := 0.0, 0.0, ""
	for _, kind := range sortedKinds(profile.Treatments) {
		probability := probabilities[kind]
		total += probability
		if probability > strongest {
			strongest, strongestKind = probability, kind
		}
	}
	if total < threshold {
		return ""
	}
	return strongestKind
}

func sortedKinds(treatments map[string]Treatment) []string {
	kinds := make([]string, 0, len(treatments))
	for kind := range treatments {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func (profile Profile) VerdictFor(claim Claim, choice string, probabilities map[string]float64) Verdict {
	verdict := Verdict{Claim: claim, Kind: choice, Probabilities: probabilities}
	verdict.Defect = profile.defectOf(probabilities, ClaimThreshold)
	verdict.Treatment = profile.Treatments[verdict.Defect]
	return verdict
}

func judgeBatch(ctx context.Context, profile Profile, decisionModel model.DecisionModel, sources Sources, claims []Claim) ([]Verdict, model.Usage, error) {
	response, errorValue := decisionModel.Decide(ctx, model.DecisionRequest{State: batchState(profile, sources, claims), Questions: batchQuestions(profile, len(claims))})
	if errorValue != nil {
		return nil, model.Usage{}, errorValue
	}
	verdicts := make([]Verdict, 0, len(claims))
	for index, claim := range claims {
		answer, isAnswered := response.Answers[questionKey(index)]
		if !isAnswered {
			return nil, model.Usage{}, fmt.Errorf("the decision model gave no answer for %s", questionKey(index))
		}
		verdicts = append(verdicts, profile.VerdictFor(claim, answer.Choice, answer.Probabilities))
	}
	return verdicts, response.Usage, nil
}

func batchState(profile Profile, sources Sources, claims []Claim) map[string]any {
	shown := map[string]shownClaim{}
	for index, claim := range claims {
		shown[questionKey(index)] = shownClaim{At: claim.At, Text: claim.Text}
	}
	state := map[string]any{
		"about":   about,
		"kinds":   profile.Kinds,
		"request": strings.Join(sources.Request, "\n\n"),
		"claims":  shown,
	}
	if len(sources.Removed) > 0 {
		state["removed"] = shownClaims(sources.Removed)
	}
	if len(sources.RuntimeFacts) > 0 {
		state["runtimeFacts"] = json.RawMessage(sources.RuntimeFacts)
	}
	if attachments := boundedAttachments(sources.Attachments); len(attachments) > 0 {
		state["attachments"] = attachments
	}
	return state
}

func shownClaims(claims []Claim) []shownClaim {
	shown := make([]shownClaim, 0, len(claims))
	for _, claim := range claims {
		shown = append(shown, shownClaim{At: claim.At, Text: claim.Text})
	}
	return shown
}

func batchQuestions(profile Profile, count int) map[string]model.DecisionQuestion {
	options := map[string]string{}
	for name := range profile.Kinds {
		options[name] = name
	}
	questions := map[string]model.DecisionQuestion{}
	for index := 0; index < count; index++ {
		key := questionKey(index)
		questions[key] = model.ChoiceQuestion{
			Instructions:       fmt.Sprintf("Which kind of value is claims.%s? %s Judge only claims.%s. The kinds are defined in kinds.", key, profile.Guard, key),
			OptionDescriptions: options,
		}.Question()
	}
	return questions
}

func questionKey(index int) string {
	return fmt.Sprintf("claim%d", index)
}

func boundedAttachments(attachments []Attachment) []Attachment {
	bounded := []Attachment{}
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.Text) == "" {
			continue
		}
		runes := []rune(attachment.Text)
		bounded = append(bounded, Attachment{Name: attachment.Name, Text: string(runes[:min(len(runes), attachmentMaxRunes)])})
	}
	return bounded
}

func copySources(sources Sources) []string {
	copies := append([]string{}, sources.Request...)
	for _, attachment := range sources.Attachments {
		copies = append(copies, attachment.Text)
	}
	var facts any
	if json.Unmarshal(sources.RuntimeFacts, &facts) == nil {
		copies = append(copies, stringValues(facts)...)
	}
	return copies
}

func isCopiedFrom(text string, sources []string) bool {
	wanted := collapsedSpace(text)
	if wanted == "" {
		return true
	}
	for _, source := range sources {
		if strings.Contains(collapsedSpace(source), wanted) {
			return true
		}
	}
	return false
}

func collapsedSpace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func stringValues(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		values := []string{}
		for _, item := range typed {
			values = append(values, stringValues(item)...)
		}
		return values
	case map[string]any:
		values := []string{}
		for _, item := range typed {
			values = append(values, stringValues(item)...)
		}
		return values
	}
	return nil
}

func addedUsage(total model.Usage, added model.Usage) model.Usage {
	total.PromptTokens += added.PromptTokens
	total.CompletionTokens += added.CompletionTokens
	total.TotalTokens += added.TotalTokens
	total.CostUSD += added.CostUSD
	return total
}
