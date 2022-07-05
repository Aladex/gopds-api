ALTER TABLE auth_user ADD COLUMN bot_token character varying(50) DEFAULT '';
ALTER TABLE auth_user ADD COLUMN telegram_id integer DEFAULT NULL;