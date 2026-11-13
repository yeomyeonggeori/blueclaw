ALTER TABLE morning_briefing_schedule RENAME TO briefing_schedule;
ALTER TABLE briefing_schedule ADD COLUMN briefing_kind text NOT NULL DEFAULT 'morning';
ALTER TABLE briefing_schedule ALTER COLUMN briefing_kind DROP DEFAULT;
ALTER TABLE briefing_schedule DROP CONSTRAINT morning_briefing_schedule_pkey;
ALTER TABLE briefing_schedule ADD CONSTRAINT briefing_schedule_pkey PRIMARY KEY (person_id, briefing_kind);

DO $$
DECLARE
  renamed record;
BEGIN
  FOR renamed IN
    SELECT conname AS old_name
    FROM pg_constraint
    WHERE conrelid = 'briefing_schedule'::regclass
      AND conname LIKE 'morning\_briefing\_schedule%'
  LOOP
    EXECUTE format(
      'ALTER TABLE briefing_schedule RENAME CONSTRAINT %I TO %I',
      renamed.old_name, replace(renamed.old_name, 'morning_briefing_schedule', 'briefing_schedule')
    );
  END LOOP;
END $$;
