// Package treasury orchestrates the Fiat → BTC purchase → LND deposit pipeline.
//
// TreasuryManager is the single service that ties together:
//   - Qonto (fiat monitoring + SEPA transfers)
//   - Crypto.com (BTC price, OTC deals, withdrawals)
//   - LND (deposit address generation, balance monitoring)
//
// Every public method on TreasuryManager corresponds 1-to-1 with an admin
// action or an automated trigger — making it straightforward to expose a GUI.
//
// Flow (automatic or manual):
//  1. GetFiatBalance → read Qonto balance
//  2. TransferFiatToCryptoCom → SEPA transfer from Qonto to Crypto.com OTC
//  3. RequestOTCDeal → execute an OTC deal against a quote obtained via WebSocket
//  4. WithdrawBTCToLND → withdraw purchased BTC to our LND on-chain address
//  5. PollWithdrawalUntilComplete → poll until withdraw is broadcast
//  6. FundLNDChannel → lock all confirmed BTC into a Lightning channel
package treasury

import (
	"context"
	"fmt"

	"btc-giftcard/internal/lnd"
	"btc-giftcard/internal/otc"
	"btc-giftcard/internal/qonto"
)

// TreasuryManager coordinates the Fiat → BTC → LND workflow.
// All methods are safe to call from the admin API or the treasury_monitor worker.
type TreasuryManager struct {
	qonto  qonto.QontoService
	crypto otc.CryptocomService
	lnd    lnd.LightningClient
	cfg    Config
}

// NewTreasuryManager wires up TreasuryManager with its dependencies.
func NewTreasuryManager(
	qonto qonto.QontoService,
	crypto otc.CryptocomService,
	lnd lnd.LightningClient,
	cfg Config,
) *TreasuryManager {
	return &TreasuryManager{qonto: qonto, crypto: crypto, lnd: lnd, cfg: cfg}
}

// ============================================================================
// Step 1 — Fiat monitoring (Qonto)
// ============================================================================

// GetFiatBalance returns the current authorised balance of our Qonto account
// in cents (e.g. 1_000_000 = 10 000.00 EUR).
func (m *TreasuryManager) GetFiatBalance(ctx context.Context) (int64, error) {
	account, err := m.qonto.GetAccount(ctx)
	if err != nil {
		return 0, fmt.Errorf("treasury: get fiat balance: %w", err)
	}
	return account.AuthorizedBalanceCents, nil
}

// ============================================================================
// Step 2 — Transfer fiat to Crypto.com (Qonto → SEPA)
// ============================================================================

// TransferFiatToCryptoCom initiates a SEPA wire from our Qonto account to the
// Crypto.com OTC IBAN. Returns as soon as Qonto accepts the transfer (status
// will initially be "pending" — confirm later with Qonto's transaction list).
//
// idempotencyKey must be unique per transfer (UUID recommended). Retrying with
// the same key will not create a duplicate transfer.
func (m *TreasuryManager) TransferFiatToCryptoCom(ctx context.Context, amountCents int64, idempotencyKey string) (*qonto.TransferResponse, error) {
	// amountCents → decimal EUR string (e.g. 150000 → "1500.00")
	euros := fmt.Sprintf("%d.%02d", amountCents/100, amountCents%100)
	req := qonto.TransferRequest{
		BeneficiaryIBAN: m.cfg.CryptoComIBAN,
		BeneficiaryName: m.cfg.CryptoComBeneficiaryName,
		Transfer: qonto.TransferDetails{
			BankAccountID: "", // filled at runtime via GetAccount
			Amount:        euros,
			Currency:      "EUR",
			BeneficiaryID: m.cfg.CryptoComBeneficiaryID,
			Reference:     idempotencyKey,
		},
	}
	// The bank_account_id must be the Qonto account UUID, not the IBAN.
	// Fetch it lazily so callers don't need to supply it.
	account, err := m.qonto.GetAccount(ctx)
	if err != nil {
		return nil, fmt.Errorf("treasury: get account for transfer: %w", err)
	}
	req.Transfer.BankAccountID = account.ID
	return m.qonto.SendTransfer(ctx, req)
}

// ============================================================================
// Step 3 — Execute OTC deal (Crypto.com RFQ)
// ============================================================================

// RequestOTCQuote obtains a live BTC_EUR quote from the Crypto.com OTC WebSocket
// channel. It is a thin wrapper around CryptocomService.RequestQuote so that
// workers don't need to import the otc package directly.
//
// amountCents is the fiat amount to exchange (in cents). The quote will request
// a BTC_EUR notional purchase of the equivalent EUR amount.
// clQuoteReqID is the caller-supplied idempotency key for the quote request.
func (m *TreasuryManager) RequestOTCQuote(ctx context.Context, clQuoteReqID string, amountCents int64) (*otc.Quote, error) {
	// Convert cents to decimal EUR string (e.g. 150000 → "1500.00")
	notional := fmt.Sprintf("%d.%02d", amountCents/100, amountCents%100)
	params := otc.RequestQuoteParams{
		ClQuoteReqID: clQuoteReqID,
		FirmQuote:    true,
		LegList: []otc.QuoteLegRequest{
			{
				InstrumentName: "BTC_EUR",
				Side:           "BUY",
				Notional:       notional,
			},
		},
	}
	quote, err := m.crypto.RequestQuote(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("treasury: request OTC quote: %w", err)
	}
	return quote, nil
}

// RequestOTCDeal executes an OTC BTC purchase by sending a deal request against
// an active quote obtained via the user.otc_qr.quotes WebSocket channel.
//
// The caller is responsible for obtaining a Quote via RequestOTCQuote first,
// then copying the QuoteID and exact leg prices into params before calling this
// method. params.ClDealID is the idempotency key — retrying with the same ID is
// safe.
func (m *TreasuryManager) RequestOTCDeal(ctx context.Context, params otc.RequestDealParams) (*otc.Deal, error) {
	return m.crypto.RequestDeal(ctx, params)
}

// ============================================================================
// Step 4 — Withdraw BTC to LND
// ============================================================================

// WithdrawBTCToLND withdraws purchased BTC from Crypto.com to the LND
// on-chain wallet. It generates a fresh bech32 address from LND per withdrawal
// so each deposit can be tracked independently.
//
// amountBTC is the BTC quantity as a string (e.g. "0.01500000").
// clientWdID is your idempotency key.
func (m *TreasuryManager) WithdrawBTCToLND(ctx context.Context, amountBTC string, clientWdID string) (*otc.Withdrawal, error) {
	addr, err := m.lnd.NewAddress(ctx)
	if err != nil {
		return nil, fmt.Errorf("treasury: get LND deposit address: %w", err)
	}
	req := otc.WithdrawalRequest{
		Currency:   "BTC",
		Amount:     amountBTC,
		Address:    addr,
		NetworkID:  "BTC",
		ClientWdID: clientWdID,
	}
	return m.crypto.Withdraw(ctx, req)
}

// GetWithdrawalStatus returns the current state of a withdrawal request,
// including the Bitcoin txid once the transaction has been broadcast.
func (m *TreasuryManager) GetWithdrawalStatus(ctx context.Context, withdrawalID string) (*otc.Withdrawal, error) {
	return m.crypto.GetWithdrawal(ctx, withdrawalID)
}

// ============================================================================
// Step 4b — Fund a Lightning channel with excess on-chain LND balance
// ============================================================================

// FundLNDChannel opens a Lightning channel funded with localAmtSats from the
// LND on-chain wallet.
//
// Bootstrap: the very first channel must be opened manually via lncli so the
// operator can choose the peer deliberately (good connectivity, inbound
// liquidity deal, fee policy agreement). Once at least one channel is open,
// this method auto-discovers the best peer (highest total activity) from
// ListChannels and resolves the P2P address from ListPeers — no config needed.
//
// If no channels exist and LNDChannelPeerPubKey is not configured, the call
// returns (nil, nil) — a no-op. BTC stays safe in the LND on-chain wallet
// and checkAndFundLNDChannel will retry on the next poll tick.
//
// Peer selection (in order of preference):
//  1. Config.LNDChannelPeerPubKey — explicit config always wins.
//  2. Auto-discover: most active peer from existing open channels.
//
// Connection: uses Config.LNDChannelPeerHost if set, otherwise resolves from
// ListPeers. LND keeps persistent connections to all channel peers, so this
// succeeds in the normal case without any config.
//
// localAmtSats is the local funding amount for the new channel.
func (m *TreasuryManager) FundLNDChannel(ctx context.Context, localAmtSats int64) (*lnd.OpenChannelResult, error) {
	pubKey := m.cfg.LNDChannelPeerPubKey
	host := m.cfg.LNDChannelPeerHost

	if pubKey == "" {
		// Auto-discover: pick the most active peer from existing open channels.
		channels, err := m.lnd.ListChannels(ctx)
		if err != nil {
			return nil, fmt.Errorf("treasury: list channels for peer discovery: %w", err)
		}
		if len(channels) == 0 {
			// No channels exist yet — first channel must be opened manually via lncli.
			// BTC remains safe in the LND on-chain wallet; this is a no-op.
			return nil, nil
		}
		// Pick the channel peer with the highest combined activity.
		best := channels[0]
		for _, ch := range channels[1:] {
			if ch.TotalSatsSent+ch.TotalSatsRecv > best.TotalSatsSent+best.TotalSatsRecv {
				best = ch
			}
		}
		pubKey = best.RemotePubKey
	}

	if host == "" {
		// Try to find the peer's live address from currently connected peers.
		// LND keeps persistent connections to all channel peers, so this will
		// succeed unless the peer is temporarily offline — in that case we
		// proceed anyway; OpenChannel will reconnect automatically.
		peers, err := m.lnd.ListPeers(ctx)
		if err == nil {
			for _, p := range peers {
				if p.PubKey == pubKey {
					host = p.Address
					break
				}
			}
		}
	}

	// Connect to the peer if we have an address and are not already connected.
	// ConnectPeer is idempotent — "already connected" is treated as success.
	if host != "" {
		_, err := m.lnd.ConnectPeer(ctx, pubKey, host)
		if err != nil {
			return nil, fmt.Errorf("treasury: connect to channel peer %s: %w", pubKey[:16], err)
		}
	}

	const pushAmtSats = 0 // keep all liquidity on our side
	const targetConf = 6  // ~1 hour confirmation target
	result, err := m.lnd.OpenChannel(ctx, pubKey, localAmtSats, pushAmtSats, targetConf)
	if err != nil {
		return nil, fmt.Errorf("treasury: open channel (%d sats) to %s: %w", localAmtSats, pubKey[:16], err)
	}
	return result, nil
}

// ============================================================================
// LND balance visibility
// ============================================================================

// GetLNDBalance returns a combined snapshot of the LND node's treasury,
// including both on-chain and Lightning channel balances.
func (m *TreasuryManager) GetLNDBalance(ctx context.Context) (*LNDBalance, error) {
	wallet, err := m.lnd.GetWalletBalance(ctx)
	if err != nil {
		return nil, fmt.Errorf("treasury: get wallet balance: %w", err)
	}
	channels, err := m.lnd.GetChannelBalance(ctx)
	if err != nil {
		return nil, fmt.Errorf("treasury: get channel balance: %w", err)
	}
	total := wallet.ConfirmedSats + channels.LocalSats
	return &LNDBalance{
		OnChainConfirmedSats:   wallet.ConfirmedSats,
		OnChainUnconfirmedSats: wallet.UnconfirmedSats,
		LightningLocalSats:     channels.LocalSats,
		LightningRemoteSats:    channels.RemoteSats,
		TotalSats:              total,
	}, nil
}
