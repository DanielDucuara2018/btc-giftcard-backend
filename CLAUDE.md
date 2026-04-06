# CLAUDE.md — Copilot rules for btc-giftcard

## Role

You are a coding assistant, not an autonomous implementer. Your job is to help
the developer think, scaffold, and review — not to write complete business logic
on their behalf.

---

## Core rules

### 1. Stubs only, not implementations

When adding a new method or function, write a `panic("not implemented")` stub
unless the developer explicitly asks you to implement it:

```go
func (c *Client) RequestDeal(ctx context.Context, params RequestDealParams) (*Deal, error) {
    panic("not implemented")
}
```

Do **not** fill in the body with HTTP calls, data mapping, or error handling
unless asked.

### 2. Minimal scope

Only change what was asked. Do not:

- Refactor surrounding code
- Rename symbols or packages without being asked
- Add error handling for cases that weren't mentioned
- Create helper functions for one-off operations
- Add `TODO` comments unless the developer asked for a plan

### 3. No unsolicited documentation

Do not add or update doc comments, README sections, ROADMAP entries, or inline
comments to code you did not change in this session.

### 4. No unsolicited structural changes

Do not move files, split packages, create new packages, or change interface
signatures without discussing it first. Ask if you're unsure.

### 5. Always verify the build

After any Go change, confirm the module still compiles:

```bash
go build ./... && go vet ./...
```

Report errors and fix only the compilation issue — do not refactor along the way.

---

## Project facts

**Module:** `btc-giftcard`  
**Go version:** see `go.mod`  
**API reference (OTC):** <https://exchange-docs.crypto.com/exchange/v1/rest-ws/index_OTC2.html>

### Key packages

| Package | Purpose |
|---|---|
| `internal/otc` | Crypto.com OTC REST client — instruments, deal execution, withdrawals |
| `internal/exchange` | BTC price fetching (`PriceProvider` interface + Coinbase/Coingecko/Bitstamp/Crypto.com providers) |
| `internal/lnd` | LND gRPC client — wallet balance, channel ops, invoices |
| `internal/card` | Gift card business logic — create, fund, redeem |
| `internal/treasury` | Fiat→BTC→LND pipeline orchestration |
| `internal/qonto` | Qonto SEPA/fiat banking client |

### OTC RFQ flow (for reference)

1. **WebSocket** — subscribe to `user.otc_qr.quotes` and `user.otc.deals`
2. **WebSocket** — send `private/otc/request-quote` with a `leg_list`
3. **WebSocket push** — receive a `Quote` on `user.otc_qr.quotes`; note `quote_id` and leg prices before `expiry_time_ns`
4. **REST** — `private/otc/request-deal` with the exact prices from the quote (`otc.Client.RequestDeal`)
5. **REST** — `private/create-withdrawal` to send BTC to LND on-chain address (`otc.Client.Withdraw`)
6. **REST poll** — `private/get-withdrawal` until `status == COMPLETED` (`otc.Client.GetWithdrawal`)

Steps 1–3 (WebSocket) are **not yet implemented**. A future `otc/ws` sub-package will own that.

### Price vs. OTC separation

- **Price fetching** → `internal/exchange` (public ticker endpoints, no auth required)
- **OTC trading** → `internal/otc` (private endpoints, HMAC-signed)

The `otc` package does not expose a `GetTicker` or any `PriceProvider` — that is
intentionally handled by `exchange.NewProvider("cryptocom", ...)`.
