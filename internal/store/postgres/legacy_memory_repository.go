package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/lib/pq"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

// LegacyMemoryRepository reads the memory the retired store holds, so a
// conversion can carry it into one file per subject. It only reads: the rows
// stay where they are until a later release drops the tables.
type LegacyMemoryRepository struct {
	database Database
}

func NewLegacyMemoryRepository(database Database) LegacyMemoryRepository {
	return LegacyMemoryRepository{database: database}
}

// staticKinds are the kinds the retired store used for what a person is rather
// than what happened to them.
var staticKinds = map[string]bool{"identity": true, "preference": true}

// LiveFacts is every fact a reader could still be shown: nothing forgotten and
// nothing superseded, with its circles and the embedding it was settled with.
func (repository LegacyMemoryRepository) LiveFacts(ctx context.Context) ([]memory.LegacyFact, error) {
	rows, errorValue := repository.database.SQL.QueryContext(ctx, `
SELECT f.fact_id, f.owner_person_id, f.content, f.kind, f.embedding_model,
       f.security_level_rank, f.required_classes, f.valid_from, f.valid_until, f.created_at,
       COALESCE(ARRAY_AGG(DISTINCT c.circle_id) FILTER (WHERE c.circle_id IS NOT NULL), '{}') AS circle_ids,
       e.embedding::text
  FROM memory_fact f
  LEFT JOIN memory_fact_circle c ON c.fact_id = f.fact_id
  LEFT JOIN memory_fact_embedding e ON e.fact_id = f.fact_id
 WHERE f.forgotten_at IS NULL AND f.superseded_by IS NULL
 GROUP BY f.fact_id, f.owner_person_id, f.content, f.kind, f.embedding_model,
          f.security_level_rank, f.required_classes, f.valid_from, f.valid_until, f.created_at,
          e.embedding
 ORDER BY f.created_at, f.fact_id`)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()

	facts := []memory.LegacyFact{}
	for rows.Next() {
		fact, errorValue := scanLegacyFact(rows)
		if errorValue != nil {
			return nil, errorValue
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func scanLegacyFact(rows *sql.Rows) (memory.LegacyFact, error) {
	var fact memory.LegacyFact
	var kind string
	var embeddingModel sql.NullString
	var requiredClasses, circleIDs pq.StringArray
	var validFrom, validUntil sql.NullTime
	var encodedEmbedding sql.NullString

	errorValue := rows.Scan(&fact.FactID, &fact.OwnerPersonID, &fact.Content, &kind, &embeddingModel,
		&fact.SecurityLevelRank, &requiredClasses, &validFrom, &validUntil, &fact.CreatedAt,
		&circleIDs, &encodedEmbedding)
	if errorValue != nil {
		return fact, errorValue
	}

	fact.IsStatic = staticKinds[kind]
	fact.EmbeddingModel = embeddingModel.String
	fact.RequiredClasses = requiredClasses
	fact.CircleIDs = circleIDs
	if validFrom.Valid {
		fact.OccurredAt = validFrom.Time.UTC()
	}
	if validUntil.Valid {
		fact.ValidUntil = validUntil.Time.UTC()
	}
	if encodedEmbedding.Valid {
		embedding, errorValue := decodeVector(encodedEmbedding.String)
		if errorValue != nil {
			return fact, fmt.Errorf("embedding of %s: %w", fact.FactID, errorValue)
		}
		fact.Embedding = embedding
	}
	return fact, nil
}

// decodeVector reads pgvector's own text form, which is a JSON array of
// numbers, so the embedding crosses without a second encoding to agree about.
func decodeVector(encoded string) ([]float32, error) {
	var numbers []float32
	if errorValue := json.Unmarshal([]byte(encoded), &numbers); errorValue != nil {
		return nil, errorValue
	}
	return numbers, nil
}

// HoldsMemory reports whether the retired tables are present and still carry a
// fact, which is what a first start checks before it converts anything.
func (repository LegacyMemoryRepository) HoldsMemory(ctx context.Context) (bool, error) {
	var count int
	errorValue := repository.database.SQL.QueryRowContext(ctx, `
SELECT COUNT(*) FROM memory_fact WHERE forgotten_at IS NULL AND superseded_by IS NULL`).Scan(&count)
	if errorValue != nil {
		if isUndefinedTable(errorValue) {
			return false, nil
		}
		return false, errorValue
	}
	return count > 0, nil
}

func isUndefinedTable(errorValue error) bool {
	pgError, isPostgresError := errorValue.(*pq.Error)
	return isPostgresError && pgError.Code == "42P01"
}

// Roster reads the projection back out of the person table. That table is
// written from whatever policy document the runtime applied, so it is the
// roster the device was using when these facts were written and read, which a
// configuration file on disk may no longer be.
func (repository LegacyMemoryRepository) Roster(ctx context.Context) (policy.PolicyProjection, error) {
	projection := policy.PolicyProjection{
		PersonAccessByPersonID: map[string]policy.PersonAccess{},
		ContainedCirclesByID:   map[string][]string{},
	}
	rows, errorValue := repository.database.SQL.QueryContext(ctx, `
SELECT person_id, security_level_rank, granted_classes, circles
  FROM person
 ORDER BY person_id`)
	if errorValue != nil {
		return projection, errorValue
	}
	defer rows.Close()

	for rows.Next() {
		var personID string
		var securityLevelRank int
		var grantedClasses, circles pq.StringArray
		if errorValue := rows.Scan(&personID, &securityLevelRank, &grantedClasses, &circles); errorValue != nil {
			return projection, errorValue
		}
		projection.PersonAccessByPersonID[personID] = policy.PersonAccess{
			PersonID:          personID,
			Circles:           circles,
			SecurityLevelRank: securityLevelRank,
			GrantedClasses:    grantedClasses,
		}
	}
	return projection, rows.Err()
}
