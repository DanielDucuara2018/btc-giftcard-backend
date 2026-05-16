-- Custom enum types for type safety
CREATE TYPE card_status AS ENUM ('created', 'funding', 'active', 'redeemed', 'expired');
CREATE TYPE card_payment_status AS ENUM ('pending', 'paid', 'failed', 'expired');
CREATE TYPE transaction_type AS ENUM ('fund', 'redeem', 'payment');
CREATE TYPE transaction_status AS ENUM ('pending', 'confirmed', 'failed');

-- Cards table: Stores gift card information with custodial Bitcoin wallets
CREATE TABLE IF NOT EXISTS cards (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NULL,                           -- NULL for anonymous purchases, links to users table later
    purchase_email VARCHAR(255) NOT NULL,        -- email for receipt and account claiming
    owner_email VARCHAR(255) NOT NULL,           -- email for card ownership
    code VARCHAR(50) UNIQUE NOT NULL,            -- Redemption code (e.g., GIFT-XXXX-YYYY-ZZZZ)
    btc_amount_sats BIGINT NOT NULL DEFAULT 0,   -- Bitcoin amount in satoshis (1 BTC = 100,000,000 sats)
    fiat_amount_cents BIGINT NOT NULL,           -- Gross face value in EUR cents (what customer pays, e.g. 10000 = €100)
    fiat_currency VARCHAR(3) NOT NULL DEFAULT 'EUR', -- ISO 4217 settlement currency (always EUR for payouts)
    status card_status DEFAULT 'created' NOT NULL,
    payment_method TEXT NOT NULL DEFAULT 'card',     -- 'card' | 'bank_transfer'
    payment_reference TEXT UNIQUE,                   -- Stripe session ID | SEPA reference
    payment_status card_payment_status NOT NULL DEFAULT 'pending',  -- 'pending' | 'paid' | 'failed' | 'expired'
    payment_expires_at TIMESTAMPTZ,                  -- 24h payment window; NULL after payment confirmed
    stripe_checkout_url TEXT,                        -- Stripe hosted checkout URL (card payments only)
    sepa_reference TEXT,                             -- random reference code for bank transfer (SEPA only)
    -- Fee breakdown (estimated at card creation; used to lock btc_amount_sats)
    -- net_eur_cents is derived: fiat_amount_cents - total_fee_cents (not stored)
    service_fee_cents        BIGINT NOT NULL DEFAULT 0,  -- platform service margin
    processor_fee_cents      BIGINT NOT NULL DEFAULT 0,  -- Stripe % fee (0 for bank transfer)
    processor_fee_flat_cents BIGINT NOT NULL DEFAULT 0,  -- Stripe flat fee e.g. €0.25 (0 for bank transfer)
    crypto_spread_cents      BIGINT NOT NULL DEFAULT 0,  -- OTC spread estimate
    sepa_fee_cents           BIGINT NOT NULL DEFAULT 0,  -- future-proof, currently 0 via Qonto
    total_fee_cents          BIGINT NOT NULL DEFAULT 0,  -- sum of all fee columns above
    -- Actual Stripe fee: populated asynchronously after T+1 settlement via balance_transaction.fee.
    -- Does NOT change btc_amount_sats; used only for reconciliation and monitoring.
    stripe_fee_actual_cents  BIGINT NOT NULL DEFAULT 0,
    btc_price_eur_cents      BIGINT NOT NULL DEFAULT 0,  -- BTC/EUR price locked at card creation (audit trail)
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    funded_at TIMESTAMPTZ NULL,                  -- When BTC funding was confirmed
    redeemed_at TIMESTAMPTZ NULL                 -- When card was redeemed by user
);

-- Transactions table: Records all Bitcoin transactions (funding, redemptions, payments)
CREATE TABLE IF NOT EXISTS transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    card_id UUID NOT NULL,
    type transaction_type DEFAULT 'fund' NOT NULL,
    redemption_method TEXT NULL,                 -- 'lightning' or 'onchain' (per transaction)
    tx_hash VARCHAR(64) NULL,                    -- Bitcoin on-chain tx hash (NULL for Lightning)
    payment_hash VARCHAR(64) NULL,               -- Lightning payment hash (NULL for on-chain)
    payment_preimage VARCHAR(64) NULL,           -- Lightning proof of payment (set on success)
    lightning_invoice TEXT NULL,                  -- BOLT11 invoice string (NULL for on-chain)
    from_address VARCHAR(100) NULL,              -- Source Bitcoin address (on-chain)
    to_address VARCHAR(100) NULL,                -- Destination Bitcoin address (on-chain)
    btc_amount_sats BIGINT NOT NULL,             -- Amount in satoshis
    status transaction_status DEFAULT 'pending' NOT NULL,
    confirmations INT NOT NULL DEFAULT 0,        -- Blockchain confirmations (0-6+)
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    broadcast_at TIMESTAMPTZ NULL,               -- When transaction was sent to blockchain
    confirmed_at TIMESTAMPTZ NULL,               -- When transaction received confirmations
    
    CONSTRAINT fk_transactions_card FOREIGN KEY (card_id) REFERENCES cards (id) ON DELETE CASCADE,
    -- Idempotency guards: if a post-payment DB write is retried after a lost commit
    -- acknowledgment, these constraints prevent duplicate records from being inserted.
    CONSTRAINT uq_transactions_tx_hash UNIQUE (tx_hash),
    CONSTRAINT uq_transactions_payment_hash UNIQUE (payment_hash)
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_cards_code ON cards(code);
CREATE INDEX IF NOT EXISTS idx_cards_user_id ON cards(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_cards_purchase_email ON cards(purchase_email) WHERE purchase_email IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_cards_owner_email ON cards(owner_email) WHERE owner_email IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_cards_status ON cards(status);
CREATE INDEX IF NOT EXISTS idx_cards_payment_status ON cards(payment_status);
CREATE INDEX IF NOT EXISTS idx_cards_payment_expires_at ON cards(payment_expires_at) WHERE payment_expires_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_cards_created_at ON cards(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_transactions_card_id ON transactions(card_id);
CREATE INDEX IF NOT EXISTS idx_transactions_tx_hash ON transactions(tx_hash) WHERE tx_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_transactions_status ON transactions(status);
CREATE INDEX IF NOT EXISTS idx_transactions_type ON transactions(type);
CREATE INDEX IF NOT EXISTS idx_transactions_created_at ON transactions(created_at DESC);