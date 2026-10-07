CREATE OR REPLACE FUNCTION ask_shape_json(document text) RETURNS jsonb
LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
  RETURN document::jsonb;
EXCEPTION WHEN others THEN
  RETURN NULL;
END
$$;

CREATE OR REPLACE FUNCTION ask_one_shape(document jsonb) RETURNS jsonb
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
  stored_kind text := btrim(coalesce(document ->> 'kind', ''));
  current_kind text;
  result jsonb := document;
  options jsonb := CASE WHEN jsonb_typeof(document -> 'options') = 'array' THEN document -> 'options' ELSE '[]'::jsonb END;
  question text := btrim(coalesce(document ->> 'question', ''));
  message text := btrim(coalesce(document ->> 'message', ''));
BEGIN
  current_kind := CASE stored_kind
    WHEN 'confirm' THEN 'ask_confirm'
    WHEN 'choice_single' THEN 'ask_input'
    WHEN 'choice_multiple' THEN 'ask_input'
    WHEN 'input' THEN 'ask_input'
    WHEN 'input_choice' THEN 'ask_input'
    ELSE stored_kind
  END;
  IF current_kind <> '' AND current_kind <> coalesce(document ->> 'kind', '') THEN
    result := result || jsonb_build_object('kind', current_kind);
  END IF;
  IF jsonb_array_length(options) = 0 AND jsonb_typeof(document -> 'choices') = 'array' THEN
    options := coalesce((
      SELECT jsonb_agg(jsonb_build_object('key', position::text, 'label', btrim(choice), 'value', btrim(choice)) ORDER BY position)
      FROM jsonb_array_elements(document -> 'choices') WITH ORDINALITY AS listed(element, position),
           LATERAL (SELECT element #>> '{}' AS choice) AS text_choice
      WHERE jsonb_typeof(element) = 'string' AND btrim(choice) <> ''
    ), '[]'::jsonb);
    IF jsonb_array_length(options) > 0 THEN
      result := result || jsonb_build_object('options', options);
    END IF;
  END IF;
  result := result - 'choices';
  IF current_kind = 'ask_input' AND jsonb_array_length(options) > 0 AND btrim(coalesce(document ->> 'selectionMode', '')) = '' THEN
    result := result || jsonb_build_object('selectionMode', CASE stored_kind WHEN 'choice_multiple' THEN 'multiple' ELSE 'single' END);
  END IF;
  IF question = '' AND message <> '' THEN
    result := result || jsonb_build_object('question', message);
  END IF;
  IF message = '' AND question <> '' THEN
    result := result || jsonb_build_object('message', question);
  END IF;
  RETURN result;
END
$$;

UPDATE task_event
SET body = ask_one_shape(ask_shape_json(body))::text
WHERE name IN ('ask.requested', 'agent.input_requested')
  AND ask_shape_json(body) IS NOT NULL
  AND jsonb_typeof(ask_shape_json(body)) = 'object'
  AND ask_one_shape(ask_shape_json(body)) IS DISTINCT FROM ask_shape_json(body);

DROP FUNCTION ask_one_shape(jsonb);
DROP FUNCTION ask_shape_json(text);
