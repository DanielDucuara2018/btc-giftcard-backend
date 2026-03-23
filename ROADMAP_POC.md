# BTC Gift Card — POC Testnet Runbook

**Last Updated:** March 2026  
**Goal:** Run a complete end-to-end POC on Bitcoin testnet — from wallet funding to card
creation, redemption via Lightning, and operator node management through the API.

---

## Table of Contents

1. [Environment Strategy](#1-environment-strategy)
2. [LND Setup](#2-lnd-setup)
3. [Running the Backend Stack](#3-running-the-backend-stack)
4. [LND Node Bootstrap](#4-lnd-node-bootstrap)
5. [Funding the Testnet Wallet](#5-funding-the-testnet-wallet)
6. [Opening a Lightning Channel](#6-opening-a-lightning-channel)
7. [End-to-End POC Flow](#7-end-to-end-poc-flow)
8. [Web Application (New Repository)](#8-web-application-new-repository)
9. [Infrastructure Repository](#9-infrastructure-repository)
10. [Roadmap: POC → Production](#10-roadmap-poc--production)

---

## 1. Environment Strategy

| Concern            | Development — localhost                | Development — GCP VM           | Production (GCP)       |
|--------------------|----------------------------------------|--------------------------------|------------------------|
| Runtime            | Docker Compose + `go run` locally      | Docker Compose on GCP VM       | Docker Compose on GCP  |
| Bitcoin network    | **testnet** (neutrino SPV)             | **testnet** (neutrino SPV)     | mainnet (neutrino SPV) |
| LND seed backup    | `--noseedbackup` (auto wallet)         | `--noseedbackup`               | Manual `lncli create`  |
| TLS / macaroons    | Copied to `./lnd-creds/` on host       | Same, paths in `config.toml`   | Same + secret mgmt     |
| Database           | Postgres 17 in Docker                  | Postgres 17 in Docker          | Postgres 17 in Docker  |
| Redis              | Redis 7 in Docker                      | Redis 7 in Docker              | Redis 7 in Docker      |
| Go services        | `go run ./cmd/...` on host             | `go run` or Docker image       | Docker image           |
| Web frontend       | `npm run dev` on host (Vite dev server)| Vite dev server or built image | Built image in Docker  |
| GCP usage          | No                                     | Optional (persistent testnet)  | Always                 |

### Choosing between localhost and GCP for development

**Use localhost when:**
- Rapid iteration on API or frontend code.
- You don't need the LND node to stay online between sessions (neutrino re-syncs quickly).
- No open inbound port is needed (no external Lightning peer connections).

**Use a GCP VM when:**
- You want LND to stay synced and channels to remain open across sessions.
- You need a public IP so external testnet peers can connect to port 9735.
- Running the full stack for a demo or external stakeholder review.
- Sharing a testnet environment across the team.

Switching between the two requires only changing `grpc_host` in `config.toml`:

```toml
# localhost
grpc_host = "localhost"

# GCP VM
grpc_host = "<GCP_EXTERNAL_IP>"
```

Everything else (ports, macaroon paths, scripts) stays the same.

### Docker Compose port map

| Service    | Host port | Purpose                         |
|------------|-----------|---------------------------------|
| postgres   | 5432      | Application DB                  |
| redis      | 6379      | Cache + Redis Streams queue     |
| lnd        | 9735      | P2P Lightning (testnet)         |
| lnd        | 10009     | gRPC API (used by Go services)  |
| lnd        | 8080      | LND REST API (debug only)       |
| api server | 3202      | btc-giftcard HTTP API           |

> **Note:** LND occupies port 8080 for its REST API; the btc-giftcard API server
> binds to **3202** to avoid the conflict.

---

## 2. LND Setup

### Why neutrino (SPV)?

LND is configured with `--bitcoin.node=neutrino`. This means LND connects to the
Bitcoin P2P network as a lightweight client — no local copy of the blockchain is
required. It downloads only block headers and the filters needed to find
transactions relevant to our wallet. This makes testnet startup practical on a
laptop or small GCP VM.

### Testnet vs mainnet toggle

The only flag that changes between environments is `--bitcoin.testnet` /
`--bitcoin.mainnet` in the `docker-compose.yml` `lnd.command` block. All gRPC
calls, macaroon paths, and the application config remain the same.

### TLS certificate and macaroon

LND generates a self-signed TLS certificate and bakes per-capability macaroons on
first boot. The Go services authenticate to LND by presenting:

- `tls.cert` — validates the LND gRPC server identity
- `admin.macaroon` — grants full API access (reduce scope for production)

These files live inside the `lnd_data` Docker named volume. The `lnd-creds-exporter`
service (defined in `docker-compose.yml`) automatically copies them to `./lnd-creds/`
on the host once LND is healthy. All Go services depend on `lnd-creds-exporter`
completing successfully before they start — no manual credential copy is needed.

### `config.toml` LND section

```toml
[lnd]
grpc_host                = "localhost"   # or "169.254.12.5" if running inside Docker
port                     = "10009"
tls_cert_path            = "./lnd-creds/tls.cert"
macaroon_path            = "./lnd-creds/admin.macaroon"
network                  = "testnet"
payment_timeout_seconds  = 30
max_payment_fee_sats     = 100
```

### Networking rules (GCP)

If running on a GCP VM, add firewall rules in the GCP console / Terraform:

| Port  | Protocol | Source    | Purpose                          |
|-------|----------|-----------|----------------------------------|
| 9735  | TCP      | 0.0.0.0/0 | LND P2P — required to receive peers |
| 10009 | TCP      | internal  | gRPC — keep private (VPC only)   |
| 3202  | TCP      | 0.0.0.0/0 | btc-giftcard API (POC only)      |

---

## 3. Running the Backend Stack

### Prerequisites

- Docker + Docker Compose v2
- Go 1.24+
- `lncli` installed locally (optional, for debugging)

### Step 1 — Clone and configure

```bash
git clone <repo-url>
cd btc-giftcard
cp config.toml config.toml.bak   # keep a backup of the defaults
```

### Step 2 — Start infrastructure (Postgres + Redis + LND)

```bash
docker compose up -d postgres redis lnd
```

Wait ~15 seconds for LND to generate its TLS cert and macaroons on first boot:

```bash
docker compose logs -f lnd   # wait until you see "Waiting for chain backend to finish sync"
```

Credentials (`tls.cert` + `admin.macaroon`) are exported automatically by the
`lnd-creds-exporter` container. No manual copy step is required.

### Step 3 — Run database migrations (keep this as test)

```bash
go run ./cmd/migrate
```

### Step 5 — Start the API server (keep this as test)

```bash
go run ./cmd/api
```

The server listens on `:3202`. Verify:

```bash
curl http://localhost:3202/health | jq .
# {"status":"ok"}
```

### Step 6 — Start the workers (separate terminals)

```bash
go run ./cmd/worker/fund_card
go run ./cmd/worker/monitor_tx
```

---

## 4. LND Node Bootstrap

After infrastructure is running, the LND node must sync to the testnet chain
before it can receive funds or open channels. Use the Node API endpoints to
monitor progress.

### Check sync status (keep this as test)

```bash
curl http://localhost:3202/api/node/info | jq .
```

```json
{
  "alias": "...",
  "pub_key": "03abc...",
  "synced_to_chain": true,
  "block_height": 2870000,
  "num_channels": 0,
  "synced_to_graph": false
}
```

Wait until `synced_to_chain` is `true`. On testnet with neutrino this typically
takes 2–10 minutes.

### Get a deposit address (keep this as test)

```bash
curl -X POST http://localhost:3202/api/node/wallet/address | jq .
```

```json
{ "address": "tb1q..." }
```

Save this address — you will use it to receive testnet BTC in the next step.

### Check wallet balance (keep this as test)

```bash
curl http://localhost:3202/api/node/wallet/balance | jq .
```

---

## 5. Funding the Testnet Wallet

Testnet BTC (tBTC) has no real value. Three strategies exist, from simplest to
most powerful.

### Strategy A — LND faucet (recommended first step)

`faucet.lightning.community` is maintained by Lightning Labs specifically for LND
testnet nodes. It opens a channel **directly to your node** (giving you both
on-chain funds and inbound liquidity) and is the purpose-built solution for this
exact setup. Request multiple times if needed.

Requirements: your node must be reachable on port 9735 from the internet (a GCP
VM with the firewall rule, or `ngrok tcp 9735` from localhost).

### Strategy B — Regtest (instant blocks, unlimited coins, no external dependency) 

> **Why not testnet CPU mining?** `generatetoaddress` is a **regtest-only RPC**
> — Bitcoin Core removed it from testnet/mainnet in v0.18. Running it against a
> testnet node produces `bad-fork-prior-to-checkpoint` because the node either
> isn't synced to the current chain tip (blocks would fork before a hardcoded
> checkpoint) or the RPC is simply unavailable. Actual testnet CPU mining
> requires a separate miner binary (`cpuminer`) plus a fully-synced node
> (~600 MB download) — far more effort than just using regtest.

Regtest is a local private Bitcoin chain where you control block production.
`generatetoaddress` was designed for exactly this mode. No sync, no faucets,
no waiting — coins are yours instantly.

The repo ships `docker-compose.regtest.yml` as a Compose override that:
- Adds a `bitcoind` service (regtest + ZMQ) with a healthcheck
- Overrides `lnd` (operator) to use `bitcoind` backend instead of neutrino
- Adds an `lnd_customer` service — a second LND node simulating a customer wallet
- Uses isolated `lnd_data_regtest` / `lnd_data_regtest_customer` volumes so testnet and regtest wallets never collide

**The `lnd_customer` node is required for Lightning redemption tests** — a single
LND instance cannot pay an invoice it issued to itself, since the customer and
the operator are distinct parties.

The simplest way to bootstrap everything is the one-command setup script (keep this as test):

```bash
# Start both LND nodes + bitcoind + app services, fund the operator wallet,
# connect the two LND nodes as peers, open a channel, mine confirmations,
# and copy operator credentials:
./scripts/regtest-setup.sh
```

Or step by step:

```bash
# 1. Start the full stack in regtest mode (credentials exported automatically)
docker compose -f docker-compose.yml -f docker-compose.regtest.yml up -d

# 2. Fund the operator wallet
ADDR=$(curl -s -X POST http://localhost:3202/api/node/wallet/address | jq -r .address)
docker compose -f docker-compose.yml -f docker-compose.regtest.yml \
  exec bitcoind bitcoin-cli -regtest -rpcuser=btc -rpcpassword=btc \
  generatetoaddress 101 "$ADDR"

# 3. Connect operator → customer as Lightning peers
CUSTOMER_PUBKEY=$(docker compose -f docker-compose.yml -f docker-compose.regtest.yml \
  exec -T lnd_customer lncli --network=regtest getinfo | jq -r .identity_pubkey)
curl -X POST http://localhost:3202/api/node/peers \
  -H "Content-Type: application/json" \
  -d "{\"pub_key\":\"$CUSTOMER_PUBKEY\",\"host\":\"gift-card-backend.lnd-customer:9735\"}"

# 4. Open a channel (operator has outbound liquidity to pay customer invoices)
curl -X POST http://localhost:3202/api/node/channels \
  -H "Content-Type: application/json" \
  -d "{\"peer_pub_key\":\"$CUSTOMER_PUBKEY\",\"local_amt_sats\":500000,\"push_amt_sats\":0,\"target_conf\":1}"

# 5. Mine 6 blocks to activate the channel
docker compose -f docker-compose.yml -f docker-compose.regtest.yml \
  exec bitcoind bitcoin-cli -regtest -rpcuser=btc -rpcpassword=btc \
  generatetoaddress 6 "$ADDR"
```

To switch back to testnet, stop the regtest stack and restart without the override:

```bash
docker compose -f docker-compose.yml -f docker-compose.regtest.yml down
docker compose up -d
```

#### Full lifecycle test in regtest (keep this as test)

```bash
# Create a card
CODE=$(curl -s -X POST http://localhost:3202/api/cards \
  -H "Content-Type: application/json" \
  -d '{"fiat_amount_cents":1000,"fiat_currency":"USD","purchase_price_cents":1050,"purchase_email":"test@example.com"}' \
  | jq -r .code)

# Wait for fund_card worker to activate it (~5s), then check balance
SATS=$(curl -s http://localhost:3202/api/cards/$CODE/balance | jq -r .btc_amount_sats)

# Customer generates an invoice on lnd_customer
INVOICE=$(docker compose -f docker-compose.yml -f docker-compose.regtest.yml \
  exec lnd_customer lncli --network=regtest addinvoice --amt $SATS \
  | jq -r .payment_request)

# Redeem via Lightning — operator pays the customer's invoice
curl -X POST http://localhost:3202/api/cards/$CODE/redeem \
  -H "Content-Type: application/json" \
  -d "{\"method\":\"lightning\",\"invoice\":\"$INVOICE\", \"amount_sats\": $SATS}" | jq .

# For on-chain redemption — mine blocks after sending to get confirmations
# docker compose ... exec bitcoind bitcoin-cli ... generatetoaddress 6 "$ADDR"

# Customer channel final balance
CH_CUSTOMER_SATS=$(docker compose -f docker-compose.yml -f docker-compose.regtest.yml \
  exec lnd_customer lncli --network=regtest channelbalance  | jq -r .balance)

# Customer onchain final balance
OC_CUSTOMER_SATS=$(docker compose -f docker-compose.yml -f docker-compose.regtest.yml \
  exec lnd_customer lncli --network=regtest walletbalance   | jq -r .confirmed_balance)
```

### Strategy C — Regtest for CI pipelines

For fully automated integration tests, use the same `docker-compose.regtest.yml`
override with a test script that mines blocks on demand between test steps:

```bash
# In your CI pipeline:
docker compose -f docker-compose.yml -f docker-compose.regtest.yml up -d
# ... run tests, mine blocks as needed between assertions ...
docker compose -f docker-compose.yml -f docker-compose.regtest.yml down -v
```

### Which strategy to use

| Scenario                                        | Strategy |
|-------------------------------------------------|----------|
| Manual POC demo + real external Lightning peers | A (LND faucet, requires public port 9735) |
| Large amounts + dev iteration, no external peers | B (regtest) |
| CI pipelines / automated integration tests      | C (regtest in CI) |
| Testing real Lightning routing with external nodes | Testnet only (Strategy A) |

### Verify funds arrived

```bash
curl http://localhost:3202/api/node/wallet/balance | jq .
# { "confirmed_sats": ..., "unconfirmed_sats": ..., "total_sats": ... }
```

### Minimum recommended balance before opening channels

| Purpose              | Amount (sats) | Approximate tBTC |
|----------------------|---------------|------------------|
| Channel open (1 ch.) | 200,000       | 0.002 tBTC       |
| On-chain fees buffer | 50,000        | 0.0005 tBTC      |
| **Total recommended**| **250,000**   | **0.0025 tBTC**  |

---

## 6. Opening a Lightning Channel

Lightning payments require at least one open channel with sufficient outbound
liquidity. The steps below connect to a well-known testnet hub and open a channel.

### Step 1 — Connect to a testnet peer

Well-known testnet nodes (as of March 2026):

| Alias         | PubKey (short)  | Host                        |
|---------------|-----------------|-----------------------------|
| ACINQ testnet | `0338...`       | `13.248.222.197:9735`       |
| Bitrefill     | `030c...`       | `52.50.244.44:9735`         |

```bash
curl -X POST http://localhost:3202/api/node/peers \
  -H "Content-Type: application/json" \
  -d '{
    "pub_key": "0338f57e4e20abf4d5c86b71b59e995ce2ad1e97f2f7e9e9e99a49d82a9f6b6d7",
    "host": "13.248.222.197:9735"
  }' | jq .
```

```json
{ "pub_key": "0338...", "address": "13.248.222.197:9735" }
```

Verify the connection:

```bash
curl http://localhost:3202/api/node/peers | jq .
```

### Step 2 — Open a channel

```bash
curl -X POST http://localhost:3202/api/node/channels \
  -H "Content-Type: application/json" \
  -d '{
    "peer_pub_key":   "0338f57e4e20abf4d5c86b71b59e995ce2ad1e97f2f7e9e9e99a49d82a9f6b6d7",
    "local_amt_sats": 150000,
    "push_amt_sats":  0,
    "target_conf":    1
  }' | jq .
```

```json
{
  "funding_txid":  "abc123...",
  "output_index":  0,
  "channel_point": "abc123...:0"
}
```

---

## 7. End-to-End POC Flow

With the node synced, funded, and a channel open, you can now test the full gift
card lifecycle.

### A. Create a card

```bash
curl -X POST http://localhost:3202/api/cards \
  -H "Content-Type: application/json" \
  -d '{
    "fiat_amount_cents":    1000,
    "fiat_currency":        "USD",
    "purchase_price_cents": 1050,
    "purchase_email":       "test@example.com"
  }' | jq .
```

```json
{
  "card_id":        "uuid-...",
  "code":           "GIFT-XXXX-YYYY-ZZZZ",
  "btc_amount_sats": 0,
  "status":         "created",
  "created_at":     "2026-03-18T10:00:00Z"
}
```

The `fund_card` worker picks up the job from Redis, fetches the current
tBTC/USD price from Coinbase/CoinGecko, calculates satoshis, checks treasury
balance, and activates the card.

### B. Check the card is funded

```bash
curl http://localhost:3202/api/cards/GIFT-XXXX-YYYY-ZZZZ | jq .
# status should be "active" within ~5s
```

```bash
curl http://localhost:3202/api/cards/GIFT-XXXX-YYYY-ZZZZ/balance | jq .
# { "btc_amount_sats": 12345, "btc_amount": "0.00012345" }
```

### C. Redeem via Lightning

Generate a BOLT11 invoice from any testnet wallet (Phoenix, Mutiny, Zeus, or
`lncli addinvoice --amt <sats> --testnet`), then:

```bash
curl -X POST http://localhost:3202/api/cards/GIFT-XXXX-YYYY-ZZZZ/redeem \
  -H "Content-Type: application/json" \
  -d '{
    "method":  "lightning",
    "invoice": "lntb..."
  }' | jq .
```

```json
{
  "transaction_id":       "uuid-...",
  "method":               "lightning",
  "payment_hash":         "aabbcc...",
  "btc_amount_sats":      12345,
  "remaining_balance_sats": 0,
  "status":               "confirmed"
}
```

### D. Redeem via on-chain (alternative)

```bash
curl -X POST http://localhost:3202/api/cards/GIFT-XXXX-YYYY-ZZZZ/redeem \
  -H "Content-Type: application/json" \
  -d '{
    "method":  "onchain",
    "address": "tb1q..."
  }' | jq .
```

The `monitor_tx` worker tracks the transaction until 6 confirmations, updating
the status from `pending` to `confirmed`.

### E. Treasury check

```bash
curl http://localhost:3202/api/treasury/balance | jq .
# { "available_sats": ... }
```

---

## 8. Web Application (New Repository)

A lightweight frontend is needed to make the POC demonstrable to stakeholders
without using `curl`. This should live in a separate repository, structured as a
**monorepo** — matching the pattern used in `audio_text_frontend` — so the mobile
app can share types, API clients, and business logic with the web app.

**Repository name:** `btc-giftcard-frontend` (suggested)

### Monorepo structure

```
btc-giftcard-frontend/
├── package.json              # npm workspaces root
├── packages/
│   ├── web/                  # React + Vite SPA (same stack as audio_text_frontend)
│   ├── mobile/               # React Native + Expo (later phase)
│   └── shared/               # Types, API client, constants (shared between web + mobile)
└── README.md
```

### Web package stack

| Layer       | Choice                                       | Rationale                                              |
|-------------|----------------------------------------------|--------------------------------------------------------|
| Framework   | **React 18** + **Vite**                      | Same as `audio_text_frontend` — fast HMR, familiar tooling |
| Language    | TypeScript                                   | Shared types with `packages/shared`                    |
| Routing     | `react-router-dom` v6                        | Same as `audio_text_frontend`                          |
| State       | Redux Toolkit + `redux-persist`              | Same as `audio_text_frontend`                          |
| HTTP        | `axios`                                      | Same as `audio_text_frontend`                          |
| Styling     | Tailwind CSS                                 | Minimal boilerplate                                    |
| Wallet      | WebLN (Alby, Zeus web)                       | In-browser Lightning payments                          |
| Dev server  | `vite` (`npm run dev`)                       | HMR on localhost:5173                                  |
| Production  | `vite build` → static files served by Nginx  | Docker image on same GCP VM                            |

### Mobile package stack (later phase)

| Layer      | Choice                                    | Rationale                                         |
|------------|-------------------------------------------|---------------------------------------------------|
| Framework  | **React Native + Expo**                   | Same as `audio_text_frontend/packages/mobile`     |
| Navigation | `@react-navigation`                       | Standard stack navigation                         |
| Wallet     | Native Lightning wallet deep-link (LNURL) | No WebLN in native mobile                         |
| Platforms  | Android + iOS                             | Expo managed workflow                             |

### Shared package

`packages/shared` exports:
```ts
// API client pointing at the btc-giftcard backend
export const apiClient = axios.create({ baseURL: import.meta.env.VITE_API_BASE_URL });

// Typed request/response models mirroring the Go structs
export type CreateCardRequest = { fiat_amount_cents: number; fiat_currency: string; ... };
export type CardResponse      = { card_id: string; code: string; status: string; ... };
```

Both web and mobile import from `@btc-giftcard/shared`, so the API integration
is written once.

### Screens required for POC (web)

1. **Buy Card** — fiat amount selector, currency dropdown, email input → `POST /api/cards`
2. **Card Status** — enter card code → `GET /api/cards/{code}`, polls until `active`
3. **Redeem Card** — card code + Lightning invoice (or BTC address) → `POST /api/cards/{code}/redeem`
4. **Node Dashboard** *(operator)* — node info, wallet balance, channels, peers via `/api/node/*`

### WebLN integration (Lightning UX)

If the user has a WebLN-compatible browser extension (Alby):

```ts
import { requestProvider } from 'webln';

// Pay with one click — no manual invoice copy needed during the demo
const provider = await requestProvider();
await provider.sendPayment(invoice);
```

### Environment variables

```env
# packages/web/.env.development  (localhost)
VITE_API_BASE_URL=http://localhost:3202

# packages/web/.env.production   (GCP VM)
VITE_API_BASE_URL=http://<GCP_EXTERNAL_IP>:3202
```

Switch between localhost and GCP simply by editing the env file — no code changes needed.

---

## 9. Infrastructure Repository

The deployment configuration should live in a separate repository to keep
infrastructure concerns decoupled from application code.

**Repository name:** `btc-giftcard-infra` (suggested)

### Contents

```
btc-giftcard-infra/
├── terraform/
│   ├── main.tf          # GCP provider, project, region
│   ├── vpc.tf           # VPC + subnets
│   ├── firewall.tf      # Ports 9735, 3202 (22 SSH internal only)
│   ├── vm.tf            # e2-standard-2 VM, ubuntu 22.04
│   ├── variables.tf
│   └── outputs.tf       # VM IP, SSH key
├── docker-compose.prod.yml   # Production compose (same services, no --noseedbackup)
├── scripts/
│   ├── bootstrap.sh     # First-time: install Docker, clone repo, start stack
│   ├── deploy.sh        # Pull latest image, restart services
│   └── backup-lnd.sh    # Tar + encrypt lnd_data volume → GCS bucket
└── README.md
```

### GCP VM spec (testnet POC)

| Setting        | Value                  |
|----------------|------------------------|
| Machine type   | `e2-standard-2`        |
| vCPU           | 2                      |
| RAM            | 8 GB                   |
| Disk           | 30 GB SSD              |
| Region         | `europe-west1` (Belgium) |
| OS             | Ubuntu 22.04 LTS       |

> Neutrino downloads ~600 MB of testnet filter data on first sync. 30 GB is
> comfortable for the data + Docker images.

### Key Terraform resources

```hcl
# firewall.tf — allow LND P2P from anywhere, restrict gRPC to internal
resource "google_compute_firewall" "lnd_p2p" {
  name    = "allow-lnd-p2p"
  network = google_compute_network.vpc.name
  allow { protocol = "tcp"; ports = ["9735"] }
  source_ranges = ["0.0.0.0/0"]
}

resource "google_compute_firewall" "api" {
  name    = "allow-api"
  network = google_compute_network.vpc.name
  allow { protocol = "tcp"; ports = ["3202"] }
  source_ranges = ["0.0.0.0/0"]   # tighten to specific IPs for production
}
```

### LND wallet security for production

- Remove `--noseedbackup` from the LND command.
- Run `lncli create` on first boot to set a wallet password and record the 24-word seed.
- Store the seed in a password manager / GCP Secret Manager.
- Use `--wallet-unlock-password-file` to automate unlocking on container restart.

---

## 10. Roadmap: POC → Production

### Phase 0 — POC (current, testnet) ✅ / 🔄

| Step | Task | Status |
|------|------|--------|
| 0.1  | LND gRPC client (pay, onchain, balances) | ✅ Done |
| 0.2  | Node management API (`/api/node/*`) | ✅ Done |
| 0.3  | Card lifecycle (create, fund, redeem) | ✅ Done |
| 0.4  | fund_card + monitor_tx workers | ✅ Done |
| 0.5  | Docker Compose stack (Postgres, Redis, LND) | ✅ Done |
| 0.6  | LND testnet sync + wallet funding | 🔄 Manual step |
| 0.7  | Connect peer + open channel via API | 🔄 Manual step |
| 0.8  | End-to-end test (create → fund → redeem) | 🔄 Todo |
| 0.9  | btc-giftcard-frontend POC (React + Vite web app, monorepo) | 🔄 New repo |
| 0.10 | btc-giftcard-frontend mobile app (React Native + Expo)     | 🔄 Later phase |
| 0.11 | btc-giftcard-infra Terraform (GCP testnet VM)              | 🔄 New repo |

### Phase 1 — MVP (mainnet, manual bank reconciliation)

| Step | Task | Priority |
|------|------|----------|
| 1.1  | Bank account setup (Qonto recommended) | HIGH |
| 1.2  | Semi-manual bank transfer reconciliation UI | HIGH |
| 1.3  | POST /api/cards response includes payment instructions + reference ID | HIGH |
| 1.4  | Email notifications (card funded, redemption confirmed) | MEDIUM |
| 1.5  | Reconciliation worker (LND ↔ DB cross-check for ghost payments) | HIGH |
| 1.6  | PagerDuty alerts on `CRITICAL` log events | MEDIUM |
| 1.7  | Switch LND to mainnet (`--bitcoin.mainnet`) | HIGH |
| 1.8  | Open mainnet channels (ACINQ, Bitrefill, LNBig) | HIGH |
| 1.9  | Remove `--noseedbackup`, secure wallet seed in GCP Secret Manager | HIGH |

### Phase 2 — Automation (Qonto API + Crypto.com OTC)

| Step | Task | Priority |
|------|------|----------|
| 2.1  | Qonto API integration (`internal/qonto`) — automated SEPA reconciliation | HIGH |
| 2.2  | Crypto.com OTC integration (`internal/cryptocom`) — automated BTC purchase | HIGH |
| 2.3  | Treasury auto-refill: balance threshold → trigger OTC buy | MEDIUM |
| 2.4  | treasury_monitor worker (already scaffolded in `cmd/worker/treasury_monitor`) | MEDIUM |
| 2.5  | Admin dashboard (treasury balance, volume charts, reconciliation history) | MEDIUM |

### Phase 3 — Merchant Payments

| Step | Task | Priority |
|------|------|----------|
| 3.1  | Merchant registration API | MEDIUM |
| 3.2  | Direct card-to-merchant Lightning payment (no redemption step) | HIGH |
| 3.3  | LNURL-pay support at merchant endpoints | MEDIUM |
| 3.4  | Virtual card integration | LOW |

---

## Quick Reference — Node API Endpoints

All endpoints are prefixed with `/api`.

### Node info & health

| Method | Path             | Description                    |
|--------|------------------|--------------------------------|
| GET    | `/health`        | Service health check           |
| GET    | `/node/info`     | Alias, pubkey, sync status     |

### Wallet

| Method | Path                    | Description                       |
|--------|-------------------------|-----------------------------------|
| GET    | `/node/wallet/balance`  | On-chain confirmed + unconfirmed  |
| POST   | `/node/wallet/address`  | Generate new deposit address      |

### Channels

| Method | Path                      | Description                          |
|--------|---------------------------|--------------------------------------|
| GET    | `/node/channels/balance`  | Aggregate channel balance            |
| GET    | `/node/channels`          | List open channels                   |
| POST   | `/node/channels`          | Open channel to a connected peer     |

### Peers

| Method | Path           | Description         |
|--------|----------------|---------------------|
| GET    | `/node/peers`  | List peers          |
| POST   | `/node/peers`  | Connect to a peer   |

### Cards

| Method | Path                        | Description                  |
|--------|-----------------------------|------------------------------|
| POST   | `/cards`                    | Create a card                |
| GET    | `/cards/{code}`             | Get card details             |
| GET    | `/cards/{code}/balance`     | Get remaining balance        |
| GET    | `/cards/{code}/validate`    | Validate card code           |
| POST   | `/cards/{code}/redeem`      | Redeem (Lightning / on-chain)|

### Treasury

| Method | Path                 | Description                    |
|--------|----------------------|--------------------------------|
| GET    | `/treasury/balance`  | Available treasury satoshis    |
