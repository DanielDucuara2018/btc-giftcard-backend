package lnd

import (
	"context"
	"errors"
	"fmt"

	"github.com/lightningnetwork/lnd/lnrpc"
)

// SendOnChain sends BTC from LND's on-chain wallet to a destination address.
// targetConf controls fee estimation: 2=next block, 6=~1h (default), 144=~1day.
func (c *Client) SendOnChain(ctx context.Context, address string, amountSats int64, targetConf int32) (*OnChainResult, error) {
	if address == "" {
		return nil, errors.New("address must not be empty")
	}

	// Bitcoin dust limit: outputs below 546 sats are rejected by the network.
	if amountSats < 546 {
		return nil, fmt.Errorf("amount %d is below dust limit (546 sats)", amountSats)
	}

	req := &lnrpc.SendCoinsRequest{
		Addr:       address,
		Amount:     amountSats,
		TargetConf: targetConf,
	}

	resp, err := c.lnClient.SendCoins(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to send on-chain coins: %w", err)
	}

	return &OnChainResult{TxHash: resp.Txid}, nil
}

// NewAddress generates a new native SegWit (bech32) deposit address from
// LND's HD wallet. Each call derives a fresh address.
func (c *Client) NewAddress(ctx context.Context) (string, error) {
	req := &lnrpc.NewAddressRequest{
		Type: lnrpc.AddressType_WITNESS_PUBKEY_HASH, // bech32 bc1q... — lowest fees
	}

	resp, err := c.lnClient.NewAddress(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to generate new address: %w", err)
	}

	return resp.Address, nil
}

// GetWalletBalance returns LND's on-chain wallet balance split into confirmed
// and unconfirmed amounts. Used by the treasury service to assess spendable funds.
func (c *Client) GetWalletBalance(ctx context.Context) (*WalletBalance, error) {
	resp, err := c.lnClient.WalletBalance(ctx, &lnrpc.WalletBalanceRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get wallet balance: %w", err)
	}

	return &WalletBalance{
		ConfirmedSats:   resp.ConfirmedBalance,
		UnconfirmedSats: resp.UnconfirmedBalance,
		TotalSats:       resp.TotalBalance,
	}, nil
}

// GetTransaction queries LND for the confirmation status of a specific
// on-chain transaction by its hash. It fetches all wallet transactions and
// searches for a match.
//
// Returns Found=false when LND has no record of the tx — this happens when:
//   - The transaction was broadcast but hasn't propagated to LND's wallet view yet.
//   - The transaction was sent from an external wallet (not tracked by LND).
//
// The monitor_tx worker uses this to poll confirmation progress and decide when
// to mark a redemption as confirmed.
func (c *Client) GetTransaction(ctx context.Context, txHash string) (*OnChainTxStatus, error) {
	resp, err := c.lnClient.GetTransactions(ctx, &lnrpc.GetTransactionsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get transactions from LND: %w", err)
	}

	for _, tx := range resp.Transactions {
		if tx.TxHash == txHash {
			return &OnChainTxStatus{
				TxHash:           tx.TxHash,
				NumConfirmations: tx.NumConfirmations,
				BlockHeight:      tx.BlockHeight,
				BlockHash:        tx.BlockHash,
				Amount:           tx.Amount,
				Found:            true,
			}, nil
		}
	}

	return &OnChainTxStatus{Found: false}, nil
}
