-- Rollback migration: Drop all tables and types in reverse order

-- Drop indexes first
DROP INDEX IF EXISTS idx_transactions_created_at;
DROP INDEX IF EXISTS idx_transactions_type;
DROP INDEX IF EXISTS idx_transactions_status;
DROP INDEX IF EXISTS idx_transactions_tx_hash;
DROP INDEX IF EXISTS idx_transactions_card_id;
-- Named unique constraints are dropped automatically with DROP TABLE below

DROP INDEX IF EXISTS idx_cards_created_at;
DROP INDEX IF EXISTS idx_cards_status;
DROP INDEX IF EXISTS idx_cards_purchase_email;
DROP INDEX IF EXISTS idx_cards_owner_email;
DROP INDEX IF EXISTS idx_cards_user_id;
DROP INDEX IF EXISTS idx_cards_code;

-- Drop tables
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS cards;

-- Drop custom types
DROP TYPE IF EXISTS transaction_status;
DROP TYPE IF EXISTS transaction_type;
DROP TYPE IF EXISTS card_status;

-- Drop schema migrations table (optional - usually keep this)
-- DROP TABLE IF EXISTS schema_migrations;
