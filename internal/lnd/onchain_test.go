package lnd

import (
	"context"
	"errors"
	"testing"

	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// ============================================================================
// Mock — stubs the lnrpc.LightningClient methods used by onchain.go
// ============================================================================

type mockOnchainLNClient struct {
	lnrpc.LightningClient // embed for interface compliance

	sendCoinsFn       func(ctx context.Context, in *lnrpc.SendCoinsRequest, opts ...grpc.CallOption) (*lnrpc.SendCoinsResponse, error)
	newAddressFn      func(ctx context.Context, in *lnrpc.NewAddressRequest, opts ...grpc.CallOption) (*lnrpc.NewAddressResponse, error)
	walletBalanceFn   func(ctx context.Context, in *lnrpc.WalletBalanceRequest, opts ...grpc.CallOption) (*lnrpc.WalletBalanceResponse, error)
	getTransactionsFn func(ctx context.Context, in *lnrpc.GetTransactionsRequest, opts ...grpc.CallOption) (*lnrpc.TransactionDetails, error)
}

func (m *mockOnchainLNClient) SendCoins(ctx context.Context, in *lnrpc.SendCoinsRequest, opts ...grpc.CallOption) (*lnrpc.SendCoinsResponse, error) {
	return m.sendCoinsFn(ctx, in, opts...)
}

func (m *mockOnchainLNClient) NewAddress(ctx context.Context, in *lnrpc.NewAddressRequest, opts ...grpc.CallOption) (*lnrpc.NewAddressResponse, error) {
	return m.newAddressFn(ctx, in, opts...)
}

func (m *mockOnchainLNClient) WalletBalance(ctx context.Context, in *lnrpc.WalletBalanceRequest, opts ...grpc.CallOption) (*lnrpc.WalletBalanceResponse, error) {
	return m.walletBalanceFn(ctx, in, opts...)
}

func (m *mockOnchainLNClient) GetTransactions(ctx context.Context, in *lnrpc.GetTransactionsRequest, opts ...grpc.CallOption) (*lnrpc.TransactionDetails, error) {
	if m.getTransactionsFn != nil {
		return m.getTransactionsFn(ctx, in, opts...)
	}
	return &lnrpc.TransactionDetails{}, nil
}

func newOnchainTestClient(mock *mockOnchainLNClient) *Client {
	return &Client{
		lnClient: mock,
		Cfg:      Config{},
	}
}

// ============================================================================
// SendOnChain tests
// ============================================================================

func TestSendOnChain_Success(t *testing.T) {
	var captured *lnrpc.SendCoinsRequest

	mock := &mockOnchainLNClient{
		sendCoinsFn: func(_ context.Context, in *lnrpc.SendCoinsRequest, _ ...grpc.CallOption) (*lnrpc.SendCoinsResponse, error) {
			captured = in
			return &lnrpc.SendCoinsResponse{
				Txid: "abc123def456abc123def456abc123def456abc123def456abc123def456abc1",
			}, nil
		},
	}

	client := newOnchainTestClient(mock)
	result, err := client.SendOnChain(context.Background(), "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx", 50000, 6)

	require.NoError(t, err)
	assert.Equal(t, "abc123def456abc123def456abc123def456abc123def456abc123def456abc1", result.TxHash)

	// Verify request fields passed correctly
	require.NotNil(t, captured)
	assert.Equal(t, "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx", captured.Addr)
	assert.Equal(t, int64(50000), captured.Amount)
	assert.Equal(t, int32(6), captured.TargetConf)
}

func TestSendOnChain_EmptyAddress(t *testing.T) {
	client := newOnchainTestClient(&mockOnchainLNClient{})

	result, err := client.SendOnChain(context.Background(), "", 50000, 6)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "address must not be empty")
}

func TestSendOnChain_BelowDustLimit(t *testing.T) {
	client := newOnchainTestClient(&mockOnchainLNClient{})

	tests := []struct {
		name   string
		amount int64
	}{
		{"zero", 0},
		{"negative", -100},
		{"one sat", 1},
		{"545 sats", 545},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := client.SendOnChain(context.Background(), "tb1qtest", tt.amount, 6)
			assert.Nil(t, result)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "dust limit")
		})
	}
}

func TestSendOnChain_ExactDustLimit(t *testing.T) {
	mock := &mockOnchainLNClient{
		sendCoinsFn: func(_ context.Context, _ *lnrpc.SendCoinsRequest, _ ...grpc.CallOption) (*lnrpc.SendCoinsResponse, error) {
			return &lnrpc.SendCoinsResponse{Txid: "txhash546"}, nil
		},
	}

	client := newOnchainTestClient(mock)
	result, err := client.SendOnChain(context.Background(), "tb1qtest", 546, 6)

	require.NoError(t, err)
	assert.Equal(t, "txhash546", result.TxHash)
}

func TestSendOnChain_LNDError(t *testing.T) {
	mock := &mockOnchainLNClient{
		sendCoinsFn: func(_ context.Context, _ *lnrpc.SendCoinsRequest, _ ...grpc.CallOption) (*lnrpc.SendCoinsResponse, error) {
			return nil, errors.New("insufficient funds for send")
		},
	}

	client := newOnchainTestClient(mock)
	result, err := client.SendOnChain(context.Background(), "tb1qtest", 100000, 6)

	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to send on-chain coins")
	assert.Contains(t, err.Error(), "insufficient funds")
}

func TestSendOnChain_DifferentTargetConf(t *testing.T) {
	var capturedConf int32

	mock := &mockOnchainLNClient{
		sendCoinsFn: func(_ context.Context, in *lnrpc.SendCoinsRequest, _ ...grpc.CallOption) (*lnrpc.SendCoinsResponse, error) {
			capturedConf = in.TargetConf
			return &lnrpc.SendCoinsResponse{Txid: "tx1"}, nil
		},
	}

	client := newOnchainTestClient(mock)

	_, err := client.SendOnChain(context.Background(), "tb1qtest", 10000, 2)
	require.NoError(t, err)
	assert.Equal(t, int32(2), capturedConf)

	_, err = client.SendOnChain(context.Background(), "tb1qtest", 10000, 144)
	require.NoError(t, err)
	assert.Equal(t, int32(144), capturedConf)
}

// ============================================================================
// NewAddress tests
// ============================================================================

func TestNewAddress_Success(t *testing.T) {
	var capturedType lnrpc.AddressType

	mock := &mockOnchainLNClient{
		newAddressFn: func(_ context.Context, in *lnrpc.NewAddressRequest, _ ...grpc.CallOption) (*lnrpc.NewAddressResponse, error) {
			capturedType = in.Type
			return &lnrpc.NewAddressResponse{
				Address: "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx",
			}, nil
		},
	}

	client := newOnchainTestClient(mock)
	addr, err := client.NewAddress(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx", addr)
	assert.Equal(t, lnrpc.AddressType_WITNESS_PUBKEY_HASH, capturedType, "should request bech32 address")
}

func TestNewAddress_LNDError(t *testing.T) {
	mock := &mockOnchainLNClient{
		newAddressFn: func(_ context.Context, _ *lnrpc.NewAddressRequest, _ ...grpc.CallOption) (*lnrpc.NewAddressResponse, error) {
			return nil, errors.New("wallet locked")
		},
	}

	client := newOnchainTestClient(mock)
	addr, err := client.NewAddress(context.Background())

	assert.Empty(t, addr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to generate new address")
	assert.Contains(t, err.Error(), "wallet locked")
}

// ============================================================================
// GetWalletBalance tests
// ============================================================================

func TestGetWalletBalance_Success(t *testing.T) {
	mock := &mockOnchainLNClient{
		walletBalanceFn: func(_ context.Context, _ *lnrpc.WalletBalanceRequest, _ ...grpc.CallOption) (*lnrpc.WalletBalanceResponse, error) {
			return &lnrpc.WalletBalanceResponse{
				ConfirmedBalance:   500000,
				UnconfirmedBalance: 10000,
				TotalBalance:       510000,
			}, nil
		},
	}

	client := newOnchainTestClient(mock)
	bal, err := client.GetWalletBalance(context.Background())

	require.NoError(t, err)
	assert.Equal(t, int64(500000), bal.ConfirmedSats)
	assert.Equal(t, int64(10000), bal.UnconfirmedSats)
	assert.Equal(t, int64(510000), bal.TotalSats)
}

func TestGetWalletBalance_ZeroBalance(t *testing.T) {
	mock := &mockOnchainLNClient{
		walletBalanceFn: func(_ context.Context, _ *lnrpc.WalletBalanceRequest, _ ...grpc.CallOption) (*lnrpc.WalletBalanceResponse, error) {
			return &lnrpc.WalletBalanceResponse{
				ConfirmedBalance:   0,
				UnconfirmedBalance: 0,
				TotalBalance:       0,
			}, nil
		},
	}

	client := newOnchainTestClient(mock)
	bal, err := client.GetWalletBalance(context.Background())

	require.NoError(t, err)
	assert.Equal(t, int64(0), bal.ConfirmedSats)
	assert.Equal(t, int64(0), bal.UnconfirmedSats)
	assert.Equal(t, int64(0), bal.TotalSats)
}

func TestGetWalletBalance_LNDError(t *testing.T) {
	mock := &mockOnchainLNClient{
		walletBalanceFn: func(_ context.Context, _ *lnrpc.WalletBalanceRequest, _ ...grpc.CallOption) (*lnrpc.WalletBalanceResponse, error) {
			return nil, errors.New("connection refused")
		},
	}

	client := newOnchainTestClient(mock)
	bal, err := client.GetWalletBalance(context.Background())

	assert.Nil(t, bal)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get wallet balance")
	assert.Contains(t, err.Error(), "connection refused")
}

// ============================================================================
// GetTransaction tests
// ============================================================================

func TestGetTransaction_Found(t *testing.T) {
	txHash := "abc123def456abc123def456abc123def456abc123def456abc123def456abc1"

	mock := &mockOnchainLNClient{
		getTransactionsFn: func(_ context.Context, _ *lnrpc.GetTransactionsRequest, _ ...grpc.CallOption) (*lnrpc.TransactionDetails, error) {
			return &lnrpc.TransactionDetails{
				Transactions: []*lnrpc.Transaction{
					{TxHash: "other_tx", NumConfirmations: 0},
					{TxHash: txHash, NumConfirmations: 6, BlockHeight: 800001, BlockHash: "blockhash", Amount: 50000},
				},
			}, nil
		},
	}

	client := newOnchainTestClient(mock)
	status, err := client.GetTransaction(context.Background(), txHash)

	require.NoError(t, err)
	require.NotNil(t, status)
	assert.True(t, status.Found)
	assert.Equal(t, txHash, status.TxHash)
	assert.Equal(t, int32(6), status.NumConfirmations)
	assert.Equal(t, int32(800001), status.BlockHeight)
	assert.Equal(t, "blockhash", status.BlockHash)
	assert.Equal(t, int64(50000), status.Amount)
}

func TestGetTransaction_NotFound(t *testing.T) {
	mock := &mockOnchainLNClient{
		getTransactionsFn: func(_ context.Context, _ *lnrpc.GetTransactionsRequest, _ ...grpc.CallOption) (*lnrpc.TransactionDetails, error) {
			return &lnrpc.TransactionDetails{
				Transactions: []*lnrpc.Transaction{
					{TxHash: "other_tx", NumConfirmations: 3},
				},
			}, nil
		},
	}

	client := newOnchainTestClient(mock)
	status, err := client.GetTransaction(context.Background(), "missing_hash")

	require.NoError(t, err)
	require.NotNil(t, status)
	assert.False(t, status.Found)
}

func TestGetTransaction_EmptyWallet(t *testing.T) {
	// LND returns no transactions at all (fresh node)
	provided := &mockOnchainLNClient{} // getTransactionsFn nil → default empty list

	client := newOnchainTestClient(provided)
	status, err := client.GetTransaction(context.Background(), "any_hash")

	require.NoError(t, err)
	assert.False(t, status.Found)
}

func TestGetTransaction_LNDError(t *testing.T) {
	mock := &mockOnchainLNClient{
		getTransactionsFn: func(_ context.Context, _ *lnrpc.GetTransactionsRequest, _ ...grpc.CallOption) (*lnrpc.TransactionDetails, error) {
			return nil, errors.New("connection refused")
		},
	}

	client := newOnchainTestClient(mock)
	status, err := client.GetTransaction(context.Background(), "any_hash")

	assert.Nil(t, status)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get transactions from LND")
	assert.Contains(t, err.Error(), "connection refused")
}

func TestGetTransaction_ZeroConfirmations(t *testing.T) {
	txHash := "abc123def456abc123def456abc123def456abc123def456abc123def456abc1"

	mock := &mockOnchainLNClient{
		getTransactionsFn: func(_ context.Context, _ *lnrpc.GetTransactionsRequest, _ ...grpc.CallOption) (*lnrpc.TransactionDetails, error) {
			return &lnrpc.TransactionDetails{
				Transactions: []*lnrpc.Transaction{
					{TxHash: txHash, NumConfirmations: 0, BlockHeight: 0},
				},
			}, nil
		},
	}

	client := newOnchainTestClient(mock)
	status, err := client.GetTransaction(context.Background(), txHash)

	require.NoError(t, err)
	assert.True(t, status.Found)
	assert.Equal(t, int32(0), status.NumConfirmations, "unconfirmed tx should have 0 confirmations")
}
