CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE IF NOT EXISTS memory_episode (
  episode_id text PRIMARY KEY,
  source_kind text NOT NULL CHECK (source_kind IN ('task_run', 'explicit', 'import')),
  source_id text NOT NULL,
  requester_person_id text NOT NULL,
  conversation_id text NOT NULL DEFAULT '',
  content text NOT NULL,
  occurred_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (source_kind, source_id)
);

CREATE TABLE IF NOT EXISTS memory_fact (
  fact_id text PRIMARY KEY,
  episode_id text NOT NULL REFERENCES memory_episode (episode_id),
  owner_person_id text NOT NULL CHECK (owner_person_id <> ''),
  subject_person_id text NOT NULL DEFAULT '',
  kind text NOT NULL CHECK (kind IN ('identity', 'preference', 'fact', 'episode', 'temporary')),
  content text NOT NULL CHECK (char_length(content) BETWEEN 1 AND 240),
  embedding_model text NOT NULL DEFAULT '',
  security_level_rank smallint NOT NULL DEFAULT 0,
  required_classes text[] NOT NULL DEFAULT '{}',
  valid_from timestamptz NOT NULL,
  valid_until timestamptz,
  superseded_by text REFERENCES memory_fact (fact_id),
  reinforcement_count integer NOT NULL DEFAULT 1,
  last_recalled_at timestamptz,
  forgotten_at timestamptz,
  forget_reason text,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS memory_fact_circle (
  fact_id text NOT NULL REFERENCES memory_fact (fact_id) ON DELETE CASCADE,
  circle_id text NOT NULL,
  PRIMARY KEY (fact_id, circle_id)
);

CREATE INDEX IF NOT EXISTS memory_fact_circle_circle_idx
  ON memory_fact_circle (circle_id);
CREATE INDEX IF NOT EXISTS memory_fact_content_trgm_idx
  ON memory_fact USING gin (content gin_trgm_ops);
CREATE INDEX IF NOT EXISTS memory_fact_owner_idx
  ON memory_fact (owner_person_id)
  WHERE superseded_by IS NULL AND forgotten_at IS NULL;
CREATE INDEX IF NOT EXISTS memory_fact_subject_idx
  ON memory_fact (subject_person_id)
  WHERE subject_person_id <> '';

CREATE TABLE IF NOT EXISTS memory_profile (
  person_id text PRIMARY KEY,
  identity_lines text[] NOT NULL DEFAULT '{}',
  current_lines text[] NOT NULL DEFAULT '{}',
  built_from_fact_count integer NOT NULL DEFAULT 0,
  built_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS memory_job (
  job_id text PRIMARY KEY,
  kind text NOT NULL CHECK (kind IN ('extract', 'profile', 'reembed', 'import')),
  subject_id text NOT NULL,
  attempts integer NOT NULL DEFAULT 0,
  run_after timestamptz NOT NULL DEFAULT now(),
  locked_until timestamptz,
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS memory_job_pending_idx
  ON memory_job (kind, subject_id)
  WHERE finished_at IS NULL;
CREATE INDEX IF NOT EXISTS memory_job_due_idx
  ON memory_job (run_after)
  WHERE finished_at IS NULL;

DO $$
DECLARE
  vector_version int[];
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'vector') THEN
    RETURN;
  END IF;
  EXECUTE 'CREATE EXTENSION IF NOT EXISTS vector';
  EXECUTE 'CREATE TABLE IF NOT EXISTS memory_fact_embedding (
    fact_id text PRIMARY KEY REFERENCES memory_fact (fact_id) ON DELETE CASCADE,
    embedding vector(1024) NOT NULL
  )';
  SELECT string_to_array(extversion, '.')::int[] INTO vector_version
  FROM pg_extension WHERE extname = 'vector';
  IF vector_version >= ARRAY[0, 5, 0] THEN
    EXECUTE 'CREATE INDEX IF NOT EXISTS memory_fact_embedding_hnsw_idx
      ON memory_fact_embedding USING hnsw (embedding vector_cosine_ops)';
  END IF;
END $$;
