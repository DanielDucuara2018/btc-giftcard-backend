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

	newAddressFn    func(ctx context.Context, in *lnrpc.NewAddressRequest, opts ...grpc.CallOption) (*lnrpc.NewAddressResponse, error)
	walletBalanceFn func(ctx context.Context, in *lnrpc.WalletBalanceRequest, opts ...grpc.CallOption) (*lnrpc.WalletBalanceResponse, error)
}

func (m *mockOnchainLNClient) NewAddress(ctx context.Context, in *lnrpc.NewAddressRequest, opts ...grpc.CallOption) (*lnrpc.NewAddressResponse, error) {
	return m.newAddressFn(ctx, in, opts...)
}

func (m *mockOnchainLNClient) WalletBalance(ctx context.Context, in *lnrpc.WalletBalanceRequest, opts ...grpc.CallOption) (*lnrpc.WalletBalanceResponse, error) {
	return m.walletBalanceFn(ctx, in, opts...)
}

func newOnchainTestClient(mock *mockOnchainLNClient) *Client {
	return &Client{
		lnClient: mock,
		Cfg:      Config{},
	}
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
