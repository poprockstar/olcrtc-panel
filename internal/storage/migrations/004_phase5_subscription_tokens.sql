ALTER TABLE clients ADD COLUMN subscription_token TEXT;

UPDATE clients
SET subscription_token = 'sub_' || lower(hex(randomblob(16)))
WHERE subscription_token IS NULL OR subscription_token = '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_clients_subscription_token
ON clients(subscription_token);
