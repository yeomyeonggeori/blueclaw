CREATE TABLE IF NOT EXISTS morning_briefing_schedule (
  person_id text PRIMARY KEY REFERENCES person(person_id) ON DELETE RESTRICT,
  task_schedule_id text NOT NULL UNIQUE REFERENCES task_schedule(task_schedule_id) ON DELETE RESTRICT
);
