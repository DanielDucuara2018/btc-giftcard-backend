# BTC Gift Card Service - Implementation Roadmap

**Last Updated:** May 2026  
**Status:** Phase 1 partially complete, Phase 3-4 core done — HTTP API complete, Lightning-only redemption

---

## Vision

> **From gift card to payment instrument.** We're not just building a BTC gift card — we're building a Lightning-native payment network. Today: buy a card, redeem BTC. Tomorrow: spend your card balance directly at merchants, online stores, and point-of-sale terminals — all powered by Lightning Network instant payments.

**Short-term (Months 1-4):** Gift card service with Lightning-first redemption  
**Medium-term (Months 5-8):** Direct merchant payments — spend card balance without redemption  
**Long-term (Year 2+):** Payment ecosystem — virtual cards, NFC payments, merchant network

---

## Executive Summary

This roadmap outlines the implementation plan to transform our MVP into a production-ready, cost-optimized BTC gift card service that evolves into a **Lightning-powered payment platform**. Key improvements include:

- **Cost Reduction:** €2,485 → €637 per 1000 cards (74% reduction)
- **Profit Increase:** €2,515 → €4,363 per 1000 cards (73% increase)
- **Processing Speed:** 30-60 minutes → 1 second (Lightning Network)
- **Automation:** Manual processes → Automated reconciliation & funding
- **Future:** Gift cards become spendable at merchants (Lightning payments)

---

## Current Status (Completed ✅)

### Foundation (original)
- ✅ Exchange price providers (Coinbase, CoinGecko, Bitstamp) — `internal/exchange/`
- ✅ Message queue system (Redis Streams) — `pkg/cache/`, `internal/queue/`
- ✅ Card service with async queue integration — `internal/card/service.go`
- ✅ Documentation: README, API docs, architecture diagrams
- ✅ Infrastructure: PostgreSQL, Redis, Docker Compose

### LND Package (47 unit + 7 integration tests)
- ✅ gRPC client with TLS + macaroon auth — `internal/lnd/client.go`
- ✅ Lightning payments (SendPaymentV2 streaming) — `internal/lnd/lightning.go`
- ✅ On-chain wallet queries (NewAddress, WalletBalance) — `internal/lnd/onchain.go`
- ✅ Treasury queries (ChannelBalance, GetInfo) — `internal/lnd/treasury.go`
- ✅ LND v0.20.1-beta module, Docker container on testnet (neutrino SPV)

### Card Service — Business Logic (`internal/card/service.go`)
- ✅ **CreateCard** with `validateCreateRequest` — currency, amount, email validation
- ✅ **FundCard** — treasury lock + balance check + card activation + revert-on-failure
- ✅ **RedeemCard** — Lightning-only orchestrator (invoice decode → pay → record)
- ✅ **GetCardByCode, GetCardBalance, ValidateCardCode** — read-only API methods
- ✅ **GetTreasuryAvailableBalance** — Redis-cached (10s TTL) for API endpoints
- ✅ Treasury distributed locking (Redis SETNX 5s TTL) + per-card locks (10s TTL)
- ✅ `computeTreasuryBalance` — uncached authoritative balance for write paths
- ✅ `PurchasePriceCents` removed — `ErrInvalidPurchase` removed, `CreateCardRequest` cleaned up
- ✅ 13 sentinel errors, string-based enums, `CreateCardFiatCurrency` (USD/EUR)

### Workers
- ✅ **fund_card worker** — thin adapter delegating to `card.Service.FundCard()`

### Database
- ✅ String-based enums: `CardStatus`, `CardPaymentStatus`, `TransactionType`, `TransactionStatus`
- ✅ Custodial model: no wallet-per-card, cards are balance claims on treasury
- ✅ Redemption fields on transactions table (method, payment_hash, preimage, invoice)
- ✅ CardRepository (Create, GetByCode, GetByID, Update, ListByUserID, GetTotalReservedBalance)
- ✅ TransactionRepository (Create, GetByID, GetByTxHash, ListByCardID, Update)
- ✅ `migrations/000001_initial_schema.up.sql` — full payment + fee columns added
- ✅ `migrations/000001_initial_schema.down.sql` — drops all indexes, tables, and types cleanly
- ✅ `internal/database/model.go` — `Card` struct aligned with new schema (payment fields + fee snapshot)

### Configuration & Infrastructure
- ✅ `config/api.go` — Stripe, Fees, Qonto (login/secret_key/iban/bic/org_slug/webhook), FrontendBaseURL, helper methods
- ✅ `config.toml` — `[stripe]`, `[fees]`, `[qonto]` sections added
- ✅ `.env` — Stripe (sk_test + pk_test), Qonto sandbox, Frontend base URL
- ✅ `.env.example` — complete with all sections
- ✅ Docker env vars renamed `BTC_GIFTCARD_*` → `GIFTER_*` across all compose files
- ✅ `go.mod` / `go.sum` — `github.com/stripe/stripe-go/v82` added

### Fee Management (`internal/fees/calculator.go`)
- ✅ `Method` type (`card` | `bank_transfer`) with `IsValid()`
- ✅ `Breakdown` struct with all atomic fee fields matching DB schema
- ✅ `Calculate(faceValueCents, method, cfg)` — pure function, unit-testable
- ✅ Handles card fees (Stripe % + flat) and bank transfer fees (SEPA only) separately

---

## Phase 1: MVP Launch - Weeks 1-2

**Goal:** Launch minimal viable product — Lightning redemption, hybrid payment intake (card + SEPA)

### 1.1 Payment Integration — Hybrid Strategy (Card Processor + Qonto Direct)

**Priority:** HIGH

#### Payment method selection rationale

Two customer payment methods need to be supported: card payments and bank transfers (SEPA).
They require different infrastructure because they carry different costs and flows:

| Method | Route | Fee | Settlement | Treasury impact |
|---|---|---|---|---|
| Card (Visa/MC) | Card processor → payout → Qonto | 1.4–1.8% + €0.25 | T+1 | Payout batched daily to Qonto |
| Bank transfer (SEPA) | Customer → Qonto directly | €0 (SEPA Instant free with Qonto) | Instant | Funds in Qonto immediately |

**Key insight:** routing SEPA bank transfers through a card processor is both slower (payout delay)
and more expensive (adds 0.8% cap fee). For bank transfers, Qonto is the right endpoint — customers
send directly to the Qonto IBAN with a unique reference code per card.

#### Card processor selection

For card payments, a processor is required. This section provides a verified, up-to-date (May 2026)
deep comparison of all relevant EU-focused options. See also **Decision Point #3** for the final verdict.

##### Fee comparison (verified live rates)

| Provider | EEA consumer card | EEA commercial card | UK card | Non-EEA card | Monthly fee | Dispute fee | SEPA Direct Debit |
|---|---|---|---|---|---|---|---|
| **Stripe** | 1.50% + €0.25 | 1.90% + €0.25 | 2.50% + €0.25 | 3.25% + €0.25 | €0 | €20 | €0.35 flat |
| **Mollie** | 1.80% + €0.25 | 2.90% + €0.25 | — | 3.25% + €0.25 | €0 | n/a | €0.35 flat |
| **PayPlug Starter** | 1.50% + €0.25 | 2.50% + €0.25 | — | 2.90% + €0.25 | **€10** | n/a | n/a |
| **PayPlug Pro** | **1.10% + €0.25** | 2.50% + €0.25 | — | 2.90% + €0.25 | **€30** | n/a | n/a |
| **Adyen** | Interchange++ (~1.2–1.4%) + €0.30 | Same IC++ | IC++ | IC++ | ~€120 min | ~€15 | IC++ |
| **Checkout.com** | Custom (IC++ or flat) | Custom | Custom | Custom | Custom | Custom | Custom |
| **Braintree (PayPal)** | ~1.90% + €0.30 | ~1.90% + €0.30 | ~2.40% + €0.30 | ~3.40% + €0.30 | €0 | $15 | n/a |

> IC++ = interchange plus plus (real interchange cost + acquirer fee + scheme fee) — cheaper at scale,
> complex to predict for budgeting.

##### Break-even: PayPlug Pro vs Mollie

PayPlug Pro saves 0.7% per transaction (1.1% vs 1.8%) but costs €30/month extra.
Break-even: `€30 / 0.007 = €4,285/month` in card volume.
Below ~€4k/month card volume → Mollie is cheaper total. Above → PayPlug Pro wins on fees.
But PayPlug is bank-backed (BPCE group) — see crypto policy below.

##### Cost per €100 card (EEA consumer, standard card)

| Provider | Cost per transaction | Monthly fee amortised (100 cards/mo) |
|---|---|---|
| Stripe | €1.75 | — |
| Mollie | €2.05 | — |
| PayPlug Starter | €1.75 + €0.10 overhead | ≈ €1.85 |
| PayPlug Pro | **€1.35** + €0.30 overhead | **≈ €1.65** |
| Adyen | ≈ €1.50–€1.70 | + setup complexity |

##### Crypto / gift card policy — CRITICAL evaluation

This is the most important criterion for our use case.

| Provider | Gift card policy | Crypto-adjacent policy | Risk level |
|---|---|---|---|
| **Stripe** | ⚠️ **RESTRICTED** — requires prior approval | ⚠️ **RESTRICTED** — Bitcoin exchanges/wallets need Stripe sales approval | 🔴 HIGH — dual restricted category |
| **Mollie** | ✅ No explicit restriction found | ✅ Dutch EMI (DNB), generally permissive for EU fintech | 🟢 LOW |
| **PayPlug** | ⚠️ Unknown | 🔴 BPCE banking group (conservative French bank) | 🟡 MEDIUM-HIGH |
| **Adyen** | ✅ Enterprise onboarding review | ⚠️ Case-by-case review | 🟡 MEDIUM |
| **Checkout.com** | ✅ Explicit Crypto solutions page | ✅ Actively supports crypto businesses | 🟢 LOW |
| **Braintree (PayPal)** | ⚠️ Restricted | 🔴 PayPal explicitly prohibits most crypto services | 🔴 HIGH |

**Stripe restriction detail (verified from Stripe's legal page, July 2025):**  
BTC gift cards touch **two** Stripe restricted categories:
1. `Non-fiat currency and stored value` → `"Pre-loaded payment cards, gift cards, virtual credits"` — **Restricted**
2. `Cryptocurrency` → `"Bitcoin, Ripple, Ethereum... exchanges and wallets"` — **Restricted (Limited availability)**

Stripe restricted ≠ banned. It means Stripe *can* allow it after manual review and approval.
Without explicit approval: account termination risk during any automated compliance review.
With approval from Stripe sales: normal operation, but ongoing compliance monitoring.

**Conclusion:** Stripe CAN work if you contact sales and get explicit written approval before launch.
The risk of using Stripe without approval is a sudden account suspension mid-operation.

##### Sandbox & developer experience

| Provider | Sandbox | Go SDK | Webhook testing | DX rating |
|---|---|---|---|---|
| **Stripe** | ✅ Excellent (CLI, dashboard replay, test cards) | ✅ Official `stripe-go` | ✅ CLI listener, dashboard replay | ⭐⭐⭐⭐⭐ |
| **Mollie** | ✅ Full test environment, test API key | ⚠️ Community `mollie-api-go` | ✅ Test mode sends real webhook | ⭐⭐⭐⭐ |
| **PayPlug** | ✅ Portal sandbox | ❌ REST only, no SDK | ⚠️ Basic | ⭐⭐⭐ |
| **Adyen** | ✅ Test environment | ✅ Official Go SDK | ✅ Test webhooks | ⭐⭐⭐⭐ |
| **Checkout.com** | ✅ Test account available | ❌ REST only | ✅ Webhook tester | ⭐⭐⭐⭐ |
| **Braintree** | ✅ Sandbox | ✅ Official Go SDK | ✅ | ⭐⭐⭐⭐ |

##### Payout schedule to Qonto

| Provider | Default payout | Configurable | Minimum payout |
|---|---|---|---|
| **Stripe** | T+2 (rolling) | T+2 to T+7 depending on country | None |
| **Mollie** | Daily / weekly / monthly (your choice) | ✅ Very flexible | None |
| **PayPlug** | ~T+2 | Limited | None |
| **Checkout.com** | Custom (T+1 to T+2 typical) | ✅ | Custom |

All processors pay out via SEPA to any EU IBAN — Qonto IBAN works fine with all of them.

##### Summary: pros and cons

**Stripe**
- ✅ Cheapest standard EU card rate: 1.5% + €0.25
- ✅ Best developer experience in the industry (CLI, docs, test tools)
- ✅ Official Go SDK with full type coverage
- ✅ No monthly fee
- ⚠️ BTC gift cards = RESTRICTED — requires explicit prior approval from Stripe sales team
- ⚠️ Dual restricted category (stored value + crypto) = heightened compliance monitoring
- ❌ Risk of sudden account suspension if operating without explicit approval

**Mollie**
- ✅ No explicit restriction on gift cards or crypto-adjacent businesses
- ✅ Dutch EMI (DNB-licensed) — strong EU/EEA compliance without crypto prejudice
- ✅ No monthly fee, cancel anytime
- ✅ Flexible daily payout schedule
- ✅ Full sandbox with real webhook delivery in test mode
- ⚠️ EU consumer card fee slightly higher than Stripe: 1.8% vs 1.5% (+0.3%)
- ⚠️ Community Go SDK (not official) — stable but fewer updates than Stripe's

**PayPlug**
- ✅ Cheapest at Pro tier for French consumer cards: 1.1% + €0.25
- ✅ French-regulated, BPCE group backing (strong compliance reputation)
- ✅ Fast onboarding for French businesses
- ❌ €10–€30/month subscription even at low volume
- ❌ BPCE banking group = likely to reject crypto-adjacent businesses on KYB review
- ❌ No Go SDK, weaker webhook infrastructure

**Checkout.com**
- ✅ Explicitly supports crypto businesses (dedicated crypto solutions page)
- ✅ Best option if/when volume exceeds €50k/month
- ✅ Interchange++ pricing can be cheapest at scale
- ❌ Enterprise-only — requires custom pricing negotiation
- ❌ Minimum processing volume (not suitable for startup phase)
- ❌ No public pricing, months to onboard

**Adyen**
- ✅ Cheapest at high volume (IC++ ~1.2-1.4%)
- ✅ Strong EU compliance record
- ❌ €120+/month minimum fees
- ❌ Complex enterprise onboarding (weeks to months)
- ❌ Not suitable until > €50k/month card volume

**Braintree (PayPal)**
- ❌ PayPal explicitly restricts crypto-related products
- ❌ Most expensive EU rate (~1.9% + €0.30)
- ❌ PayPal brand = customer confusion (users think they're paying via PayPal)

##### ✅ Decision: Stripe (with prior sales approval) + Checkout.com at scale

> **DECIDED (May 2026):** Stripe is the card payment processor for this project.
> **Mandatory pre-launch step:** Contact Stripe sales and obtain written approval for the stored-value
> + crypto-adjacent restricted categories before going live. Without this approval, sudden account
> termination risk is real. With approval: normal operation, standard Stripe monitoring.

**Phase 1 (MVP — Stripe):**
- Best-in-class developer experience: CLI, official `stripe-go` SDK, dashboard webhook replay
- EEA consumer card rate: 1.5% + €0.25 — cheapest standard rate of all compared providers
- No monthly fee
- Official Go SDK (`github.com/stripe/stripe-go/v82`) with full type coverage
- Sandbox test cards + CLI listener for local webhook development
- SEPA payout to Qonto IBAN at T+2
- ⚠️ **ACTION REQUIRED before launch:** Contact Stripe sales, explain business model (fiat gift cards
  that redeem BTC value), request written approval for stored-value + cryptocurrency categories

**Phase 3+ (scale — > €50k/month card volume): Checkout.com or Adyen**
- Both support crypto explicitly; negotiate interchange++ pricing
- Migration cost: ~8-12 hours (swap payment provider implementation behind the `Provider` interface)

#### Treasury flow with hybrid approach

```
Card payment (Stripe):
  Customer → Stripe Checkout → checkout.session.completed webhook → activate card
                                              ↓ (daily SEPA payout, T+2)
                                          Qonto account → Crypto.com OTC → BTC → LND

SEPA bank transfer (Qonto direct):
  Customer → Qonto IBAN (unique ref per card) → transaction.created webhook → activate card
                                                        ↓ (already in Qonto, instant)
                                                     Crypto.com OTC → BTC → LND
```

SEPA funds land in Qonto immediately and are available for OTC purchase without any payout delay.
Card payments are batched by Stripe and paid out to Qonto at T+2 (rolling basis).

The `treasury_monitor` worker already handles the Qonto → Crypto.com → LND pipeline.
What's needed is the **payment intake layer** that gates card activation on payment confirmation.

#### Card status flow

```
created (pending_payment)           [BTC amount locked at creation price]
    │
    ├── card payment  → Stripe webhook checkout.session.completed  ─┐
    └── bank transfer → Qonto webhook transaction.created           ─┴→ active (FundCardMessage published)
    │
    └── no payment within 24h → expired (cron job)
```

#### Implementation tasks

##### Step 0 — Pre-launch compliance (BLOCKING)

- [ ] **Obtain Stripe written approval before going live**
  - Contact Stripe sales: explain business model (customers pay EUR fiat → receive BTC gift card)
  - Request explicit approval under `Non-fiat currency and stored value` + `Cryptocurrency` restricted categories
  - Operate on test mode only until written approval is in hand
  - **Estimated effort:** 1-2 days (waiting for Stripe response)

##### Step 1 — Fee management foundation (do this first)

- [x] **Add fee configuration to `config.toml`** ✅ Done
  ```toml
  [fees]
  service_fee_pct     = 2.0    # our service margin (%)
  stripe_fee_pct      = 1.5    # Stripe EEA consumer card fee (%)
  stripe_fee_flat_eur = 0.25   # Stripe flat fee per transaction (€)
  crypto_spread_pct   = 0.16   # Crypto.com OTC spread estimate (%)
  sepa_fee_eur        = 0.0    # SEPA processing fee (€, currently 0 via Qonto)
  payment_expiry_h    = 24     # hours before pending card expires
  ```

- [x] **Implement fee calculator** ✅ Done (`internal/fees/calculator.go`)
  - `Method` type (`card` | `bank_transfer`) with `IsValid()`
  - `Calculate(faceValueCents int64, method Method, cfg ApiConfig) (Breakdown, error)`
  - `Breakdown`: `ServiceFeeCents`, `ProcessorFeeCents`, `ProcessorFeeFlatCents`,
    `CryptoSpreadCents`, `SEPAFeeCents`, `TotalFeeCents`, `NetEURCents`
  - For card: `processorFee = face * stripe_fee_pct/100 + stripe_fee_flat`; flat fee is card-only
  - For bank_transfer: processor fees = 0, `SEPAFeeCents` applied
  - `NetEURCents = FaceValueCents - TotalFeeCents`
  - Unit-testable pure function — no external dependencies

##### Step 2 — Database schema migrations

- [x] **Extend `cards` table** ✅ Done (replaced `ALTER TABLE` approach with full schema in initial migration)
  - `card_payment_status` ENUM type: `pending | paid | failed | expired`
  - New columns: `payment_method`, `payment_reference` (UNIQUE), `payment_status`, `payment_expires_at`,
    `stripe_checkout_url`, `sepa_reference`
  - Fee snapshot columns: `service_fee_cents`, `processor_fee_cents`, `processor_fee_flat_cents`,
    `crypto_spread_cents`, `sepa_fee_cents`, `total_fee_cents`, `stripe_fee_actual_cents` (async reconciliation),
    `btc_price_eur_cents` (locked at creation)
  - Indexes: `idx_cards_payment_status`, `idx_cards_payment_expires_at` (partial, WHERE NOT NULL)
  - `purchase_price_cents` column removed — `fiat_amount_cents` is the gross face value; net is derived
  - Down migration also updated to drop new indexes + `card_payment_status` type

##### Step 3 — Stripe payment integration

- [x] **Add Go dependency** ✅ Done
  ```
  go get github.com/stripe/stripe-go/v82
  ```

- [x] **Implement Stripe client** (`internal/payment/stripe.go`)
  - Interface: `Provider` with `CreateCheckoutSession`, `ConstructEvent`
  - `CreateCheckoutSession(ctx, req CreateCheckoutRequest) (*CheckoutSession, error)`
    - Mode: `payment` (one-time only — no `subscription` or `setup`)
    - `line_items`: one entry per denomination × quantity; a single session supports
      multi-denomination bulk orders (e.g. 2 × €100 + 3 × €50 in one checkout)
    - `metadata`: `{"purchase_email": "..."}` — stored for auditing/receipts only;
      **no card identifiers are stored in Stripe metadata** — the DB is the source
      of truth; cards are looked up by session ID via `GetByStripeSessionID`
    - `success_url`, `cancel_url` from config
    - `expires_at`: now + 24h (Stripe enforces payment deadline)
    - Returns `session.ID` (stored as `payment_reference` on all cards in the order)
      + `session.URL` (hosted checkout URL sent to the frontend)
  - `ConstructEvent(rawBody []byte, sigHeader string) (*Event, error)`
    - Wraps `webhook.ConstructEvent` from `stripe-go` — must receive raw request bytes
    - Returns provider-agnostic `*Event{Type, CheckoutSession: &CheckoutSessionPayload{ID}}`
    - `CheckoutSessionPayload` exposes only the session `ID` — no metadata fields;
      the webhook handler must query the DB to resolve cards from the session ID
  - Sandbox: set `stripe.Key = cfg.Stripe.SecretKey` (prefix `sk_test_` for sandbox)
  - **Estimated effort:** 3-4 hours

- [x] **Add `POST /webhook/stripe` endpoint** ✅ Done (`cmd/api/handlers_payment.go`)

  **Architecture:**
  - Handler (`cardPayment`) is thin: read raw body → `ConstructEvent` → `HandleCheckoutEvent` → `200 OK`
  - All business logic lives in `Service.HandleCheckoutEvent` (`internal/card/service.go`)
  - `CardRepository.UpdatePaymentStatus` executes `UPDATE cards SET payment_status = $2 WHERE payment_reference = $1`
  - `updateCardPaymentStatus` private helper was intentionally omitted — `HandleCheckoutEvent` calls
    `UpdatePaymentStatus` directly in both branches; a pass-through wrapper would add no value

  **Layering:**
  ```
  Handler (handlers_payment.go)     Service (service.go)               Repo (card_repository.go)
  ──────────────────────────────    ────────────────────────────────    ───────────────────────────
  io.ReadAll(r.Body)
  ConstructEvent(raw, sig)       →  HandleCheckoutEvent(ctx, event)
                                      guard: event.CheckoutSession == nil → return nil
                                      switch event.Type:
                                        completed →                    →  GetByStripeSessionID
                                                                          UpdatePaymentStatus(PaymentPaid)
                                                                          Publish FundCardMessage ×N
                                        expired   →                    →  GetByStripeSessionID
                                                                          UpdatePaymentStatus(PaymentExpired)
                                        (other)   → return nil
  w.WriteHeader(200)
  ```

  **Key implementation notes:**
  - `event.CheckoutSession` nil-guard at top of `HandleCheckoutEvent` — prevents panic on unknown event types
  - `payment_reference` is written at card creation; webhook only updates `payment_status`
  - Idempotency: `cards[0].PaymentStatus != PaymentPending` guard on `completed` path
  - Always `200 OK` — Stripe retries on any non-2xx; processing errors are logged, not surfaced
  - `stripeProvider` is passed through `newServer` → `newHandler` → `handler.stripeClient`

##### Step 4 — Qonto SEPA webhook integration

- [ ] **Add `POST /webhook/qonto` endpoint** (`cmd/api/handlers.go`)
  - Verify HMAC-SHA256 signature: `hmac(body, GIFTER_QONTO_WEBHOOK_SECRET)` == `X-Qonto-Signature`
  - Handle `transaction.created` for incoming bank transfers
  - Match `transaction.label` or `transaction.reference` to `cards.sepa_reference`
  - On match + amount >= `face_value_cents`: set `payment_status = "paid"`, publish `FundCardMessage`
  - Idempotent: UNIQUE constraint on `payment_reference`; `transaction.id` stored as reference
  - Handle partial amounts (< face_value): log warning, do NOT activate (customer underpaid)
  - **Estimated effort:** 3 hours

##### Step 5 — Update `POST /api/cards`

- [x] **Remove `PurchasePriceCents` from `CreateCardRequest`** ✅ Done (`internal/card/service.go`)
  - `PurchasePriceCents` was a manually-passed "total including fee" value — now removed
  - `ErrInvalidPurchase` sentinel and its validation check removed
  - `purchase_price_cents` DB column removed; fee columns (`service_fee_cents`, etc.) store the breakdown atomically

- [ ] **Update card creation endpoint** (`cmd/api/handlers.go`)
  - Accept: `fiat_amount_cents` (int, replaces the old `purchase_price_cents` input), `fiat_currency`
    ("EUR" only for now), `payment_method` (`"card"` | `"sepa"`) — default `"card"`
  - Calculate fee breakdown via `fees.Calculate(fiatAmountCents, method, cfg.Fees)`
  - Fetch BTC price → compute `btc_amount_sats = netEURCents / btcPricePerEUR * 1e8`
  - Generate SEPA reference: `BTCGIFT-{YYYYMMDD}-{8 random alphanumeric chars}`
  - If `payment_method == "card"`: create Stripe Checkout Session
  - Create card in DB: `status=created`, `payment_status=pending`, store fee snapshot
  - Response:
    ```json
    {
      "card_code": "XXXX-XXXX-XXXX",
      "face_value_cents": 10000,
      "btc_amount_sats": 95238,
      "fee_breakdown": {
        "service_fee_cents": 200,
        "processor_fee_cents": 175,
        "crypto_spread_cents": 16,
        "total_fee_cents": 391,
        "total_fee_pct": 3.91
      },
      "payment": {
        "method": "card",
        "checkout_url": "https://checkout.stripe.com/...",
        "expires_at": "2026-05-04T12:00:00Z"
      },
      "bank_transfer": {
        "iban": "FR76...",
        "bic": "QNTOFRP1XXX",
        "reference": "BTCGIFT-20260503-A4B7C9D2",
        "amount_eur": "100.00"
      }
    }
    ```
  - **Estimated effort:** 3-4 hours

##### Step 6 — Expiry worker

- [ ] **Add card expiry cron** (`cmd/worker/expire_cards/main.go`)
  - Runs every 15 minutes
  - `UPDATE cards SET payment_status='expired', status='expired' WHERE payment_status='pending' AND payment_expires_at < now()`
  - Log expired card codes for audit
  - **Estimated effort:** 1-2 hours

##### Step 7 — Configuration additions

- [x] **Add to `.env` / `config.toml`** ✅ Done
  ```toml
  [stripe]
  secret_key       = "sk_test_xxx"        # sk_live_xxx after Stripe approval
  public_key       = "pk_test_xxx"
  webhook_secret   = "whsec_xxx"          # from Stripe Dashboard → Webhooks
  success_endpoint = "success?session={CHECKOUT_SESSION_ID}"
  cancel_endpoint  = "cancel"

  [qonto]
  base_url          = "https://thirdparty.qonto.com/v2"
  login             = "xxx"
  secret_key        = "xxx"
  webhook_secret    = "xxx"              # from Qonto Dashboard
  iban              = "FR76..."           # Qonto account IBAN for incoming SEPA
  bic               = "QNTOFRP1XXX"
  organization_slug = "your-company-slug"
  staging_token     = "xxx"             # Qonto sandbox staging token

  [fees]
  service_fee_pct     = 2.0
  stripe_fee_pct      = 1.5
  stripe_fee_flat_eur = 0.25
  crypto_spread_pct   = 0.16
  sepa_fee_eur        = 0.0
  payment_expiry_h    = 24
  ```

- [ ] **Set up Stripe account**
  - Create restricted API key with minimum permissions (Checkout read/write, Webhook read)
  - Register webhook endpoint URL in Stripe Dashboard → Developers → Webhooks
  - Subscribe to events: `checkout.session.completed`, `checkout.session.expired`
  - Download webhook signing secret (`whsec_xxx`) — never the secret key
  - Use `stripe listen --forward-to localhost:8080/webhook/stripe` for local development
  - **Estimated effort:** 30 minutes + Stripe sales approval process

- [x] **Set up Qonto webhook** ✅ Done
  - Qonto sandbox account active (`GIFTER_QONTO_LOGIN`, `GIFTER_QONTO_SECRET_KEY`, `GIFTER_QONTO_IBAN` configured)
  - Staging token configured (`GIFTER_QONTO_STAGING_TOKEN`)
  - Webhook URL registration + `transaction.created` subscription: pending (requires deployed endpoint URL)

### 1.2 Treasury Management - Automated OTC Purchases (Crypto.com)

**Priority:** HIGH  
**Cost Impact:** 0.16% (OTC) vs 3% (fiat onramp)  
**Automation Level:** Fully automatable via Crypto.com Exchange API

- [x] **Set up Crypto.com Exchange account** ✅ Done
  - UAT sandbox configured (`GIFTER_CRYPTOCOM_BASE_URL`, `GIFTER_CRYPTOCOM_API_KEY`, `GIFTER_CRYPTOCOM_SECRET_KEY`)
  - Production credentials also configured (commented out in `.env`)
  - Whitelist treasury wallet address for withdrawals: pending (requires production LND on-chain address)

- [ ] **Create treasury wallet system**
  - Database table: `treasury_wallets`
    - Fields: wallet_type, address, balance_sats, balance_fiat_cents, last_updated
  - Generate on-chain BTC address for receiving from OTC
  - Encrypt seed/private key with AES-256-GCM
  - Store encrypted key in secure location (consider HSM for production)
  - **Estimated effort:** 6-8 hours

- [x] **Implement balance tracking** ✅ Done
  - `computeTreasuryBalance()` — queries LND (on-chain + channel) minus reserved card balances
  - `GetTreasuryAvailableBalance()` — Redis-cached (10s TTL) for API endpoints
  - `AcquireTreasuryLock()` / `ReleaseTreasuryLock()` — Redis SETNX distributed lock (5s TTL)
  - `InvalidateTreasuryCache()` — bust cache after mutations
  - Per-card Redis lock `card:lock:{code}` (10s TTL) for concurrent redemption safety

- [ ] **Implement automated OTC purchase flow (Crypto.com OTC 2.0 API)**
  - Create `internal/treasury/otc_provider.go`
  - **Step 1 - Fiat deposit to Crypto.com (via Bank API):**
    - Use bank API (Qonto/Revolut/Wise) to send SEPA wire to Crypto.com fiat wallet
    - Crypto.com provides SEPA deposit details via Fiat Wallet API (`openpayd_exchange_sepa`)
    - ⚠️ Fiat deposits CANNOT be initiated via Crypto.com API (must come from bank side)
    - Monitor deposit arrival via `private/user-balance` polling
  - **Step 2 - RFQ (Request for Quote):**
    - `POST private/otc/request-quote` with `{symbol: "BTCEUR", side: "BUY", size: amount}`
    - Response: quote with price, expiry (typically 10 seconds)
  - **Step 3 - Accept deal:**
    - `POST private/otc/request-deal` with `{quote_id: "..."}`
    - BTC credited instantly to exchange wallet
  - **Step 4 - Withdraw BTC to treasury:**
    - `POST private/create-withdrawal` with whitelisted treasury address
    - Monitor withdrawal status
  - **Full cycle time:** ~1-2 business days (SEPA) + instant (OTC buy + withdrawal)
  - **Estimated effort:** 10-12 hours

- [ ] **Implement treasury auto-refill trigger**
  - Monitor treasury balance via `GetTreasuryBalance()`
  - Trigger conditions:
    - Balance < 20% of weekly volume → Normal refill
    - Balance < 10% → Critical refill (immediate)
  - Auto-refill flow:
    1. Calculate refill amount (target: 1 week of expected volume)
    2. Send SEPA wire from bank to Crypto.com (via bank API)
    3. Wait for deposit confirmation (poll Crypto.com balance)
    4. Execute OTC buy (RFQ → Deal)
    5. Withdraw BTC to treasury wallet
  - Slack/email notifications at each step
  - **Estimated effort:** 6-8 hours

- [ ] **Set up treasury monitoring alerts**
  - Email/Slack alert at 20% balance
  - Critical alert at 10% balance
  - Daily balance summary email
  - Webhook integration with Slack
  - Monitor Crypto.com exchange balance separately
  - **Estimated effort:** 3-4 hours

### 1.3 Worker Implementation - Custodial Funding

**Priority:** HIGH  
**Status:** Skeleton exists, TODOs updated for custodial model

- [x] **Implement `cmd/worker/fund_card/main.go`** ✅ Done
  - Worker is a thin adapter: parse message → fetch price → calculate sats → delegate to `card.Service.FundCard()`
  - `Service.FundCard()` handles: treasury lock → balance check → card activation → tx creation → cache invalidation → revert-on-failure
  - Uses `exchange.PriceProvider` (Coinbase/CoinGecko/Bitstamp) for price fetching
  - String-based enums throughout, per-card distributed locking

- [ ] **Add OTC price source to exchange provider**
  - Add `cryptocom_otc` provider to `internal/exchange/provider.go`
  - Use Crypto.com OTC 2.0 RFQ endpoint for indicative quotes
  - Fallback chain: OTC provider → Coinbase → CoinGecko
  - Cache price for 30 seconds (avoid hitting rate limits)
  - **Estimated effort:** 3-4 hours

### 1.4 Post-Payment Reliability

**Priority:** HIGH  
**Context:** LND payments (`RedeemCard` Step 4) are irreversible and external. A DB failure
after payment creates a **ghost payment** — money sent, no DB record, card balance unchanged.
Three layers of defence are implemented or planned:

- [x] **Atomic DB writes with retry** ✅ Done
  - `RedeemCard` Steps 5+6 (INSERT transaction + UPDATE card balance) wrapped in `RunInTx`
  - `retryWithBackoff(3 attempts, 100ms → 200ms → 400ms)` for transient DB errors
  - Idempotency: `UNIQUE` constraints on `payment_hash` and `tx_hash` prevent duplicate records
    if a previously successful commit acknowledgment was lost (network blip)
  - `FundCard` Steps 5+6 (UPDATE card Active + INSERT fund record) also wrapped atomically
  - `ErrTransactionExists` returned by `TransactionRepository.Create` on unique violation
  - `CRITICAL` log emitted with `card_id`, `payment_hash`, `tx_hash`, `amount_sats` if all retries fail

- [ ] **Reconciliation worker — LND ↔ DB cross-check**
  - Create `cmd/worker/reconcile/main.go` + `internal/card/reconcile.go`
  - Run on schedule (every 5 minutes)
  - **Lightning path:** query `lndClient.ListPayments(creationDateStart=lastRunTime)`
    - For each LND payment, look up DB by `payment_hash`
    - Ghost detected → INSERT transaction record + decrement `btc_amount_sats` on card
  - **On-chain path:** cross-check `tx_hash` via LND wallet transaction list
  - Emit `RECONCILE_GHOST_PAYMENT` structured log + alert on every ghost found
  - Idempotent: safe to run multiple times (duplicate writes hit unique constraint cleanly)
  - **Estimated effort:** 8-10 hours

- [ ] **PagerDuty / Opsgenie alerting on CRITICAL log**
  - Wire zap logger to fire a PagerDuty Events API v2 call on any `CRITICAL`-prefixed message
  - Triggers on: `"CRITICAL: payment sent but DB write failed after retries"`
  - PagerDuty incident payload: `card_id`, `card_code`, `payment_hash`/`tx_hash`, `amount_sats`
  - Include runbook link pointing to reconciliation procedure in incident details
  - Resolves automatically when reconciliation worker confirms the ghost is fixed
  - **Options:** PagerDuty Events API v2 (`POST events.pagerduty.com/v2/enqueue`),
    Opsgenie (`POST api.opsgenie.com/v1/alerts`)
  - **Estimated effort:** 2-3 hours

---

### 1.5 Testing & Quality Assurance

- [ ] **Integration tests for full card lifecycle**
  - Test: Payment received → Card funded → Transaction confirmed
  - Test: Insufficient treasury balance handling
  - Test: Concurrent card funding (no double-spend)
  - Test: Transaction timeout and retry
  - **Estimated effort:** 8-10 hours

- [ ] **Load testing**
  - Simulate 100 cards/hour
  - Monitor Redis queue performance
  - Check database query performance
  - Identify bottlenecks
  - **Estimated effort:** 4-6 hours

- [ ] **Security audit**
  - Private key storage review
  - API authentication verification
  - SQL injection testing
  - Rate limiting validation
  - **Estimated effort:** 6-8 hours

---

### 1.7 Fee Management & Transparent Pricing

**Priority:** HIGH — must be implemented before Phase 1.1 payment integration

#### The goal

All costs must be embedded in the card price. A customer buying a **€100 card** sees exactly how
much BTC they will receive **before** they pay. The card price is the face value; the BTC amount
is calculated as face value minus all stacked fees.

#### Fee stack

```
Face value (what customer pays)
    - Service fee             (our margin, e.g., 2.0%)
    - Stripe processing fee   (1.5% + €0.25 for EEA consumer card)
    - Crypto.com OTC spread   (approx. 0.16%)
    ─────────────────────────────────────────────────
    = Net EUR available for BTC purchase

Net EUR / BTC price (locked at card creation) = BTC sats credited to card
```

**Example: €100 card, EEA consumer card payment**

| Component | Amount |
|---|---|
| Face value | €100.00 |
| Service fee (2.0%) | −€2.00 |
| Stripe fee (1.5% + €0.25) | −€1.75 |
| Crypto.com spread (0.16%) | −€0.16 |
| **Net EUR for BTC** | **€96.09** |
| BTC at €95,000/BTC | **≈ 101,147 sats** |

For SEPA bank transfers: Stripe fee is replaced by `sepa_fee_eur = €0.00` (Qonto has no incoming
SEPA fee), so customers get more BTC per euro with bank transfer.

#### Feasibility: yes, fully parameterizable

Stripe's fee per transaction is: `face_value * stripe_fee_pct/100 + stripe_fee_flat_eur`.
All four fee components are deterministic at card creation time. The fee config is loaded from
`config.toml` and snapshot-stored in the `cards.fee_snapshot` JSONB column at creation time.
Changing the config does not retroactively affect existing cards — each card carries its own
immutable fee record.

#### Implementation tasks

- [x] **Fee config section in `config.toml`** (covered in Phase 1.1 Step 1)
  ```toml
  [fees]
  service_fee_pct     = 2.0
  stripe_fee_pct      = 1.5
  stripe_fee_flat_eur = 0.25
  crypto_spread_pct   = 0.16
  sepa_fee_eur        = 0.0
  payment_expiry_h    = 24
  ```

- [x] **Implement `internal/fees/calculator.go`** ✅ Done (covered in Phase 1.1 Step 1)
  - `Method` type, `Breakdown` struct, `Calculate()` pure function
  - SEPA path: skip Stripe fees, apply `sepa_fee_eur` only
  - All amounts in integer cents to avoid float rounding errors

- [ ] **Return fee breakdown in `POST /api/cards` response**
  - Customer sees exact fee breakdown and resulting BTC amount before any payment
  - `fee_breakdown.total_fee_pct` rendered as "You receive X% of your payment as BTC"
  - `fee_breakdown.btc_amount_sats` rendered in both sats and fiat equivalent

- [x] **Fee snapshot columns** ✅ Done (atomic columns chosen over JSONB `fee_snapshot`)
  - DB stores: `service_fee_cents`, `processor_fee_cents`, `processor_fee_flat_cents`,
    `crypto_spread_cents`, `sepa_fee_cents`, `total_fee_cents`, `btc_price_eur_cents`
  - All columns are NOT NULL with DEFAULT 0 — completeness enforced at DB level
  - SQL-aggregatable (unlike JSONB) — enables fee revenue queries without JSON extraction
  - Async reconciliation: `stripe_fee_actual_cents` populated after T+1 settlement

- [ ] **BTC price locking**
  - BTC price is fetched and locked at `POST /api/cards` time, NOT at webhook receipt time
  - Rationale: customer sees and agrees to exact BTC amount before paying
  - Risk: if customer delays 24h and price surges, we honor the locked amount (we absorb the loss)
  - Mitigation: 24h expiry window limits exposure; price drift risk is small for small card values
  - Store locked price in `fee_snapshot.btc_price_per_eur`

- [ ] **Admin visibility**
  - `GET /api/treasury/balance` response should include fee config summary (operator only)
  - Daily fee revenue metric: sum of `total_fee_cents` over paid cards in the last 24h

---

### 1.6 API Security & Access Control

**Priority:** HIGH — operator endpoints are currently unprotected

#### The public API concern

The frontend and backend being publicly accessible is correct architecture — the frontend
needs to call the backend, and exposing the API publicly is unavoidable and safe **as long as
each endpoint is appropriately gated.** The risk is not that the API is public; it is that
some endpoints are not restricted enough.

#### Route classification

| Route | Who can call | Protection |
|---|---|---|
| `POST /webhook/mollie` | Mollie servers only | Mollie signature verification |
| `POST /webhook/qonto` | Qonto servers only | Qonto signature verification |
| `POST /api/cards` | Anyone who just paid | One-time payment token (see below) |
| `POST /api/cards/{code}/redeem` | Card holder (knows the code) | Code is the secret |
| `GET /api/cards/{code}` | Anyone | Code is the secret |
| `GET /api/cards/{code}/balance` | Anyone | Code is the secret |
| `GET /api/cards/{code}/validate` | Anyone | Code is the secret |
| `GET /api/treasury/balance` | Operator only | API key |
| `GET /api/node/*` | Operator only | API key |
| `POST /api/node/*` | Operator only | API key |

#### Implementation tasks

- [ ] **Operator API key middleware** (`internal/middleware/apikey.go`)
  - Read `GIFTER_OPERATOR_API_KEY` from env at startup
  - Middleware checks `Authorization: Bearer <key>` on `/api/node/*` and `/api/treasury/*`
  - Returns `401` on missing or wrong key, `403` if key is present but route is operator-only
  - **Estimated effort:** 1-2 hours (highest priority, closes LND node exposure immediately)

- [ ] **Payment token for `POST /api/cards`**
  - After Mollie `payment.paid` webhook is received, generate a short-lived signed token
    - HMAC-SHA256(secret, `payment_id + card_code + expiry_unix`)
    - TTL: 10 minutes
    - Stored in Redis with `SET payment_token:{token} card_code EX 600`
  - Return token to frontend via the Mollie `redirectURL` as a query param
    (e.g. `https://app.example.com/success?token=xxx`)
  - Frontend calls `POST /api/cards` with `Authorization: Bearer <token>`
  - Middleware validates token, looks up card code from Redis, allows request, deletes token (single-use)
  - For SEPA bank transfer: token is generated by the Qonto webhook handler on reconciliation
  - **Estimated effort:** 3-4 hours

- [ ] **Webhook signature verification**
  - Mollie: verify `X-Mollie-Signature` HMAC header against `GIFTER_MOLLIE_WEBHOOK_SECRET`
  - Qonto: verify request origin against Qonto's published IP allowlist
  - Both webhook endpoints return `200 OK` immediately (Mollie retries on non-2xx)
  - **Estimated effort:** 2 hours

- [ ] **Add to `.env` / `config.toml`**
  ```toml
  [api]
  operator_api_key      = ""   # set in production; blank disables operator routes in dev
  payment_token_secret  = ""   # HMAC key for one-time payment tokens

  [mollie]
  webhook_secret = ""          # from Mollie dashboard
  ```

---

## Phase 2: Automation & Optimization - Month 2

**Goal:** Automate manual processes and reduce operational overhead

### 2.1 Automated Bank Transfer Reconciliation

**Priority:** MEDIUM  
**Cost Impact:** €0-9/month (API costs) vs 30 min/day manual work

- [ ] **Integrate bank API for real-time payment notifications**
  - Create `internal/payment/bank_provider.go` (interface for multiple banks)
  - **If Qonto (recommended):**
    - OAuth 2.0 authentication
    - Trust Crypto.com as beneficiary → enables fully automated SEPA transfers (no SCA)
    - `POST /v2/external_transfers` for automated payouts to trusted beneficiaries
    - `POST /v2/sepa/bulk_transfers` for batch processing (up to 400 per batch)
    - Idempotency via `X-Qonto-Idempotency-Key` header
    - Instant SEPA by default (fallback to standard above threshold)
    - ⚠️ Transfers >€30,000 require at least one attachment
    - Sandbox available via Developer Portal (`X-Qonto-Staging-Token`)
  - **If Revolut Business:**
    - Bearer token auth (JWT), OAuth2, token expires 40 min
    - `POST /pay` endpoint (Company plans only, not Freelancer)
    - Counterparty management: Create, validate account name (CoP/VoP)
    - Webhooks v2: `TransactionCreated`, `TransactionStateChanged` events
    - Webhook retry: 3 times with 10-min intervals
    - ⚠️ Freelancer accounts must use `/payment-drafts` (manual approval)
    - Sandbox + Postman collection available
  - **If Wise Business:**
    - OAuth 2.0, client credentials + user tokens
    - Quote → Recipient → Transfer → Fund flow (4-step process)
    - `POST /v1/transfers` + `POST /v3/profiles/{id}/transfers/{id}/payments`
    - Batch groups: up to 1000 transfers in single funding (`POST /v3/profiles/{id}/batch-groups`)
    - Webhooks: `transfers#state-change`, `balances#credit` events
    - SCA protected for UK/EEA profiles (bypass with mTLS + client credentials)
    - Sandbox: `https://api.wise-sandbox.com/`
  - **Estimated effort:** 8-10 hours

- [ ] **Automated matching system**
  - Parse webhook/callback payload for reference ID
  - Auto-match transfers to cards in database
  - Auto-trigger funding workflow on match
  - Slack notification for unmatched transfers
  - Daily reconciliation report
  - **Estimated effort:** 4-6 hours

- [ ] **Handle edge cases**
  - Partial payments (wait for completion)
  - Overpayments (refund process)
  - Duplicate payments (idempotency)
  - Expired cards (auto-refund after 24h)
  - **Estimated effort:** 6-8 hours

### 2.2 Treasury Analytics Dashboard

**Priority:** MEDIUM

- [ ] **Build internal admin dashboard**
  - Real-time treasury balance display
  - Daily/weekly/monthly volume charts
  - Card funding success rate
  - Average confirmation time
  - Fee spending trends
  - **Estimated effort:** 12-16 hours

- [ ] **Automated reporting**
  - Weekly P&L summary
  - Cost breakdown (fees, OTC, operations)
  - Revenue by currency (EUR, USD, GBP)
  - Customer acquisition metrics
  - **Estimated effort:** 6-8 hours

### 2.3 Customer Experience Improvements

- [ ] **Email notifications**
  - Payment received confirmation
  - Card funding in progress
  - Card ready for redemption
  - Transaction confirmation updates
  - **Estimated effort:** 4-6 hours

- [ ] **Card redemption API**
  - Endpoint: `POST /api/cards/{id}/redeem`
  - Accept user's BTC address
  - Transfer funds from card wallet to user wallet
  - Broadcast transaction
  - Return tx_hash
  - **Estimated effort:** 6-8 hours

- [ ] **Card status tracking**
  - Public endpoint for checking card status
  - No authentication required (use card ID + security code)
  - Show: payment status, funding status, confirmations
  - **Estimated effort:** 3-4 hours

---

## Phase 3: Lightning Network Migration - Month 3-4

**Goal:** Reduce transaction costs by 99% and improve speed to <1 second

**Cost Impact:** €500 (on-chain) → €1 (Lightning) per 1000 cards

### 3.1 Lightning Infrastructure Setup

**Priority:** HIGH (if pursuing Lightning)  
**Prerequisites:** Phase 1 complete and generating revenue

- [x] **Deploy LND node** ✅ Done
  - Docker container with LND v0.18.4-beta on testnet (neutrino SPV backend)
  - Named volume `lnd_data` for persistence
  - Go module uses `lnd@v0.20.1-beta` with protobuf replace directive
  - Macaroon authentication + TLS configured
  - Config struct: GRPCHost, Port, TLSCertPath, MacaroonPath, Network, PaymentTimeoutSeconds, MaxPaymentFeeSats

- [ ] **Open Lightning channels**
  - Research hub selection (ACINQ, Bitrefill, LNBig)
  - Open channels with high-liquidity hubs
  - Channel size: 0.05-0.1 BTC per channel
  - Total channels: 3-5 for redundancy
  - **Cost:** €20-30 in channel opening fees (one-time)
  - **Estimated effort:** 4-6 hours

- [ ] **Set up channel monitoring**
  - Monitor channel balance (local vs remote)
  - Alert on low outbound liquidity
  - Automated channel rebalancing (loop out if needed)
  - Channel force-close detection
  - **Estimated effort:** 6-8 hours

### 3.2 Lightning Wallet Integration

- [x] **Replace btcsuite with LND client** ✅ Done
  - Created `internal/lnd/client.go` (gRPC + TLS + macaroon, `LightningClient` interface)
  - `internal/lnd/lightning.go`: `PayInvoice` (SendPaymentV2 streaming), `DecodeInvoice`
  - `internal/lnd/onchain.go`: `NewAddress`, `GetWalletBalance`
  - `internal/lnd/treasury.go`: `GetChannelBalance`, `GetInfo`
  - 47 unit tests + 7 integration tests passing
  - `PaymentResultStatus` enum: Succeeded/Failed/InFlight

- [ ] **Update database schema for custodial model**
  ```sql
  -- Cards are balance claims on treasury. No wallets, no keys, just amounts.
  -- btc_amount_sats tracks remaining balance (decremented on each spend)
  -- Status: created → funding → active → redeemed (when balance = 0)
  -- No redemption_method on cards — each transaction tracks its own method
  
  -- ALREADY DONE: Removed wallet_address, encrypted_priv_key from cards
  -- ALREADY DONE: Added redemption_method, payment_hash, payment_preimage,
  --               lightning_invoice to transactions table
  ```
  - ~~Migration script to remove wallet fields~~ ✅ Done
  - Much simpler and more secure than managing 1000s of private keys
  - **Partial spend model:** Cards can be spent in portions (multiple transactions)
    - Each transaction deducts from `btc_amount_sats`
    - Card stays `active` until balance reaches 0, then becomes `redeemed`
    - Each transaction independently chooses Lightning or on-chain
  - **Estimated effort:** 2-3 hours

- [x] **Update CreateCard for custodial model** ✅ Done
  - `CreateCard(ctx, req)` creates card as a balance claim on treasury
  - No Bitcoin transaction, no wallet generation
  - `validateCreateRequest()` validates currency, fiat amount, purchase price, email
  - `CreateCardFiatCurrency` enum (USD/EUR) with `IsValid()` method
  - Card status starts as `Created`, transitions to `Funding` → `Active` via FundCard

### 3.3 Custodial Treasury System

**Architecture:** OTC (on-chain) → Treasury On-Chain Wallet → Lightning Channels (BTC locked) → Users redeem on-demand

**How it works:**
1. **Receive from OTC:** BTC arrives at treasury on-chain address (example: 2 BTC received)
2. **Split Treasury:**
   - **Lightning Channels:** Lock 1.8 BTC (90%) - for Lightning redemptions
   - **Hot Wallet:** Keep 0.2 BTC (10%) on-chain - for on-chain redemptions
3. **Create Cards:** Database entries with balance claims (NO Bitcoin tx, NO individual wallets)
4. **User Redeems (Lightning):** Pay from Lightning channel balance → User's Lightning wallet
5. **User Redeems (On-Chain):** Send from hot wallet → User's on-chain address

**Important:** 
- Cards are custodial - NO individual wallets created per card
- We hold ALL BTC in OUR treasury (Lightning channels + hot wallet)
- Card creation is just accounting - BTC only moves when user redeems
- Lightning channels can ONLY send Lightning payments (that's why we need hot wallet for on-chain)

- [x] **Implement treasury management system** ✅ Done
  - `computeTreasuryBalance()`: LND on-chain wallet + channel balance - reserved card balances
  - `GetTreasuryAvailableBalance()`: Redis-cached (10s TTL) for API reads
  - `AcquireTreasuryLock()` / `ReleaseTreasuryLock()`: Redis SETNX distributed lock (5s TTL)
  - `InvalidateTreasuryCache()`: Cache busting after mutations
  - Formula: Available = (WalletBalance + ChannelBalance) - TotalReservedBalance
  - Hot wallet + Lightning channel split tracked via LND queries
  - **Why both?** Lightning adoption is growing but not universal. Maximize market reach.
  - **Estimated effort:** 6-8 hours

- [ ] **Automated channel liquidity management**
  - Monitor outbound capacity daily
  - Refill channels from on-chain treasury
  - Use submarine swaps (Lightning Loop) if needed
  - Alert when channels below 20% capacity
  - **Estimated effort:** 8-10 hours

- [ ] **Channel opening automation**
  - Function: `OpenChannel(nodeID, amountSats)`
  - Trigger: When outbound liquidity < 10%
  - Source: On-chain treasury wallet
  - Confirmation: Wait for 3 confirmations before using
  - **Estimated effort:** 4-6 hours

### 3.4 Worker Update - Lightning-First Funding

- [ ] **Update `cmd/worker/fund_card/main.go` for hybrid mode**
  - Check card type: Lightning invoice or on-chain address
  - **Lightning path:**
    - Decode invoice
    - Check invoice expiry
    - Send payment via LND `SendPaymentSync()`
    - Update card status on success (instant)
    - Cost: €0.001, Time: 1 second
  - **On-chain fallback:**
    - Use existing on-chain logic
    - Cost: €0.50, Time: 30-60 minutes
  - **Estimated effort:** 6-8 hours

- [ ] **Add Lightning payment monitoring**
  - Subscribe to LND payment updates via gRPC stream
  - Handle payment failures (routing, insufficient liquidity)
  - Retry logic: Try 3 times, then fallback to on-chain
  - Log payment routes for optimization
  - **Estimated effort:** 6-8 hours

### 3.5 Testing Lightning Integration

- [ ] **Testnet validation**
  - Deploy LND on Bitcoin testnet
  - Open channels with testnet faucets
  - Test invoice generation and payment
  - Test failure scenarios (expired invoice, routing failure)
  - **Estimated effort:** 8-10 hours

- [ ] **Mainnet pilot**
  - Start with 10 Lightning cards
  - Monitor success rate
  - Measure actual costs and speed
  - Gather user feedback
  - **Estimated effort:** 4-6 hours + monitoring time

---

## Phase 4: Lightning-First Redemption - Month 5

**Goal:** Default to Lightning redemption with on-chain fallback for maximum adoption

**Strategy:** 90% Lightning (instant, €0.001) + 10% on-chain (30 min, €0.50)
**User Compatibility Analysis (2026):**
- Lightning wallets: Phoenix, Muun, Wallet of Satoshi, BlueWallet (~40% of users)
- Exchange wallets: Coinbase, Binance, Kraken (support Lightning withdrawals only)
- Hardware wallets: Ledger, Trezor (on-chain only) (~20% of users)
- **Reality:** Most users CAN receive Lightning, but many prefer familiar on-chain

### 4.1 Database Schema Updates

- [x] **Move redemption fields to transactions table** ✅ Done
  ```sql
  -- Transactions table now tracks per-spend details:
  -- redemption_method TEXT NULL     — 'lightning' (per transaction)
  -- payment_hash VARCHAR(64) NULL   — Lightning payment identifier
  -- payment_preimage VARCHAR(64) NULL — Lightning proof of payment
  -- lightning_invoice TEXT NULL      — BOLT11 invoice string
  -- tx_hash VARCHAR(64) NULL        — reserved for future use
  ```
  - Each spend creates a new transaction with its own method
  - Cards support partial spends (multiple redeems until balance = 0)
  - `btc_amount_sats` on Card = remaining balance (decremented per spend)

### 4.2 Redemption API Updates

- [x] **Update `POST /api/cards/{id}/redeem` endpoint** ✅ Done
  - `RedeemCard(ctx, req)` accepts Lightning invoice + amount_sats
  - Partial spend support: amount_sats can be less than card balance
  - Creates Transaction record with redemption_method=lightning
  - Deducts amount_sats from card's btc_amount_sats
  - Card stays `Active` until balance = 0, then becomes `Redeemed`
  - Validates Lightning invoice amount
  - Orchestrator: validate → lock → check card → decode invoice → pay → record tx → update balance

- [x] **Lightning redemption** ✅ Done
  - **Lightning path:** `lndClient.PayInvoice()` (SendPaymentV2 streaming, maxFeeSats from config)
  - PaymentResult.Status checked for Succeeded/Failed/InFlight

### 4.3 User Experience - Lightning First

- [ ] **Smart redemption UI with Lightning default**
  - **Default:** Lightning option selected (instant, free)
  - **Alternative:** "Use standard Bitcoin address instead" link (slower, €0.50 fee)
  - Show clear benefits: "⚡ Instant & FREE" vs "🐌 30 min wait + €0.50 fee"
  - QR code generation for Lightning invoices
  - Address validation with real-time feedback
  - **Psychology:** Make Lightning the path of least resistance
  - **Estimated effort:** 10-12 hours

- [ ] **Lightning onboarding flow**
  - Detect if user has Lightning wallet (paste invoice vs address)
  - Recommend Lightning wallets if on-chain chosen: Phoenix (easiest), Muun, Wallet of Satoshi
  - "Try Lightning - Get your BTC instantly!" banner
  - Educational tooltip: "Lightning = Same Bitcoin, instant delivery, no fees"
  - Track redemption method choices (measure Lightning adoption)
  - **Goal:** Push 90%+ to Lightning through UX, not force
  - **Estimated effort:** 6-8 hours

- [ ] **"Don't redeem, spend it!" teaser (pre-Phase 5)**
  - After redemption, show: "Coming soon: Spend your gift card directly at merchants ⚡"
  - Email capture for waitlist: "Be first to spend BTC at your favorite stores"
  - Track interest level (measure demand for payment features)
  - **Purpose:** Educate users that Lightning = payments, not just transfers
  - **Estimated effort:** 2-3 hours

---

## Phase 5: Merchant Payments - Month 6-8

**Goal:** Transform gift cards from a transfer tool into a **payment instrument**

> Instead of: Buy card → Redeem to wallet → Send to merchant  
> We enable: Buy card → **Pay merchant directly** from card balance

**Why this matters:**
- Users keep BTC in our ecosystem (longer retention, more revenue)
- Every payment = a fee opportunity (1-2% merchant fee)
- Merchants get instant settlement via Lightning (no 3-5 day bank wait)
- Positions us as a **payment platform**, not just a gift card shop

### 5.1 Direct Card Payments (Card-to-Merchant Lightning)

**Priority:** HIGH  
**Revenue Impact:** 1-2% per transaction (recurring vs one-time card fee)

- [ ] **Implement partial balance spending**
  - Update database: Cards can have partial redemptions
  - Track transaction history per card (not just one-time redeem)
  - New field: `remaining_balance_sats`
  - Card becomes a **prepaid Lightning wallet**
  - **Estimated effort:** 6-8 hours

- [ ] **Payment API for merchants**
  - Endpoint: `POST /api/cards/{id}/pay`
  - Request: `{ "amount_sats": 50000, "merchant_invoice": "lnbc..." }`
  - Validate card balance >= payment amount
  - Pay merchant's Lightning invoice from our channel
  - Deduct from card balance (partial spend)
  - Return payment confirmation + remaining balance
  - **Estimated effort:** 8-10 hours

- [ ] **Merchant onboarding portal**
  - Merchant registration with Lightning address/node
  - API keys for POS integration
  - Merchant dashboard: payments received, settlement history
  - Fee structure: 1-2% per transaction (competitive vs Visa's 2-3%)
  - **Estimated effort:** 16-20 hours

- [ ] **QR code payment flow**
  - Merchant displays QR code with Lightning invoice
  - User scans QR with our web app using card ID
  - One-tap payment from card balance
  - Instant confirmation for both parties
  - **Estimated effort:** 8-10 hours

### 5.2 Lightning Address Integration

- [ ] **Assign Lightning addresses to cards**
  - Each card gets: `card-{id}@ourgiftcard.com`
  - Cards can RECEIVE Lightning payments (top-up from friends)
  - Cards can SEND Lightning payments (pay merchants)
  - Makes cards feel like real wallets
  - **Estimated effort:** 10-12 hours

- [ ] **LNURL-pay support**
  - Implement LNURL-pay for seamless merchant payments
  - Static QR codes for merchants (no new invoice each time)
  - Support comments/memos on payments
  - **Estimated effort:** 6-8 hours

### 5.3 Multi-Currency Support

- [ ] **Add USD, GBP support**
  - Update database: Support multiple fiat currencies
  - Currency conversion via exchange APIs
  - Display prices in user's local currency
  - Show card balance in both BTC and fiat equivalent
  - **Estimated effort:** 12-16 hours

### 5.4 Marketing & Growth

- [ ] **Referral program**
  - Unique referral codes
  - 5% commission on referred sales
  - Referral dashboard
  - **Estimated effort:** 10-12 hours

- [ ] **Gift card customization**
  - Custom messages on cards
  - Branding options for B2B
  - Scheduled delivery (birthdays, holidays)
  - **Estimated effort:** 12-16 hours

- [ ] **"Pay with BTC Gift Card" badges for merchants**
  - Embeddable payment buttons for websites
  - "We accept BTC Gift Cards" stickers for physical stores
  - Co-marketing with early merchant partners
  - **Estimated effort:** 4-6 hours

---

## Phase 6: Payment Ecosystem - Year 2+

**Goal:** Full payment platform with virtual cards, NFC, and merchant network

> From gift card company → **Lightning payment network**

### 6.1 Virtual Debit Cards

- [ ] **Issue virtual Visa/Mastercard linked to card balance**
  - Partner with card issuer (e.g., Reap, Immersve, or similar crypto-card provider)
  - User links gift card balance to virtual card
  - Spend anywhere Visa/Mastercard is accepted
  - Auto-convert BTC → EUR at point of sale
  - **Estimated effort:** Research + partnership (2-3 months)

### 6.2 NFC Tap-to-Pay

- [ ] **Physical card with NFC chip**
  - Tap-to-pay at Lightning-enabled terminals
  - BoltCard standard (open-source NFC + Lightning)
  - Premium product: physical gift card with NFC
  - **Estimated effort:** Hardware partnership + 40-60 hours development

### 6.3 Merchant Network

- [ ] **Build merchant directory**
  - Map of merchants accepting our gift cards
  - Categories: restaurants, online stores, services
  - Merchant reviews and ratings
  - **Estimated effort:** 20-30 hours

- [ ] **Merchant SDK / plugins**
  - WooCommerce plugin for online stores
  - Shopify integration
  - POS integration (Square, SumUp)
  - **Estimated effort:** 40-60 hours

### 6.4 Bulk / B2B Solutions

- [ ] **B2B endpoint for bulk card creation**
  - Endpoint: `POST /api/cards/bulk`
  - Create 10-1000 cards in one request
  - Discount pricing tiers for businesses
  - CSV export of card details
  - Use cases: employee rewards, customer incentives, event giveaways
  - **Estimated effort:** 8-10 hours

- [ ] **Corporate gift card program**
  - White-label gift cards for businesses
  - Custom branding and messaging
  - Analytics dashboard for corporate clients
  - **Estimated effort:** 20-30 hours

### 6.5 Advanced Security

- [ ] **Multi-signature treasury**
  - Require 2-of-3 signatures for large withdrawals
  - Hardware wallet integration (Ledger, Trezor)
  - Separate hot/cold wallet system
  - **Estimated effort:** 16-20 hours

- [ ] **Rate limiting and DDoS protection**
  - Implement Redis-based rate limiting
  - Cloudflare integration
  - API key system for partners
  - **Estimated effort:** 6-8 hours

---

## Cost-Benefit Analysis

### Current MVP (On-Chain Only)
**Per 1000 cards:**
- Revenue: €5,000 (5% fee)
- Costs: €2,485 (Stripe 0.25% + on-chain €0.50)
- Profit: €2,515 (50.3% margin)

### Phase 2 Optimization (Direct Bank + On-Chain)
**Per 1000 cards:**
- Revenue: €5,000
- Costs: €841 (€0 bank + €0.50 on-chain + €341 OTC)
- Profit: €4,159 (83.2% margin)
- **Improvement:** +€1,644 profit (+65%)

### Phase 3 Migration (Direct Bank + Lightning)
**Per 1000 cards:**
- Revenue: €5,000
- Costs: €637 (€0 bank + €0.001 Lightning + €341 OTC + €20 channels)
- Profit: €4,363 (87.3% margin)
- **Improvement:** +€1,848 profit (+73% vs MVP)

---

## Risk Mitigation

### Technical Risks

- **Lightning Network Complexity**
  - Mitigation: Start on testnet, pilot with 10 cards, gradual rollout
  - Fallback: Keep on-chain system as backup

- **Channel Liquidity Issues**
  - Mitigation: Monitor daily, set up automated alerts, maintain hot wallet
  - Fallback: On-chain funding if Lightning fails

- **LND Node Downtime**
  - Mitigation: Hot standby node, automated failover, health checks
  - Fallback: Queue cards until node recovers

### Business Risks

- **Low Volume (Treasury Overinvestment)**
  - Mitigation: Start with €5K treasury, scale based on demand
  - Trigger: Refill only when processing >50 cards/week

- **OTC Provider Delays**
  - Mitigation: 2-3 business day buffer in treasury
  - Minimum treasury: 1 week of expected volume

- **Regulatory Compliance**
  - Mitigation: Consult with crypto-friendly legal advisor
  - KYC/AML: Implement if volume exceeds regulatory thresholds

---

## Decision Points

### Critical Decisions Needed

1. **Lightning Migration Timeline**
   - ⏳ Decision: Proceed with Phase 3 (Month 3-4) or stay on-chain indefinitely?
   - **Recommendation:** Migrate after 500 successful on-chain cards
   - **Criteria:** Revenue > €2,500, operational stability, team capacity

2. **Bank Transfer Provider**
   - ✅ Decision: **Qonto** — French-regulated, API on all plans, SEPA instant, webhook on incoming transactions
   - SEPA bank transfers go **directly to Qonto** — no intermediary processor needed
   - Qonto `transaction.created` webhook triggers card activation

3. **Card Payment Processor**
   - ✅ Decision: **Stripe** — chosen for best-in-class DX, official Go SDK, lowest EEA card rate
   - **⚠️ MANDATORY pre-launch action:** Contact Stripe sales for written approval under:
     1. `Non-fiat currency and stored value` → `"Pre-loaded payment cards, gift cards"` (Restricted)
     2. `Cryptocurrency` → `"Bitcoin exchanges and wallets"` (Restricted, Limited availability)
     - Operate in test/sandbox mode only until written approval is received
     - With approval: standard operation, normal compliance monitoring
     - Without approval: risk of account suspension mid-operation
   - **Rate:** 1.5% + €0.25 for EEA consumer cards (cheapest standard rate of all compared providers)
   - **SDK:** `github.com/stripe/stripe-go/v82` (official, full type coverage)
   - **Webhook:** `POST /webhook/stripe` — `checkout.session.completed` event
   - **Signature:** `stripe.ConstructEvent(rawBody, sig, whsec_xxx)` — SDK built-in HMAC verification
   - **Payout:** T+2 rolling to Qonto IBAN
   - **Scale path:** Checkout.com (explicit crypto support, IC++) when > €50k/month card volume

5. **Redemption Strategy**
   - ⏳ Decision: Lightning-only or Lightning-first with on-chain fallback?
   - **Recommendation:** Lightning-first hybrid (reach 100% of users, push 85-90% to Lightning through UX)
   - **Why not Lightning-only?** Would exclude 20-40% of potential customers (exchange-only users, hardware wallets)
   - **Why not equal treatment?** Lightning is objectively better (instant, free) - make it the default
   - **Criteria:** Adoption metrics (track % choosing Lightning), customer feedback

6. **OTC Provider Selection**
   - ✅ Decision: **Crypto.com OTC** — UAT sandbox + production credentials already configured
   - **Recommendation:** Crypto.com OTC (fully automatable API, RFQ flow, sandbox available)
   - **Comparison:**

     | Feature | Crypto.com OTC 2.0 | Kraken OTC | Binance OTC |
     |---------|-------------------|------------|-------------|
     | API automation | ✅ Full RFQ → Deal flow | ❌ Contact desk | ⚠️ Limited |
     | BTC withdrawal API | ✅ `create-withdrawal` | ✅ API available | ✅ API available |
     | Fiat deposit API | ❌ Must wire from bank | ❌ Must wire from bank | ❌ Must wire from bank |
     | Fiat deposit methods | SEPA, SWIFT, Fedwire, UK FPS | SEPA, SWIFT | SEPA, SWIFT |
     | Sandbox/UAT | ✅ `uat-api.3ona.co` | ❌ No sandbox | ❌ No sandbox |
     | Auth method | HMAC-SHA256 | API key + nonce | HMAC-SHA256 |
     | Quote validity | ~10 seconds | Manual negotiation | Varies |
     | EU compliance | ✅ Licensed | ✅ Licensed | ⚠️ Regulatory pressure |

   - **Criteria:** Full API automation, sandbox for testing, EU regulatory status

---

## Success Metrics

### Phase 1 (MVP Launch)
- ✅ 100 cards created successfully
- ✅ 95% payment success rate
- ✅ Average funding time: <90 minutes
- ✅ Zero treasury overdraft incidents
- ✅ <1% customer support tickets

### Phase 2 (Automation)
- ✅ 90% automated bank Infrastructure)
- ✅ Lightning channels operational with 90% treasury capacity
- ✅ Channel uptime: >99.5%
- ✅ Transaction cost: <€0.01 per redemption
- ✅ Zero failed payments (automatic on-chain fallback)

### Phase 4 (Lightning-First Redemption)
- ✅ **Target:** 85-90% users choose Lightning (through smart UX + free redemption)
- ✅ 10-15% users use on-chain fallback (exchange wallets, hardware wallets)
- ✅ 100% redemption success rate (hybrid system ensures no failures)
- ✅ Average redemption time: <5 seconds (Lightning) or <30 minutes (on-chain)
- ✅ Customer satisfaction: "Instant delivery" vs competitors' 30+ minute wait
- ✅ 20%+ users sign up for "spend at merchants" waitlist

### Phase 5 (Merchant Payments)
- ✅ 10+ merchants onboarded
- ✅ 30% of cards used for payments (not just redemption)
- ✅ Average card lifetime: >2 transactions (partial spending)
- ✅ Merchant settlement time: <5 seconds
- ✅ Payment success rate: >99%

### Phase 6 (Payment Ecosystem)
- ✅ 100+ merchants in network
- ✅ Virtual card integration active
- ✅ 50% of revenue from payment fees (vs card creation fees)
- ✅ Gift card → Payment wallet conversion: 40%+ of users

---

## Resources & Budget

### Infrastructure Costs

**Phase 1 (On-Chain MVP):**
- Server: €10-20/month (DigitalOcean/Hetzner)
- Database: Included in server
- Redis: Included in server
- Total: €10-20/month

**Phase 3 (Lightning):**
- LND Server: €20-40/month (dedicated server)
- Channel opening fees: €20-30 (one-time)
- Channel rebalancing: €5-10/month
- Total: €25-50/month

### Development Time Estimates

- **Phase 1:** 60-80 hours (1.5-2 months part-time)
- **Phase 2:** 40-60 hours (1 month part-time)
- **Phase 3:** 60-80 hours (1.5-2 months part-time)
- **Phase 4:** 40-60 hours (1 month part-time)
- **Phase 5:** 80-100 hours (2-3 months part-time) ← Merchant payments
- **Phase 6:** 120-160 hours (ongoing) ← Payment ecosystem
- **Total (MVP → Payments):** 280-380 hours (7-10 months part-time)

### Treasury Investment

- **Initial:** €5,000 (bootstrap phase)
- **Month 2:** €10,000 (first automated OTC purchase via Crypto.com)
- **Month 3:** €20,000 (scale to 200 cards/week)
- **Month 6:** €50,000+ (institutional volume)

### Revenue Evolution

- **Phase 1-4:** Revenue from 5% card creation fee only
- **Phase 5:** + 1-2% merchant payment fee (recurring revenue per card)
- **Phase 6:** + Virtual card fees, NFC card sales, B2B partnerships
- **Long-term:** Payment fees > Card creation fees (sustainable recurring revenue)

---

---

## Architecture Summary: Custodial Model

### How It Works

**Cards are custodial balance claims, NOT individual wallets:**
fast, cheap redemptions - DEFAULT path)
   - Hot wallet: 10-20% on-chain (for users who need on-chain - FALLBACK path)
2. **Cards are database entries** - Each card has a `balance_sats` field representing their claim
3. **No wallet per card** - We don't generate addresses or private keys for cards
4. **BTC transfers only on redemption:**
   - **Lightning redemption (90% of users):** Pay from Lightning channel balance (instant, €0.001)
   - **On-chain redemption (10% of users):** Send from hot wallet (30 min, €0.50)
   
**Market Reality (2026):**
- Lightning adoption growing but not universal (~40-60% have Lightning wallet capability)
- Exchanges support Lightning withdrawals but users still often deposit to exchange wallets
- Hardware wallet users (security-conscious) prefer on-chain
- **Solution:** Make Lightning the easy path, keep on-chain available
   - Lightning redemption → Pay from Lightning channel balance
   - On-chain redemption → Send from hot wallet

**Card Creation = Accounting, NOT Transaction:**
- User pays €100 → Card created with balance 0.0019 BTC
- No Bitcoin movement yet
- BTC stays in our Lightning channels/hot wallet
- Card is a promise to pay that amount later

**Card Redemption = Actual Bitcoin Transfer:**
- User provides Lightning invoice → We pay from our Lightning channel (instant, €0.001)
- User provides on-chain address → We send from hot wallet (30 min, €0.50)
- Card balance set to 0, marked as redeemed

**Treasury Balance Formula:**
```
Total Treasury = On-Chain + Lightning Channels
Available Balance = Total - Sum(Unredeemed Card Balances)
```

Example:
- Total Treasury: 2 BTC
- Unredeemed cards: 100 cards × 0.0019 BTC = 0.19 BTC
- Available: 2 - 0.19 = 1.81 BTC (can create more cards)

---

## Appendix A: Bank API Comparison

### Full Feature Comparison

| Feature | Qonto | Revolut Business | Wise Business | bunq |
|---------|-------|-----------------|---------------|------|
| **Regulation** | French ACPR | Lithuanian EMI (EU passport) | Multi-jurisdiction EMI | Dutch banking license (DNB) |
| **API availability** | All plans (incl. Basic) | Company plans only for `/pay` | All plans | All plans |
| **Auth method** | OAuth 2.0 + Bearer | Bearer JWT + OAuth2 (40min expiry) | OAuth 2.0 + Client Credentials | API key + request signing |
| **Outgoing SEPA** | ✅ `POST /v2/external_transfers` | ✅ `POST /pay` (Company only) | ✅ Quote→Recipient→Transfer→Fund (4 steps) | ✅ `POST /payment` |
| **Batch payments** | ✅ Up to 400 per batch | ❌ One at a time | ✅ Up to 1000 (Batch Groups) | ✅ Up to 350 (XML) |
| **SEPA instant** | ✅ Default (fallback to standard) | ✅ Supported | ⚠️ Depends on route | ✅ Supported |
| **Webhooks** | ⚠️ Limited (polling recommended) | ✅ v2: `TransactionCreated`, `TransactionStateChanged` | ✅ `transfers#state-change`, `balances#credit` | ✅ `MUTATION`, `PAYMENT` categories |
| **SCA bypass** | ✅ Trusted beneficiaries = no SCA | ⚠️ Company plan only, mTLS optional | ✅ mTLS + client credentials = no SCA | ⚠️ App-based confirmation |
| **Idempotency** | ✅ `X-Qonto-Idempotency-Key` | ✅ `request_id` field | ✅ `customerTransactionId` | ✅ `X-Bunq-Client-Request-Id` |
| **Sandbox** | ✅ Developer Portal | ✅ Available + Postman | ✅ `api.wise-sandbox.com` | ✅ `sandbox.bunq.com` |
| **SDKs** | ❌ REST only | ❌ REST only | ❌ REST only | ✅ Python, Java, C#, PHP |
| **Account balances** | ✅ API | ✅ `GET /accounts` | ✅ `GET /v4/profiles/{id}/balances` | ✅ API |
| **Multi-currency** | ⚠️ EUR-focused | ✅ 30+ currencies | ✅ 50+ currencies, best FX rates | ✅ EUR-focused |
| **Monthly fee** | From €9/month (Basic) | From €0 (Free), €25 (Grow) | From €0 (pay-per-use) | From €8.99/month |
| **Key limitation** | >€30K transfers need attachment | Freelancer plan = payment drafts only | 4-step transfer flow (complex) | Complex auth (request signing) |

### Automation Path for Treasury Refill

**Qonto (Recommended):**
1. One-time: Trust Crypto.com's SEPA beneficiary details → No SCA required for future transfers
2. `POST /v2/external_transfers` with Crypto.com IBAN + amount + idempotency key
3. Instant SEPA (arrives in seconds-minutes) → Crypto.com detects deposit
4. **Result:** Fully automated fiat-to-exchange pipeline, no human intervention

**Revolut Business:**
1. Create counterparty: `POST /counterparty` with Crypto.com bank details
2. `POST /pay` with `account_id`, `receiver.counterparty_id`, `amount`, `currency`, `reference`
3. Webhook notification on `TransactionStateChanged` for confirmation
4. **Result:** Fully automated, but requires Company plan (€25+/month)

**Wise Business:**
1. Create recipient: `POST /v1/accounts` with Crypto.com bank details
2. Create quote: `POST /v3/profiles/{id}/quotes` (sourceAmount, EUR→EUR)
3. Create transfer: `POST /v1/transfers` with quote + recipient
4. Fund transfer: `POST /v3/profiles/{id}/transfers/{id}/payments` (type: BALANCE)
5. Track: Subscribe to `transfers#state-change` webhook
6. **Result:** Fully automated but 4-step flow, best for multi-currency (EUR→USD, GBP→EUR)

**bunq:**
1. Create payment: `POST /v1/user/{id}/monetary-account/{id}/payment`
2. Webhook via `notification-filter-url` with `PAYMENT` category
3. **Result:** Fully automated, simple API, but Dutch banking license (strong regulation)

### Recommendation

**Primary: Qonto** — Best for our use case because:
- ✅ Trusted beneficiary = fully automated transfers without SCA (critical for automation)
- ✅ SEPA instant by default (fastest fiat delivery to Crypto.com)
- ✅ API on all plans (no premium plan required for API access)
- ✅ French-regulated (ACPR) — strong EU compliance
- ✅ Batch transfers up to 400 (useful for refunds)
- ✅ Idempotency support (safe retries)
- ⚠️ EUR-focused (fine for EU-based business)

**Secondary: Revolut Business** (if multi-currency needed or already using Revolut):
- ✅ 30+ currencies, good webhook support
- ⚠️ Requires Company plan for API payments (€25+/month)

**Tertiary: Wise Business** (if sending to non-SEPA destinations):
- ✅ Best FX rates, 50+ currencies
- ✅ Ideal if treasury refill involves USD/GBP conversion
- ⚠️ More complex 4-step transfer flow

---

## Appendix B: Automated Treasury Refill Flow

### End-to-End Automation: Bank → Crypto.com → Treasury

```
┌─────────────────────────────────────────────────────────────────────┐
│                    AUTOMATED TREASURY REFILL                        │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  1. TRIGGER: Treasury balance < 20% of weekly volume                │
│     └─ internal/treasury/monitor.go polls every 5 minutes           │
│                                                                     │
│  2. BANK TRANSFER (Qonto API):                                     │
│     └─ POST /v2/external_transfers                                  │
│        ├─ To: Crypto.com SEPA details (trusted beneficiary)         │
│        ├─ Amount: 1 week of expected volume (e.g., €10,000)         │
│        ├─ Reference: "TREASURY-REFILL-{timestamp}"                  │
│        └─ SEPA Instant → arrives in seconds                         │
│                                                                     │
│  3. WAIT FOR DEPOSIT (Crypto.com API):                              │
│     └─ Poll: POST private/user-balance every 60 seconds             │
│        └─ Check EUR balance increase                                │
│                                                                     │
│  4. OTC BUY (Crypto.com OTC 2.0 API):                              │
│     ├─ POST private/otc/request-quote {BTCEUR, BUY, amount}        │
│     ├─ Receive quote (valid ~10 seconds)                            │
│     └─ POST private/otc/request-deal {quote_id}                    │
│        └─ BTC credited instantly to exchange wallet                 │
│                                                                     │
│  5. WITHDRAW BTC (Crypto.com Wallet API):                           │
│     └─ POST private/create-withdrawal                               │
│        ├─ To: Whitelisted treasury wallet address                   │
│        ├─ Amount: BTC purchased                                     │
│        └─ Monitor: Poll withdrawal status                           │
│                                                                     │
│  6. CONFIRM: BTC arrives at treasury wallet                         │
│     └─ Update treasury balance in database                          │
│     └─ Slack notification: "Treasury refilled: +X BTC"              │
│                                                                     │
│  Total time: ~5 min (SEPA instant) to ~1 day (standard SEPA)       │
│  Total fees: ~0.16% OTC + SEPA transfer fee (~€0-1)                │
│  Human intervention: NONE                                           │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### API Authentication Summary

| Service | Auth Method | Token Lifetime | Sandbox |
|---------|------------|----------------|---------|
| Qonto | OAuth 2.0 Bearer | Session-based | `X-Qonto-Staging-Token` |
| Revolut | JWT Bearer + OAuth2 | 40 minutes (refresh available) | Postman + sandbox env |
| Wise | OAuth 2.0 + Client Credentials | Session-based | `api.wise-sandbox.com` |
| bunq | API key + HMAC request signing | Per session | `sandbox.bunq.com` |
| Crypto.com | HMAC-SHA256 | Per request | `uat-api.3ona.co` |

---

## Notes

- This roadmap is subject to change based on user feedback and market conditions
- Prioritize user experience and security over speed of implementation
- Test thoroughly on testnet before any mainnet deployment
- Keep detailed logs of all treasury transactions for accounting
- Stay updated on regulatory requirements for crypto businesses in your jurisdiction
- **Strategic priority:** Every decision should move users toward Lightning Network adoption
- **North star metric:** % of cards used for payments (not just one-time redemption)
- Gift cards are the entry point — Lightning payments are the destination

---

**Next Actions:**
1. Review and approve this roadmap
2. Make decision on Lightning migration timeline
3. Choose bank transfer provider (Qonto recommended — see Appendix A)
4. Choose OTC provider (Crypto.com recommended — see Decision Point #4)
5. Set up business bank account + Crypto.com Exchange account
6. Test automation pipeline in sandboxes (Qonto staging + Crypto.com UAT)
7. Begin Phase 1 implementation
8. Research merchant payment regulations (payment license requirements)
9. Identify 5-10 pilot merchants for Phase 5 (crypto-friendly businesses)
