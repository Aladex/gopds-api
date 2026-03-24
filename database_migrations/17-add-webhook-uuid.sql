ALTER TABLE auth_user ADD COLUMN webhook_uuid character varying(36) DEFAULT '';
-- Generate UUID for existing users with bot tokens
UPDATE auth_user SET webhook_uuid = gen_random_uuid()::text WHERE bot_token != '';
