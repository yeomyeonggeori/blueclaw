ALTER TABLE schedule ADD COLUMN approved_at timestamptz;

UPDATE schedule
SET approved_at = (approved_call ->> 'approvedAt')::timestamptz
WHERE approved_call IS NOT NULL;
