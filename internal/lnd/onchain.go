package lnd

import (
	"context"
	"fmt"

	"github.com/lightningnetwork/lnd/lnrpc"
)

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
