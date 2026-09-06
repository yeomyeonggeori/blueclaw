package learning

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/skill"
)

const DefaultActiveLimit = 20
const maximumSkillInstructionBytes = 8 * 1024
const maximumSkillInstructionLines = 300
const maximumCandidateSkills = 20
const maximumRetainedRevisions = 100

type Skill struct {
	ID           string    `json:"id"`
	Version      int       `json:"version"`
	Audience     string    `json:"audience"`
	Description  string    `json:"description"`
	Instruction  string    `json:"instruction"`
	EvidenceIDs  []string  `json:"evidenceIDs"`
	Reason       string    `json:"reason,omitempty"`
	Verification string    `json:"verification"`
	Status       string    `json:"status"`
	Protected    bool      `json:"protected"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	RetiredAt    time.Time `json:"retiredAt,omitempty"`
}

type Store struct {
	mutex       sync.Mutex
	path        string
	activeLimit int
	enabled     bool
	skills      map[string][]Skill
}

type Settings struct {
	Enabled     bool `json:"enabled"`
	ActiveLimit int  `json:"activeLimit"`
}

type StoreDecision struct {
	Action          string `json:"action"`
	Skill           Skill  `json:"skill"`
	ID              string `json:"id"`
	ReplaceID       string `json:"replaceID,omitempty"`
	ExpectedVersion int    `json:"expectedVersion"`
}

func Open(path string, activeLimit int) (*Store, error) {
	if activeLimit <= 0 {
		activeLimit = DefaultActiveLimit
	}
	store := &Store{path: path, activeLimit: activeLimit, skills: map[string][]Skill{}}
	document, errorValue := os.ReadFile(path)
	if os.IsNotExist(errorValue) {
		document = nil
		errorValue = nil
	}
	if errorValue != nil {
		return nil, errorValue
	}
	if len(document) > 0 {
		if errorValue := json.Unmarshal(document, &store.skills); errorValue != nil {
			return nil, errorValue
		}
		for _, versions := range store.skills {
			for _, storedSkill := range versions {
				if errorValue := validateSkillInstruction(storedSkill); errorValue != nil {
					return nil, errorValue
				}
			}
		}
	}
	if settingsDocument, settingsError := os.ReadFile(path + ".settings"); settingsError == nil {
		var settings Settings
		if json.Unmarshal(settingsDocument, &settings) == nil {
			store.enabled = settings.Enabled
			if settings.ActiveLimit > 0 {
				store.activeLimit = settings.ActiveLimit
			}
		}
	}
	return store, nil
}

func (store *Store) List(audience string, includeRetired bool) []Skill {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	result := []Skill{}
	for _, versions := range store.skills {
		if len(versions) == 0 {
			continue
		}
		skill := versions[len(versions)-1]
		if skill.Audience == audience && (includeRetired || skill.Status == "active") {
			result = append(result, skill)
		}
	}
	sort.Slice(result, func(first, second int) bool { return result[first].UpdatedAt.After(result[second].UpdatedAt) })
	return result
}

func (store *Store) Get(id string, audience string, includeHistory bool) ([]Skill, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	versions := store.skills[strings.TrimSpace(id)]
	if len(versions) == 0 {
		return nil, errors.New("learned skill not found")
	}
	if versions[len(versions)-1].Audience != audience {
		return nil, errors.New("learned skill not found")
	}
	if includeHistory {
		return cloneSkillSlice(versions), nil
	}
	return []Skill{cloneSkill(versions[len(versions)-1])}, nil
}

func (store *Store) Settings() Settings {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return Settings{Enabled: store.enabled, ActiveLimit: store.activeLimit}
}

func (store *Store) ActiveCount() int {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return store.activeCount()
}

func (store *Store) UpdateSettings(settings Settings) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if settings.ActiveLimit <= 0 {
		return errors.New("active limit must be positive")
	}
	if errorValue := writePrivateJSON(store.path+".settings", settings); errorValue != nil {
		return errorValue
	}
	store.enabled, store.activeLimit = settings.Enabled, settings.ActiveLimit
	return nil
}

func (store *Store) Activate(id string, expectedVersion int) (Skill, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	versions := store.skills[id]
	if len(versions) == 0 {
		return Skill{}, errors.New("learned skill not found")
	}
	latest := versions[len(versions)-1]
	if latest.Version != expectedVersion || latest.Status != "candidate" {
		return Skill{}, errors.New("learned skill revision is stale")
	}
	if store.activeCount() >= store.activeLimit {
		return Skill{}, errors.New("active learned skill limit reached")
	}
	copySkills := cloneSkills(store.skills)
	copySkills[id][len(versions)-1].Status = "active"
	copySkills[id][len(versions)-1].UpdatedAt = time.Now().UTC()
	if errorValue := store.persistSkills(copySkills); errorValue != nil {
		return Skill{}, errorValue
	}
	store.skills = copySkills
	return copySkills[id][len(versions)-1], nil
}

func (store *Store) ApplyDecision(decision StoreDecision) (Skill, error) {
	switch decision.Action {
	case "create", "revise":
		return store.Put(decision.Skill)
	case "activate":
		return store.Activate(decision.ID, decision.ExpectedVersion)
	case "replace":
		return store.replace(decision)
	case "retire":
		return store.retireDecision(decision)
	case "restore":
		return store.Restore(decision.ID)
	default:
		return Skill{}, errors.New("unsupported learning decision")
	}
}

func (store *Store) retireDecision(decision StoreDecision) (Skill, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	versions := store.skills[decision.ID]
	if len(versions) == 0 {
		return Skill{}, errors.New("active learned skill not found")
	}
	latest := versions[len(versions)-1]
	if latest.Audience != decision.Skill.Audience {
		return Skill{}, errors.New("learned skill not found")
	}
	if decision.ExpectedVersion == 0 || latest.Version != decision.ExpectedVersion {
		return Skill{}, errors.New("learned skill revision is stale")
	}
	if latest.Status != "active" || latest.Protected {
		return Skill{}, errors.New("active learned skill cannot be retired")
	}
	copySkills := cloneSkills(store.skills)
	copySkills[decision.ID][len(versions)-1].Status = "retired"
	copySkills[decision.ID][len(versions)-1].RetiredAt = time.Now().UTC()
	copySkills[decision.ID][len(versions)-1].UpdatedAt = time.Now().UTC()
	if errorValue := store.persistSkills(copySkills); errorValue != nil {
		return Skill{}, errorValue
	}
	store.skills = copySkills
	return cloneSkill(copySkills[decision.ID][len(versions)-1]), nil
}

func (store *Store) replace(decision StoreDecision) (Skill, error) {
	if errorValue := validateSkillInstruction(decision.Skill); errorValue != nil {
		return Skill{}, errorValue
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	versions := store.skills[decision.ReplaceID]
	if len(versions) == 0 || versions[len(versions)-1].Protected {
		return Skill{}, errors.New("replace target is unavailable")
	}
	previous := versions[len(versions)-1]
	if decision.ExpectedVersion == 0 || previous.Version != decision.ExpectedVersion {
		return Skill{}, errors.New("learned skill revision is stale")
	}
	if previous.Audience != decision.Skill.Audience {
		return Skill{}, errors.New("learned skill audience cannot change")
	}
	if decision.Skill.ID == "" || decision.Skill.Audience == "" || decision.Skill.Instruction == "" {
		return Skill{}, errors.New("replacement skill is incomplete")
	}
	if decision.Skill.ID == decision.ReplaceID {
		return Skill{}, errors.New("replacement must use a new skill ID")
	}
	if len(store.skills[decision.Skill.ID]) != 0 {
		return Skill{}, errors.New("replacement skill ID already exists")
	}
	if decision.Skill.Status == "" {
		decision.Skill.Status = "candidate"
	}
	if decision.Skill.Status != "candidate" && decision.Skill.Status != "active" {
		return Skill{}, errors.New("learned skill status is invalid")
	}
	if len([]byte(decision.Skill.Instruction)) > maximumSkillInstructionBytes || len(strings.Split(decision.Skill.Instruction, "\n")) > maximumSkillInstructionLines {
		return Skill{}, errors.New("learned skill instruction exceeds size limits")
	}
	activeCount := store.activeCount()
	if previous.Status == "active" {
		activeCount--
	}
	if decision.Skill.Status == "active" && activeCount >= store.activeLimit {
		return Skill{}, errors.New("active learned skill limit reached")
	}
	if decision.Skill.Status == "candidate" && store.candidateCount() >= maximumCandidateSkills && versions[len(versions)-1].Status != "candidate" {
		return Skill{}, errors.New("learned skill candidate limit reached")
	}
	if store.revisionCount() >= maximumRetainedRevisions {
		return Skill{}, errors.New("learned skill history limit reached")
	}
	decision.Skill.Version = 1
	decision.Skill.CreatedAt = time.Now().UTC()
	decision.Skill.UpdatedAt = decision.Skill.CreatedAt
	copySkills := cloneSkills(store.skills)
	copySkills[decision.ReplaceID][len(versions)-1].Status = "retired"
	copySkills[decision.ReplaceID][len(versions)-1].RetiredAt = time.Now().UTC()
	copySkills[decision.Skill.ID] = append(copySkills[decision.Skill.ID], decision.Skill)
	if errorValue := store.persistSkills(copySkills); errorValue != nil {
		return Skill{}, errorValue
	}
	store.skills = copySkills
	return decision.Skill, nil
}

func cloneSkill(skill Skill) Skill {
	skill.EvidenceIDs = append([]string{}, skill.EvidenceIDs...)
	return skill
}
func cloneSkillSlice(skills []Skill) []Skill {
	clone := make([]Skill, len(skills))
	for index, skill := range skills {
		clone[index] = cloneSkill(skill)
	}
	return clone
}

func (store *Store) Put(skill Skill) (Skill, error) {
	if errorValue := validateSkillInstruction(skill); errorValue != nil {
		return Skill{}, errorValue
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if strings.TrimSpace(skill.ID) == "" || strings.TrimSpace(skill.Audience) == "" || strings.TrimSpace(skill.Instruction) == "" {
		return Skill{}, errors.New("skill id, audience, and instruction are required")
	}
	if len([]byte(skill.Instruction)) > maximumSkillInstructionBytes || len(strings.Split(skill.Instruction, "\n")) > maximumSkillInstructionLines {
		return Skill{}, errors.New("learned skill instruction exceeds size limits")
	}
	versions := store.skills[skill.ID]
	if len(versions) > 0 {
		latest := versions[len(versions)-1]
		if skill.Version == 0 || skill.Version != latest.Version {
			return Skill{}, errors.New("learned skill revision is stale")
		}
		if latest.Protected {
			return Skill{}, errors.New("protected learned skill cannot be replaced")
		}
		skill.Version = latest.Version + 1
		skill.CreatedAt = latest.CreatedAt
		if skill.Audience != latest.Audience {
			return Skill{}, errors.New("learned skill audience cannot change")
		}
		if latest.Protected {
			skill.Protected = true
		}
	}
	if skill.Version == 0 {
		skill.Version = 1
	}
	if skill.Status == "" {
		skill.Status = "candidate"
	}
	if skill.Status != "candidate" && skill.Status != "active" {
		return Skill{}, errors.New("learned skill status is invalid")
	}
	if skill.Status == "active" && store.activeCount() >= store.activeLimit && (len(versions) == 0 || versions[len(versions)-1].Status != "active") {
		return Skill{}, errors.New("active learned skill limit reached")
	}
	if skill.Status == "candidate" && store.candidateCount() >= maximumCandidateSkills && (len(versions) == 0 || versions[len(versions)-1].Status != "candidate") {
		return Skill{}, errors.New("learned skill candidate limit reached")
	}
	if store.revisionCount() >= maximumRetainedRevisions && len(versions) == 0 {
		return Skill{}, errors.New("learned skill history limit reached")
	}
	now := time.Now().UTC()
	if skill.CreatedAt.IsZero() {
		skill.CreatedAt = now
	}
	skill.UpdatedAt = now
	copySkills := cloneSkills(store.skills)
	copySkills[skill.ID] = append(copySkills[skill.ID], skill)
	if errorValue := store.persistSkills(copySkills); errorValue != nil {
		return Skill{}, errorValue
	}
	store.skills = copySkills
	return skill, nil
}

func validateSkillInstruction(candidate Skill) error {
	parsed, errorValue := skill.ParseDocument(candidate.Instruction)
	if errorValue != nil {
		return errors.New("learned skill instruction is not a valid SKILL.md document")
	}
	if strings.TrimSpace(parsed.Name) != "" && strings.TrimSpace(parsed.Name) != strings.TrimSpace(candidate.ID) {
		return errors.New("learned skill document name does not match its ID")
	}
	if strings.TrimSpace(candidate.Description) == "" && strings.TrimSpace(parsed.Description) == "" {
		return errors.New("learned skill description is required")
	}
	return nil
}

func (store *Store) SetProtected(id string, protected bool) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	versions := store.skills[id]
	if len(versions) == 0 {
		return errors.New("learned skill not found")
	}
	copySkills := cloneSkills(store.skills)
	copySkills[id][len(versions)-1].Protected = protected
	copySkills[id][len(versions)-1].UpdatedAt = time.Now().UTC()
	if errorValue := store.persistSkills(copySkills); errorValue != nil {
		return errorValue
	}
	store.skills = copySkills
	return nil
}

func (store *Store) Retire(id string) error { return store.changeStatus(id, "retired") }

func (store *Store) Restore(id string) (Skill, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	versions := store.skills[id]
	if len(versions) == 0 || versions[len(versions)-1].Status != "retired" {
		return Skill{}, errors.New("retired learned skill not found")
	}
	if store.activeCount() >= store.activeLimit {
		return Skill{}, errors.New("active learned skill limit reached")
	}
	copySkills := cloneSkills(store.skills)
	copySkills[id][len(versions)-1].Status = "active"
	copySkills[id][len(versions)-1].RetiredAt = time.Time{}
	copySkills[id][len(versions)-1].UpdatedAt = time.Now().UTC()
	if errorValue := store.persistSkills(copySkills); errorValue != nil {
		return Skill{}, errorValue
	}
	store.skills = copySkills
	return copySkills[id][len(versions)-1], nil
}

func (store *Store) changeStatus(id string, status string) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	versions := store.skills[id]
	if len(versions) == 0 || versions[len(versions)-1].Status != "active" {
		return errors.New("active learned skill not found")
	}
	if versions[len(versions)-1].Protected {
		return errors.New("protected learned skill cannot be retired")
	}
	copySkills := cloneSkills(store.skills)
	copySkills[id][len(versions)-1].Status = status
	copySkills[id][len(versions)-1].RetiredAt = time.Now().UTC()
	copySkills[id][len(versions)-1].UpdatedAt = time.Now().UTC()
	if errorValue := store.persistSkills(copySkills); errorValue != nil {
		return errorValue
	}
	store.skills = copySkills
	return nil
}

func (store *Store) activeCount() int {
	count := 0
	for _, versions := range store.skills {
		if len(versions) > 0 && versions[len(versions)-1].Status == "active" {
			count++
		}
	}
	return count
}

func (store *Store) candidateCount() int {
	count := 0
	for _, versions := range store.skills {
		if len(versions) > 0 && versions[len(versions)-1].Status == "candidate" {
			count++
		}
	}
	return count
}
func (store *Store) revisionCount() int {
	count := 0
	for _, versions := range store.skills {
		count += len(versions)
	}
	return count
}

func (store *Store) persist() error {
	return store.persistSkills(store.skills)
}

func cloneSkills(source map[string][]Skill) map[string][]Skill {
	clone := map[string][]Skill{}
	for id, versions := range source {
		clone[id] = append([]Skill{}, versions...)
	}
	return clone
}

func (store *Store) persistSkills(skills map[string][]Skill) error {
	document, errorValue := json.MarshalIndent(skills, "", "  ")
	if errorValue != nil {
		return errorValue
	}
	if errorValue := os.MkdirAll(filepath.Dir(store.path), 0o750); errorValue != nil {
		return errorValue
	}
	temporary, errorValue := os.CreateTemp(filepath.Dir(store.path), ".learned-skills-*.json")
	if errorValue != nil {
		return errorValue
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, errorValue := temporary.Write(append(document, '\n')); errorValue != nil {
		temporary.Close()
		return errorValue
	}
	if errorValue := temporary.Close(); errorValue != nil {
		return errorValue
	}
	return os.Rename(temporaryPath, store.path)
}
