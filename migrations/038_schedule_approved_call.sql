ALTER TABLE schedule ADD COLUMN approved_call jsonb;

ALTER TABLE schedule ADD CONSTRAINT schedule_approved_call_runs_once
  CHECK (approved_call IS NULL OR schedule_kind = 'once');
