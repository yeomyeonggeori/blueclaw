CREATE TABLE IF NOT EXISTS ledger_part (
  part_hash text PRIMARY KEY,
  body text NOT NULL
);

CREATE TABLE IF NOT EXISTS llm_call (
  llm_call_id text PRIMARY KEY,
  task_run_id text REFERENCES task_run(task_run_id) ON DELETE CASCADE,
  subjects text[] NOT NULL DEFAULT '{}',
  record text NOT NULL,
  request_part text,
  response_part text,
  input_part text,
  part_hashes text[] NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS llm_call_task_run_id_idx ON llm_call(task_run_id);
CREATE INDEX IF NOT EXISTS llm_call_subjects_idx ON llm_call USING gin(subjects);
CREATE INDEX IF NOT EXISTS llm_call_taskless_created_at_idx ON llm_call(created_at) WHERE task_run_id IS NULL;
CREATE INDEX IF NOT EXISTS llm_call_part_hashes_idx ON llm_call USING gin(part_hashes);

ALTER TABLE task_event ADD COLUMN IF NOT EXISTS part_hashes text[];
CREATE INDEX IF NOT EXISTS task_event_part_hashes_idx ON task_event USING gin(part_hashes) WHERE part_hashes IS NOT NULL;

INSERT INTO llm_call (llm_call_id, task_run_id, record, created_at)
SELECT task_event_id, task_run_id, body, created_at
FROM task_event
WHERE name = 'llm.call'
ON CONFLICT (llm_call_id) DO NOTHING;

DELETE FROM task_event WHERE name = 'llm.call';
