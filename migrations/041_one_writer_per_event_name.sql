CREATE OR REPLACE FUNCTION one_writer_json(document text) RETURNS jsonb
LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
  RETURN document::jsonb;
EXCEPTION WHEN others THEN
  RETURN NULL;
END
$$;

UPDATE task_event
SET name = 'blueclaw.connector.files_undelivered'
WHERE name = 'agent.failure_report'
  AND one_writer_json(body) ->> 'phase' = 'delivery';

UPDATE task_event
SET name = 'blueclaw.connector.stop_outbox_suppressed'
WHERE name = 'task.stop.outbox_suppressed'
  AND one_writer_json(body) IS NOT NULL
  AND jsonb_typeof(one_writer_json(body)) = 'object';

DROP FUNCTION one_writer_json(text);
