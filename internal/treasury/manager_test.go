package treasury

import (
	"context"
	"errors"
	"testing"

	"btc-giftcard/internal/lnd"
	"btc-giftcard/internal/otc"
	"btc-giftcard/internal/qonto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Mocks
// ============================================================================

// mockQonto implements qonto.QontoService.
type mockQonto struct {
	getAccount       func(ctx context.Context) (*qonto.Account, error)
	listTransactions func(ctx context.Context, side, status string, page int) (*qonto.TransactionListResponse, error)
	sendTransfer     func(ctx context.Context, req qonto.TransferRequest) (*qonto.TransferResponse, error)
}

func (m *mockQonto) GetAccount(ctx context.Context) (*qonto.Account, error) {
	return m.getAccount(ctx)
}
func (m *mockQonto) ListTransactions(ctx context.Context, side, status string, page int) (*qonto.TransactionListResponse, error) {
	return m.listTransactions(ctx, side, status, page)
}
func (m *mockQonto) SendTransfer(ctx context.Context, req qonto.TransferRequest) (*qonto.TransferResponse, error) {
	return m.sendTransfer(ctx, req)
}

// mockCryptoCom implements otc.CryptocomService.
type mockCryptoCom struct {
	getOTCInstruments func(ctx context.Context) ([]otc.OTCInstrument, error)
	requestQuote      func(ctx context.Context, params otc.RequestQuoteParams) (*otc.Quote, error)
	requestDeal       func(ctx context.Context, params otc.RequestDealParams) (*otc.Deal, error)
	withdraw          func(ctx context.Context, req otc.WithdrawalRequest) (*otc.Withdrawal, error)
	getWithdrawal     func(ctx context.Context, withdrawalID string) (*otc.Withdrawal, error)
}

func (m *mockCryptoCom) GetOTCInstruments(ctx context.Context) ([]otc.OTCInstrument, error) {
	return m.getOTCInstruments(ctx)
}
func (m *mockCryptoCom) RequestQuote(ctx context.Context, params otc.RequestQuoteParams) (*otc.Quote, error) {
	return m.requestQuote(ctx, params)
}
func (m *mockCryptoCom) RequestDeal(ctx context.Context, params otc.RequestDealParams) (*otc.Deal, error) {
	return m.requestDeal(ctx, params)
}
func (m *mockCryptoCom) Withdraw(ctx context.Context, req otc.WithdrawalRequest) (*otc.Withdrawal, error) {
	return m.withdraw(ctx, req)
}
func (m *mockCryptoCom) GetWithdrawal(ctx context.Context, withdrawalID string) (*otc.Withdrawal, error) {
	return m.getWithdrawal(ctx, withdrawalID)
}

// mockLND implements lnd.LightningClient.
type mockLND struct {
	payInvoice     func(ctx context.Context, bolt11 string, maxFeeSats int64) (*lnd.PaymentResult, error)
	decodeInvoice  func(ctx context.Context, bolt11 string) (*lnd.Invoice, error)
	sendOnChain    func(ctx context.Context, address string, amountSats int64, targetConf int32) (*lnd.OnChainResult, error)
	newAddress     func(ctx context.Context) (string, error)
	getWalletBal   func(ctx context.Context) (*lnd.WalletBalance, error)
	getChannelBal  func(ctx context.Context) (*lnd.ChannelBalance, error)
	getInfo        func(ctx context.Context) (*lnd.NodeInfo, error)
	getTransaction func(ctx context.Context, txHash string) (*lnd.OnChainTxStatus, error)
	connectPeer    func(ctx context.Context, pubKey, host string) (*lnd.ConnectPeerResult, error)
	listPeers      func(ctx context.Context) ([]lnd.Peer, error)
	listChannels   func(ctx context.Context) ([]lnd.Channel, error)
	openChannel    func(ctx context.Context, peerPubKey string, localAmtSats, pushAmtSats int64, targetConf int32) (*lnd.OpenChannelResult, error)
}

func (m *mockLND) Close() error { return nil }

func (m *mockLND) PayInvoice(ctx context.Context, bolt11 string, maxFeeSats int64) (*lnd.PaymentResult, error) {
	return m.payInvoice(ctx, bolt11, maxFeeSats)
}
func (m *mockLND) DecodeInvoice(ctx context.Context, bolt11 string) (*lnd.Invoice, error) {
	return m.decodeInvoice(ctx, bolt11)
}
func (m *mockLND) SendOnChain(ctx context.Context, address string, amountSats int64, targetConf int32) (*lnd.OnChainResult, error) {
	return m.sendOnChain(ctx, address, amountSats, targetConf)
}
func (m *mockLND) NewAddress(ctx context.Context) (string, error) { return m.newAddress(ctx) }
func (m *mockLND) GetWalletBalance(ctx context.Context) (*lnd.WalletBalance, error) {
	return m.getWalletBal(ctx)
}
func (m *mockLND) GetChannelBalance(ctx context.Context) (*lnd.ChannelBalance, error) {
	return m.getChannelBal(ctx)
}
func (m *mockLND) GetInfo(ctx context.Context) (*lnd.NodeInfo, error) { return m.getInfo(ctx) }
func (m *mockLND) GetTransaction(ctx context.Context, txHash string) (*lnd.OnChainTxStatus, error) {
	return m.getTransaction(ctx, txHash)
}
func (m *mockLND) ConnectPeer(ctx context.Context, pubKey, host string) (*lnd.ConnectPeerResult, error) {
	return m.connectPeer(ctx, pubKey, host)
}
func (m *mockLND) ListPeers(ctx context.Context) ([]lnd.Peer, error) { return m.listPeers(ctx) }
func (m *mockLND) ListChannels(ctx context.Context) ([]lnd.Channel, error) {
	return m.listChannels(ctx)
}
func (m *mockLND) OpenChannel(ctx context.Context, peerPubKey string, localAmtSats, pushAmtSats int64, targetConf int32) (*lnd.OpenChannelResult, error) {
	return m.openChannel(ctx, peerPubKey, localAmtSats, pushAmtSats, targetConf)
}

// newManager is a test helper that wires up a TreasuryManager with mock deps.
func newManager(q qonto.QontoService, c otc.CryptocomService, l lnd.LightningClient, cfg Config) *TreasuryManager {
	return NewTreasuryManager(q, c, l, cfg)
}

// ============================================================================
// GetFiatBalance
// ============================================================================

func TestGetFiatBalance_Success(t *testing.T) {
	q := &mockQonto{
		getAccount: func(_ context.Context) (*qonto.Account, error) {
			return &qonto.Account{AuthorizedBalanceCents: 1_200_000}, nil
		},
	}
	mgr := newManager(q, nil, nil, Config{})
	bal, err := mgr.GetFiatBalance(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1_200_000), bal)
}

func TestGetFiatBalance_Error(t *testing.T) {
	q := &mockQonto{
		getAccount: func(_ context.Context) (*qonto.Account, error) {
			return nil, errors.New("qonto unavailable")
		},
	}
	mgr := newManager(q, nil, nil, Config{})
	_, err := mgr.GetFiatBalance(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fiat balance")
}

// ============================================================================
// TransferFiatToCryptoCom
// ============================================================================

func TestTransferFiatToCryptoCom_Success(t *testing.T) {
	const (
		cryptoComIBAN = "DE89370400440532013000"
		cryptoComName = "Crypto.com OTC"
		cryptoComBID  = "bene-uuid-001"
		acctID        = "qonto-acct-uuid"
		idempKey      = "uuid-idempotency-001"
	)

	var capturedReq qonto.TransferRequest
	q := &mockQonto{
		getAccount: func(_ context.Context) (*qonto.Account, error) {
			return &qonto.Account{ID: acctID, IBAN: "FR761..."}, nil
		},
		sendTransfer: func(_ context.Context, req qonto.TransferRequest) (*qonto.TransferResponse, error) {
			capturedReq = req
			return &qonto.TransferResponse{}, nil
		},
	}

	cfg := Config{
		CryptoComIBAN:            cryptoComIBAN,
		CryptoComBeneficiaryName: cryptoComName,
		CryptoComBeneficiaryID:   cryptoComBID,
	}
	mgr := newManager(q, nil, nil, cfg)
	_, err := mgr.TransferFiatToCryptoCom(context.Background(), 150_000, idempKey)
	require.NoError(t, err)

	// Verify the IBAN and Name are wired from config (required for VOP inside client).
	assert.Equal(t, cryptoComIBAN, capturedReq.BeneficiaryIBAN)
	assert.Equal(t, cryptoComName, capturedReq.BeneficiaryName)
	assert.Equal(t, acctID, capturedReq.Transfer.BankAccountID)
	assert.Equal(t, idempKey, capturedReq.Transfer.Reference)
	assert.Equal(t, "EUR", capturedReq.Transfer.Currency)
	// 150_000 cents = 1500.00 EUR
	assert.Equal(t, "1500.00", capturedReq.Transfer.Amount)
}

func TestTransferFiatToCryptoCom_GetAccountError(t *testing.T) {
	q := &mockQonto{
		getAccount: func(_ context.Context) (*qonto.Account, error) {
			return nil, errors.New("network timeout")
		},
	}
	mgr := newManager(q, nil, nil, Config{})
	_, err := mgr.TransferFiatToCryptoCom(context.Background(), 100_000, "ref-001")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get account")
}

func TestTransferFiatToCryptoCom_SendTransferError(t *testing.T) {
	q := &mockQonto{
		getAccount: func(_ context.Context) (*qonto.Account, error) {
			return &qonto.Account{ID: "acct-1"}, nil
		},
		sendTransfer: func(_ context.Context, _ qonto.TransferRequest) (*qonto.TransferResponse, error) {
			return nil, errors.New("insufficient balance")
		},
	}
	mgr := newManager(q, nil, nil, Config{})
	_, err := mgr.TransferFiatToCryptoCom(context.Background(), 100_000, "ref-002")
	assert.Error(t, err)
}

func TestTransferFiatToCryptoCom_AmountFormatting(t *testing.T) {
	cases := []struct {
		cents    int64
		expected string
	}{
		{100_000, "1000.00"},
		{150_050, "1500.50"},
		{1, "0.01"},
		{99, "0.99"},
		{100, "1.00"},
	}
	for _, tc := range cases {
		t.Run(tc.expected, func(t *testing.T) {
			var capturedAmount string
			q := &mockQonto{
				getAccount: func(_ context.Context) (*qonto.Account, error) {
					return &qonto.Account{ID: "acct"}, nil
				},
				sendTransfer: func(_ context.Context, req qonto.TransferRequest) (*qonto.TransferResponse, error) {
					capturedAmount = req.Transfer.Amount
					return &qonto.TransferResponse{}, nil
				},
			}
			mgr := newManager(q, nil, nil, Config{})
			_, err := mgr.TransferFiatToCryptoCom(context.Background(), tc.cents, "ref")
			require.NoError(t, err)
			assert.Equal(t, tc.expected, capturedAmount)
		})
	}
}

// ============================================================================
// FundLNDChannel
// ============================================================================

func TestFundLNDChannel_NilWhenNoChannelsAndNoConfig(t *testing.T) {
	l := &mockLND{
		listChannels: func(_ context.Context) ([]lnd.Channel, error) {
			return []lnd.Channel{}, nil // no existing channels
		},
	}
	mgr := newManager(nil, nil, l, Config{}) // no LNDChannelPeerPubKey
	result, err := mgr.FundLNDChannel(context.Background(), 1_000_000)
	require.NoError(t, err)
	assert.Nil(t, result, "should return nil when no channels exist and no pubkey is configured")
}

func TestFundLNDChannel_ListChannelsError(t *testing.T) {
	l := &mockLND{
		listChannels: func(_ context.Context) ([]lnd.Channel, error) {
			return nil, errors.New("lnd unreachable")
		},
	}
	mgr := newManager(nil, nil, l, Config{})
	_, err := mgr.FundLNDChannel(context.Background(), 1_000_000)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "list channels")
}

func TestFundLNDChannel_AutoDiscoversMostActivePeer(t *testing.T) {
	const bestPubKey = "aabbcc112233"
	var openedWith string

	l := &mockLND{
		listChannels: func(_ context.Context) ([]lnd.Channel, error) {
			return []lnd.Channel{
				{RemotePubKey: "lowactivity000", TotalSatsSent: 100, TotalSatsRecv: 200},
				{RemotePubKey: bestPubKey, TotalSatsSent: 5000, TotalSatsRecv: 8000}, // highest
				{RemotePubKey: "midactivity111", TotalSatsSent: 1000, TotalSatsRecv: 500},
			}, nil
		},
		listPeers: func(_ context.Context) ([]lnd.Peer, error) {
			return []lnd.Peer{
				{PubKey: bestPubKey, Address: "10.0.0.1:9735"},
			}, nil
		},
		connectPeer: func(_ context.Context, pubKey, host string) (*lnd.ConnectPeerResult, error) {
			return &lnd.ConnectPeerResult{PubKey: pubKey, Address: host}, nil
		},
		openChannel: func(_ context.Context, peerPubKey string, _, _ int64, _ int32) (*lnd.OpenChannelResult, error) {
			openedWith = peerPubKey
			return &lnd.OpenChannelResult{FundingTxID: "txid-abc", ChannelPoint: "txid-abc:0"}, nil
		},
	}

	mgr := newManager(nil, nil, l, Config{}) // no explicit pubkey
	result, err := mgr.FundLNDChannel(context.Background(), 1_000_000)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, bestPubKey, openedWith, "should open channel with the most active peer")
	assert.Equal(t, "txid-abc", result.FundingTxID)
}

func TestFundLNDChannel_ExplicitPubKeyInConfig(t *testing.T) {
	const configuredPubKey = "configured-pubkey-xyz"
	var openedWith string

	l := &mockLND{
		// listChannels should not be called when pubkey is configured.
		listPeers: func(_ context.Context) ([]lnd.Peer, error) {
			return []lnd.Peer{
				{PubKey: configuredPubKey, Address: "192.168.1.1:9735"},
			}, nil
		},
		connectPeer: func(_ context.Context, pubKey, _ string) (*lnd.ConnectPeerResult, error) {
			return &lnd.ConnectPeerResult{PubKey: pubKey}, nil
		},
		openChannel: func(_ context.Context, peerPubKey string, _, _ int64, _ int32) (*lnd.OpenChannelResult, error) {
			openedWith = peerPubKey
			return &lnd.OpenChannelResult{FundingTxID: "txid-config"}, nil
		},
	}

	cfg := Config{LNDChannelPeerPubKey: configuredPubKey}
	mgr := newManager(nil, nil, l, cfg)
	result, err := mgr.FundLNDChannel(context.Background(), 500_000)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, configuredPubKey, openedWith)
}

func TestFundLNDChannel_OpenChannelError(t *testing.T) {
	l := &mockLND{
		listChannels: func(_ context.Context) ([]lnd.Channel, error) {
			return []lnd.Channel{
				{RemotePubKey: "aabbcc112233ddeeff0011223344556677", TotalSatsSent: 1000},
			}, nil
		},
		listPeers: func(_ context.Context) ([]lnd.Peer, error) {
			return []lnd.Peer{{PubKey: "aabbcc112233ddeeff0011223344556677", Address: "1.2.3.4:9735"}}, nil
		},
		connectPeer: func(_ context.Context, _, _ string) (*lnd.ConnectPeerResult, error) {
			return &lnd.ConnectPeerResult{}, nil
		},
		openChannel: func(_ context.Context, _ string, _, _ int64, _ int32) (*lnd.OpenChannelResult, error) {
			return nil, errors.New("channel open failed: peer disconnected")
		},
	}

	mgr := newManager(nil, nil, l, Config{})
	_, err := mgr.FundLNDChannel(context.Background(), 1_000_000)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "open channel")
}

// ============================================================================
// GetLNDBalance
// ============================================================================

func TestGetLNDBalance_Success(t *testing.T) {
	l := &mockLND{
		getWalletBal: func(_ context.Context) (*lnd.WalletBalance, error) {
			return &lnd.WalletBalance{ConfirmedSats: 2_000_000, UnconfirmedSats: 100_000}, nil
		},
		getChannelBal: func(_ context.Context) (*lnd.ChannelBalance, error) {
			return &lnd.ChannelBalance{LocalSats: 3_000_000, RemoteSats: 1_500_000}, nil
		},
	}
	mgr := newManager(nil, nil, l, Config{})
	bal, err := mgr.GetLNDBalance(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(2_000_000), bal.OnChainConfirmedSats)
	assert.Equal(t, int64(100_000), bal.OnChainUnconfirmedSats)
	assert.Equal(t, int64(3_000_000), bal.LightningLocalSats)
	assert.Equal(t, int64(1_500_000), bal.LightningRemoteSats)
	// Total = confirmed on-chain + local lightning
	assert.Equal(t, int64(5_000_000), bal.TotalSats)
}

func TestGetLNDBalance_WalletError(t *testing.T) {
	l := &mockLND{
		getWalletBal: func(_ context.Context) (*lnd.WalletBalance, error) {
			return nil, errors.New("lnd wallet unreachable")
		},
	}
	mgr := newManager(nil, nil, l, Config{})
	_, err := mgr.GetLNDBalance(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "wallet balance")
}

func TestGetLNDBalance_ChannelError(t *testing.T) {
	l := &mockLND{
		getWalletBal: func(_ context.Context) (*lnd.WalletBalance, error) {
			return &lnd.WalletBalance{ConfirmedSats: 1_000_000}, nil
		},
		getChannelBal: func(_ context.Context) (*lnd.ChannelBalance, error) {
			return nil, errors.New("channel balance rpc failed")
		},
	}
	mgr := newManager(nil, nil, l, Config{})
	_, err := mgr.GetLNDBalance(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "channel balance")
}
