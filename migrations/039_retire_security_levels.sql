ALTER TABLE person
  DROP COLUMN IF EXISTS security_level_name,
  DROP COLUMN IF EXISTS security_level_rank,
  DROP COLUMN IF EXISTS granted_classes;

ALTER TABLE policy_channel_rule
  DROP COLUMN IF EXISTS default_security_level_rank,
  DROP COLUMN IF EXISTS default_required_classes;

ALTER TABLE raw_event
  DROP COLUMN IF EXISTS security_level_rank,
  DROP COLUMN IF EXISTS required_classes;

ALTER TABLE attachment
  DROP COLUMN IF EXISTS security_level_rank,
  DROP COLUMN IF EXISTS required_classes;
