#!/usr/bin/env bash
# =============================================================================
# regtest-setup.sh — Bootstrap the full regtest environment in one command.
#
# What this script does:
#   1. Starts the full regtest stack (bitcoind + both LND nodes + app services)
#   2. Waits for both LND nodes to be ready
#   3. Mines 101 blocks → operator wallet (funds + matures coinbase)
#   4. Connects the two LND nodes as peers
#   5. Opens a channel: operator → customer (150k sats local capacity)
#   6. Mines 6 blocks to activate the channel
#   7. Copies operator credentials to ./lnd-creds/
#   8. Prints a summary and a ready-to-use curl example
#
# Usage:
#   ./scripts/regtest-setup.sh
#
# Prerequisites: docker compose v2, curl, jq
# =============================================================================

set -euo pipefail

COMPOSE="docker compose -f docker-compose.yml -f docker-compose.regtest.yml"
API="http://localhost:3202"
CHANNEL_SIZE_SATS=500000   # outbound capacity for the operator → customer channel

log()  { echo "[regtest-setup] $*"; }
ok()   { echo "[regtest-setup] ✓ $*"; }
fail() { echo "[regtest-setup] ✗ $*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# 1. Start the stack
#
# lnd-creds-exporter (defined in docker-compose.yml and overridden for regtest
# in docker-compose.regtest.yml) automatically copies tls.cert + admin.macaroon
# from the lnd_data volume to ./lnd-creds/ once LND is healthy. All Go services
# wait for lnd-creds-exporter to complete before starting.
# ---------------------------------------------------------------------------
log "Starting regtest stack..."
$COMPOSE up -d

# ---------------------------------------------------------------------------
# 2. Wait for bitcoind to be ready, then mine 1 block to exit IBD mode
#
# Bitcoin Core starts in "Initial Block Download" mode whenever the chain tip
# is older than 24 hours. On a fresh regtest chain the genesis block is from
# January 2009, so bitcoind stays in IBD forever — and LND will never report
# synced_to_chain=true while bitcoind is in IBD.
#
# Mining 1 block brings the tip to "now", bitcoind exits IBD, and LND can sync.
# We mine to a throwaway bitcoind wallet address (not the LND wallet address,
# which doesn't exist yet) — this block just acts as the IBD trigger.
# ---------------------------------------------------------------------------
log "Waiting for bitcoind to be ready..."
for i in $(seq 1 30); do
  $COMPOSE exec bitcoind bitcoin-cli -regtest -rpcuser=btc -rpcpassword=btc \
    getblockchaininfo > /dev/null 2>&1 && break
  [[ $i -eq 30 ]] && fail "bitcoind did not become ready within 60s"
  sleep 2
done
ok "bitcoind ready"

log "Mining 1 block to exit IBD mode (genesis block is from 2009)..."
# Bitcoin Core v22+ no longer creates a default wallet automatically.
$COMPOSE exec -T bitcoind bitcoin-cli -regtest -rpcuser=btc -rpcpassword=btc \
  createwallet "default" > /dev/null 2>&1 || true
IBD_ADDR=$($COMPOSE exec -T bitcoind bitcoin-cli -regtest -rpcuser=btc -rpcpassword=btc \
  getnewaddress 2>/dev/null | tr -d '[:space:]')
$COMPOSE exec -T bitcoind bitcoin-cli -regtest -rpcuser=btc -rpcpassword=btc \
  generatetoaddress 1 "$IBD_ADDR" > /dev/null
ok "IBD block mined — bitcoind exited IBD mode"

# ---------------------------------------------------------------------------
# 3. Wait for the API to be healthy
# ---------------------------------------------------------------------------
log "Waiting for API server to be ready..."
for i in $(seq 1 60); do
  if curl -sf "${API}/health" > /dev/null 2>&1; then
    ok "API server is up"
    break
  fi
  [[ $i -eq 60 ]] && fail "API server did not start within 60s"
  sleep 2
done

# ---------------------------------------------------------------------------
# 4. Wait for operator LND to sync
# ---------------------------------------------------------------------------
log "Waiting for operator LND to sync..."
for i in $(seq 1 60); do
  SYNCED=$(curl -sf "${API}/api/node/info" 2>/dev/null | jq -r '.synced_to_chain // false')
  if [[ "$SYNCED" == "true" ]]; then
    ok "Operator LND synced"
    break
  fi
  [[ $i -eq 60 ]] && fail "Operator LND did not sync within 60s — check: docker compose logs lnd"
  sleep 2
done

# ---------------------------------------------------------------------------
# 5. Mine 101 blocks to the operator wallet
# (101 = 100 coinbase maturity blocks + 1 to bring chain tip to "now")
# ---------------------------------------------------------------------------
log "Getting operator deposit address..."
OP_ADDR=$(curl -sf -X POST "${API}/api/node/wallet/address" | jq -r '.address')
[[ -z "$OP_ADDR" ]] && fail "Could not get operator wallet address"
log "Mining 101 blocks to operator: ${OP_ADDR}"
$COMPOSE exec bitcoind bitcoin-cli -regtest -rpcuser=btc -rpcpassword=btc \
  generatetoaddress 101 "$OP_ADDR" > /dev/null
ok "Mined 101 blocks — operator wallet funded"

# Short pause for LND to index the blocks
sleep 3

# ---------------------------------------------------------------------------
# 6. Get customer LND pubkey and connect peers
# ---------------------------------------------------------------------------
log "Getting customer LND identity pubkey..."
CUSTOMER_PUBKEY=$($COMPOSE exec -T lnd_customer \
  lncli --network=regtest getinfo 2>/dev/null | jq -r '.identity_pubkey')
[[ -z "$CUSTOMER_PUBKEY" ]] && fail "Could not get customer LND pubkey"
ok "Customer pubkey: ${CUSTOMER_PUBKEY}"

log "Connecting operator → customer as peers..."
curl -sf -X POST "${API}/api/node/peers" \
  -H "Content-Type: application/json" \
  -d "{\"pub_key\":\"${CUSTOMER_PUBKEY}\",\"host\":\"gift-card-backend.lnd-customer:9735\"}" \
  > /dev/null || true   # ignore "already connected" errors
ok "Peers connected"

# ---------------------------------------------------------------------------
# 7. Open a channel operator → customer
# ---------------------------------------------------------------------------
log "Opening channel (operator → customer, ${CHANNEL_SIZE_SATS} sats)..."
CHAN_RESULT=$(curl -sf -X POST "${API}/api/node/channels" \
  -H "Content-Type: application/json" \
  -d "{\"peer_pub_key\":\"${CUSTOMER_PUBKEY}\",\"local_amt_sats\":${CHANNEL_SIZE_SATS},\"push_amt_sats\":0,\"target_conf\":1}")
FUNDING_TXID=$(echo "$CHAN_RESULT" | jq -r '.funding_txid')
ok "Channel funding tx: ${FUNDING_TXID}"

# ---------------------------------------------------------------------------
# 8. Mine 6 blocks to confirm the channel
# ---------------------------------------------------------------------------
log "Mining 6 blocks to activate channel..."
$COMPOSE exec bitcoind bitcoin-cli -regtest -rpcuser=btc -rpcpassword=btc \
  generatetoaddress 6 "$OP_ADDR" > /dev/null
# ---------------------------------------------------------------------------
# 9. Summary
# ---------------------------------------------------------------------------
WALLET_BALANCE=$(curl -sf "${API}/api/node/wallet/balance" | jq '.')
CHANNEL_BALANCE=$(curl -sf "${API}/api/node/channels/balance" | jq '.')

echo ""
echo "============================================================"
echo "  Regtest environment ready"
echo "============================================================"
echo ""
echo "  Operator wallet balance:"
echo "$WALLET_BALANCE" | jq '.'
echo ""
echo "  Operator channel balance:"
echo "$CHANNEL_BALANCE" | jq '.'
echo ""
echo "  API:  ${API}"
echo ""
echo "  --- Example: full gift card lifecycle ---"
echo ""
echo "  # 1. Create a card"
echo "  curl -X POST ${API}/api/cards -H 'Content-Type: application/json' \\"
echo "    -d '{\"fiat_amount_cents\":1000,\"fiat_currency\":\"EUR\",\"purchase_email\":\"test@example.com\"}' | jq ."
echo ""
echo "  # 2. Generate a customer invoice (replace <SATS> with the card's btc_amount_sats)"
echo "  ${COMPOSE} exec lnd_customer lncli --network=regtest addinvoice --amt <SATS>"
echo ""
echo "  # 3. Redeem the card"
echo "  curl -X POST ${API}/api/cards/<CODE>/redeem -H 'Content-Type: application/json' \\"
echo "    -d '{\"method\":\"lightning\",\"invoice\":\"<BOLT11>\"}' | jq ."
echo ""
echo "  # Mine blocks for on-chain confirmations at any time:"
echo "  ${COMPOSE} exec bitcoind bitcoin-cli -regtest -rpcuser=btc -rpcpassword=btc generatetoaddress 6 ${OP_ADDR}"
echo "============================================================"
