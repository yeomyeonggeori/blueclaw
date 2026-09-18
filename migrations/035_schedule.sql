ALTER TABLE task_schedule RENAME TO schedule;
ALTER TABLE schedule RENAME COLUMN task_schedule_id TO schedule_id;
ALTER TABLE morning_briefing_schedule RENAME COLUMN task_schedule_id TO schedule_id;

DO $$
DECLARE
  renamed record;
BEGIN
  FOR renamed IN
    SELECT conrelid::regclass AS table_name, conname AS old_name
    FROM pg_constraint
    WHERE conrelid IN ('schedule'::regclass, 'morning_briefing_schedule'::regclass)
      AND conname LIKE '%task\_schedule%'
  LOOP
    EXECUTE format(
      'ALTER TABLE %s RENAME CONSTRAINT %I TO %I',
      renamed.table_name, renamed.old_name, replace(renamed.old_name, 'task_schedule', 'schedule')
    );
  END LOOP;

  FOR renamed IN
    SELECT indexname AS old_name
    FROM pg_indexes
    WHERE schemaname = current_schema()
      AND tablename IN ('schedule', 'morning_briefing_schedule')
      AND indexname LIKE '%task\_schedule%'
  LOOP
    EXECUTE format(
      'ALTER INDEX %I RENAME TO %I',
      renamed.old_name, replace(renamed.old_name, 'task_schedule', 'schedule')
    );
  END LOOP;
END $$;
