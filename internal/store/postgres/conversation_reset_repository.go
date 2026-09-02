package postgres

import (
	"context"
)

type ConversationResetRepository struct {
	database Database
}

func NewConversationResetRepository(database Database) ConversationResetRepository {
	return ConversationResetRepository{database: database}
}

func (repository ConversationResetRepository) ResetMattermostDirectConversation(ctx context.Context, channelID string) (int64, error) {
	directConversationID := "dm:" + channelID
	threadConversationPattern := "thread:" + channelID + ":%"
	transaction, errorValue := repository.database.SQL.BeginTx(ctx, nil)
	if errorValue != nil {
		return 0, errorValue
	}
	defer func() { _ = transaction.Rollback() }()
	statements := []string{
		`DELETE FROM task_run
		 WHERE origin_conversation_id = $1 OR origin_conversation_id LIKE $2`,
		`DELETE FROM raw_event
		 WHERE conversation_id IN (
		   SELECT conversation_id FROM conversation
		   WHERE platform = 'mattermost'
		     AND (external_conversation_id = $1 OR external_conversation_id LIKE $2))`,
		`UPDATE memory_fact SET forgotten_at = now(), forget_reason = 'conversation reset'
		 WHERE forgotten_at IS NULL
		   AND episode_id IN (
		     SELECT episode_id FROM memory_episode
		     WHERE conversation_id = $1 OR conversation_id LIKE $2)`,
		`DELETE FROM conversation
		 WHERE platform = 'mattermost'
		   AND (external_conversation_id = $1 OR external_conversation_id LIKE $2)`,
	}
	deletedRows := int64(0)
	for _, statement := range statements {
		result, errorValue := transaction.ExecContext(ctx, statement, directConversationID, threadConversationPattern)
		if errorValue != nil {
			return 0, errorValue
		}
		affected, errorValue := result.RowsAffected()
		if errorValue != nil {
			return 0, errorValue
		}
		deletedRows += affected
	}
	if errorValue := transaction.Commit(); errorValue != nil {
		return 0, errorValue
	}
	return deletedRows, nil
}
