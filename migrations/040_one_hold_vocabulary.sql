CREATE OR REPLACE FUNCTION hold_vocabulary_json(document text) RETURNS jsonb
LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
  RETURN document::jsonb;
EXCEPTION WHEN others THEN
  RETURN NULL;
END
$$;

WITH offered AS (
  SELECT task_event_id, task_run_id, created_at, hold_vocabulary_json(body) AS record
  FROM task_event
  WHERE name = 'approval.choices_offered'
),
offered_hold AS (
  SELECT offered.record -> 'choices' AS choices, hold.task_event_id AS hold_event_id
  FROM offered
  JOIN LATERAL (
    SELECT hold_event.task_event_id, hold_vocabulary_json(hold_event.body) AS record
    FROM task_event hold_event
    WHERE hold_event.task_run_id = offered.task_run_id
      AND hold_event.name IN ('approval.pending_call', 'approval.hold_opened')
      AND (hold_event.created_at, hold_event.task_event_id) < (offered.created_at, offered.task_event_id)
    ORDER BY hold_event.created_at DESC, hold_event.task_event_id DESC
    LIMIT 1
  ) hold ON true
  WHERE offered.record ? 'choices'
    AND btrim(coalesce(hold.record ->> 'toolName', '')) = btrim(coalesce(offered.record ->> 'toolName', ''))
    AND coalesce(hold.record -> 'toolInput', '{}'::jsonb) = coalesce(offered.record -> 'toolInput', '{}'::jsonb)
)
UPDATE task_event
SET body = (hold_vocabulary_json(task_event.body) || jsonb_build_object('choices', offered_hold.choices))::text
FROM offered_hold
WHERE task_event.task_event_id = offered_hold.hold_event_id;

DELETE FROM task_event WHERE name = 'approval.choices_offered';

UPDATE task_event
SET name = 'approval.hold_opened'
WHERE name = 'approval.held_call'
  AND EXISTS (
    SELECT 1
    FROM task_run
    WHERE task_run.task_run_id = task_event.task_run_id
      AND task_run.status = 'waiting_approval'
  )
  AND NOT EXISTS (
    SELECT 1
    FROM task_event other_hold
    WHERE other_hold.task_run_id = task_event.task_run_id
      AND other_hold.name IN ('approval.pending_call', 'approval.hold_opened')
  );

DELETE FROM task_event WHERE name = 'approval.held_call';

UPDATE task_event SET name = 'approval.hold_opened' WHERE name = 'approval.pending_call';
UPDATE task_event SET name = 'approval.hold_spent' WHERE name = 'approval.executed';

UPDATE task_event
SET body = (
  (hold_vocabulary_json(body) - 'approvalToken')
  || jsonb_build_object('holdID', coalesce(
    nullif(hold_vocabulary_json(body) ->> 'holdID', ''),
    nullif(hold_vocabulary_json(body) ->> 'approvalToken', ''),
    task_event_id
  ))
)::text
WHERE name = 'approval.hold_opened'
  AND hold_vocabulary_json(body) IS NOT NULL
  AND (
    hold_vocabulary_json(body) ? 'approvalToken'
    OR coalesce(hold_vocabulary_json(body) ->> 'holdID', '') = ''
  );

UPDATE task_event
SET body = (
  (hold_vocabulary_json(body) - 'approvalToken')
  || jsonb_build_object('holdID', coalesce(
    nullif(hold_vocabulary_json(body) ->> 'holdID', ''),
    hold_vocabulary_json(body) ->> 'approvalToken',
    ''
  ))
)::text
WHERE name IN ('approval.decided', 'approval.hold_spent')
  AND hold_vocabulary_json(body) ? 'approvalToken';

UPDATE task_event
SET body = jsonb_set(
  hold_vocabulary_json(body),
  '{decision}',
  to_jsonb(CASE hold_vocabulary_json(body) ->> 'decision'
    WHEN 'cancel' THEN 'reject'
    ELSE 'approve'
  END)
)::text
WHERE name = 'approval.decided'
  AND hold_vocabulary_json(body) ->> 'decision' IN ('confirm', 'confirm_task', 'cancel');

UPDATE task_event
SET body = (
  (hold_vocabulary_json(body) - 'presentedToken' - 'awaitingHeldCallIDs')
  || jsonb_build_object('presentedHoldID', coalesce(hold_vocabulary_json(body) -> 'presentedToken', to_jsonb(''::text)))
)::text
WHERE name = 'approval.unheld_call_carried_out'
  AND (
    hold_vocabulary_json(body) ? 'presentedToken'
    OR hold_vocabulary_json(body) ? 'awaitingHeldCallIDs'
  );

DO $$
DECLARE
  legacy record;
  paired_hold_id text;
BEGIN
  FOR legacy IN
    SELECT task_event_id, task_run_id, name, created_at, hold_vocabulary_json(body) AS record
    FROM task_event
    WHERE name IN ('approval.decided', 'approval.hold_spent')
      AND hold_vocabulary_json(body) IS NOT NULL
      AND coalesce(hold_vocabulary_json(body) ->> 'holdID', '') = ''
    ORDER BY created_at, task_event_id
  LOOP
    paired_hold_id := NULL;
    IF legacy.name = 'approval.decided' THEN
      SELECT hold.record ->> 'holdID' INTO paired_hold_id
      FROM (
        SELECT task_event_id, created_at, hold_vocabulary_json(body) AS record
        FROM task_event
        WHERE name = 'approval.hold_opened'
          AND task_run_id = legacy.task_run_id
          AND (created_at, task_event_id) < (legacy.created_at, legacy.task_event_id)
      ) hold
      WHERE NOT EXISTS (
        SELECT 1
        FROM task_event earlier
        WHERE earlier.name = 'approval.decided'
          AND earlier.task_run_id = legacy.task_run_id
          AND (earlier.created_at, earlier.task_event_id) < (legacy.created_at, legacy.task_event_id)
          AND hold_vocabulary_json(earlier.body) ->> 'holdID' = hold.record ->> 'holdID'
      )
      ORDER BY hold.created_at DESC, hold.task_event_id DESC
      LIMIT 1;
    ELSE
      SELECT hold.record ->> 'holdID' INTO paired_hold_id
      FROM (
        SELECT task_event_id, created_at, hold_vocabulary_json(body) AS record
        FROM task_event
        WHERE name = 'approval.hold_opened'
          AND task_run_id = legacy.task_run_id
          AND (created_at, task_event_id) < (legacy.created_at, legacy.task_event_id)
      ) hold
      WHERE btrim(coalesce(hold.record ->> 'toolName', '')) = btrim(coalesce(legacy.record ->> 'toolName', ''))
        AND EXISTS (
          SELECT 1
          FROM task_event approval
          WHERE approval.name = 'approval.decided'
            AND approval.task_run_id = legacy.task_run_id
            AND hold_vocabulary_json(approval.body) ->> 'holdID' = hold.record ->> 'holdID'
            AND hold_vocabulary_json(approval.body) ->> 'decision' = 'approve'
        )
        AND NOT EXISTS (
          SELECT 1
          FROM task_event spent
          WHERE spent.name = 'approval.hold_spent'
            AND spent.task_run_id = legacy.task_run_id
            AND hold_vocabulary_json(spent.body) ->> 'holdID' = hold.record ->> 'holdID'
        )
      ORDER BY hold.created_at DESC, hold.task_event_id DESC
      LIMIT 1;
    END IF;
    IF paired_hold_id IS NOT NULL THEN
      UPDATE task_event
      SET body = (hold_vocabulary_json(body) || jsonb_build_object('holdID', paired_hold_id))::text
      WHERE task_event_id = legacy.task_event_id;
    END IF;
  END LOOP;
END
$$;

DROP FUNCTION hold_vocabulary_json(text);
