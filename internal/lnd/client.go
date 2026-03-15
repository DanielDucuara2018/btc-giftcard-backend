// Package lnd provides a gRPC client wrapper for interacting with an LND node.
//
// This package abstracts the Lightning Network Daemon (LND) behind a clean
// interface so the rest of the codebase depends on LightningClient, not on
// LND internals. This makes testing and potential future migration (e.g., CLN)
// easier.
//
// ============================================================================
// DEPENDENCIES TO ADD (go get):
// ============================================================================
//
//	go get google.golang.org/grpc
//	go get github.com/lightningnetwork/lnd/lnrpc
//	go get gopkg.in/macaroon.v2
//
// ============================================================================
// ARCHITECTURE OVERVIEW
// ============================================================================
//
//	┌──────────┐     ┌──────────┐     ┌─────────────────┐
//	│  API     │────▶│ Service  │────▶│ LightningClient  │ (interface)
//	│(HTTP/gRPC)│     │(card pkg)│     │                  │
//	└──────────┘     └──────────┘     └────────┬─────────┘
//	                                            │
//	                                 ┌──────────▼─────────┐
//	                                 │   lnd.Client        │ (this package)
//	                                 │   (gRPC to LND)     │
//	                                 └──────────┬─────────┘
//	                                            │ gRPC + TLS + macaroon
//	                                 ┌──────────▼─────────┐
//	                                 │   LND daemon        │
//	                                 │   (docker container) │
//	                                 └────────────────────┘
//
// ============================================================================
// FILES TO CREATE IN THIS PACKAGE
// ============================================================================
//
//	internal/lnd/
//	├── client.go         ← THIS FILE: interface + Config + constructor
//	├── lightning.go       ← Lightning payment methods (SendPayment, DecodeInvoice)
//	├── onchain.go         ← On-chain methods (SendCoins, NewAddress, WalletBalance)
//	└── treasury.go        ← Treasury balance aggregation (channel + on-chain)
package lnd

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/lnrpc/routerrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// ============================================================================
// Config — LND connection settings (populated from config.toml [lnd] section)
// ============================================================================

// IMPLEMENT:
type Config struct {
	GRPCHost              string // "localhost" or "gift-card-backend.lnd"
	GRPCPort              string // 10009
	TLSCertPath           string // Path to LND's tls.cert
	MacaroonPath          string // Path to admin.macaroon (or custom-baked macaroon)
	Network               string // "mainnet", "testnet", "regtest"
	PaymentTimeoutSeconds int    // Max time for Lightning payment settlement (default: 30)
	MaxPaymentFeeSats     int64  // Max routing fee in sats (default: 100)
}

// ============================================================================
// LightningClient — interface for Lightning + on-chain operations
// ============================================================================
//
// IMPLEMENT: Define this interface so the card.Service and fund_card worker
// depend on it (not on the concrete Client struct). This enables:
//   - Unit testing with mock implementations
//   - Future migration to CLN or other Lightning implementations
type LightningClient interface {
	// ---- Lightning payments ----

	// PayInvoice pays a BOLT11 invoice and returns the payment result.
	// Used by card.Service.RedeemCard() when method == "lightning".
	//   - Decode the invoice to validate amount, expiry, and network
	//   - Call lnrpc.Lightning.SendPaymentSync() with fee limit
	//   - Return PaymentResult with payment_hash, payment_preimage, fee_sats
	//   - Handle errors: INSUFFICIENT_BALANCE, NO_ROUTE, INVOICE_EXPIRED
	PayInvoice(ctx context.Context, bolt11 string, maxFeeSats int64) (*PaymentResult, error)

	// DecodeInvoice decodes a BOLT11 invoice string without paying it.
	// Used to validate invoice amount matches requested spend amount.
	//   - Call lnrpc.Lightning.DecodePayReq()
	//   - Return decoded fields: destination, amount_sats, payment_hash, expiry, description
	//   - Validate: invoice not expired, amount > 0, correct network
	DecodeInvoice(ctx context.Context, bolt11 string) (*Invoice, error)

	// ---- On-chain transactions ----

	// SendOnChain sends BTC from the LND wallet to a destination address.
	// Used by card.Service.RedeemCard() when method == "onchain".
	//   - Call lnrpc.Lightning.SendCoins() with target address and amount
	//   - targetConf controls fee rate: 2=next block, 6=~1h, 144=~1day
	//   - Return the tx_hash from the response
	//   - Handle errors: INSUFFICIENT_FUNDS, INVALID_ADDRESS
	SendOnChain(ctx context.Context, address string, amountSats int64, targetConf int32) (*OnChainResult, error)

	// NewAddress generates a new on-chain Bitcoin address from LND's wallet.
	// Used for treasury deposit operations (receiving OTC-purchased BTC).
	//   - Call lnrpc.Lightning.NewAddress() with WITNESS_PUBKEY_HASH (bech32)
	//   - Return the generated address string
	NewAddress(ctx context.Context) (string, error)

	// ---- Balance & treasury ----

	// GetWalletBalance returns the on-chain wallet balance (confirmed + unconfirmed).
	// Used by the treasury service to calculate available balance.
	//   - Call lnrpc.Lightning.WalletBalance()
	//   - Return confirmed_balance and total_balance (both in sats)
	GetWalletBalance(ctx context.Context) (*WalletBalance, error)

	// GetChannelBalance returns the total balance across all Lightning channels.
	// Used by the treasury service to calculate available balance.
	//   - Call lnrpc.Lightning.ChannelBalance()
	//   - Return local_balance (spendable) and remote_balance (receivable)
	GetChannelBalance(ctx context.Context) (*ChannelBalance, error)

	// GetInfo returns basic LND node information (alias, pubkey, synced status).
	// Used for health checks and startup validation.
	//   - Call lnrpc.Lightning.GetInfo()
	//   - Return NodeInfo with synced_to_chain, synced_to_graph, block_height
	GetInfo(ctx context.Context) (*NodeInfo, error)

	// GetTransaction queries LND for the confirmation status of an on-chain
	// transaction. Used by the monitor_tx worker to track confirmation progress.
	//   - Call lnrpc.Lightning.GetTransactions() and search for txHash in results
	//   - Return Found=false when the tx is not in LND's wallet history
	GetTransaction(ctx context.Context, txHash string) (*OnChainTxStatus, error)

	// Close closes the underlying gRPC connection.
	Close() error
}

// ============================================================================
// Result types — returned by LightningClient methods
// ============================================================================

type PaymentResultStatus string

const (
	Succeeded PaymentResultStatus = "succeeded"
	Failed    PaymentResultStatus = "failed"
	InFlight  PaymentResultStatus = "in_flight"
)

type PaymentResult struct {
	PaymentHash     string              // hex-encoded payment hash (32 bytes)
	PaymentPreimage string              // hex-encoded preimage (proof of payment)
	FeeSats         int64               // Routing fee paid in satoshis
	Status          PaymentResultStatus // "succeeded", "failed", "in_flight"
}

type Invoice struct {
	Destination string // Recipient node public key
	AmountSats  int64  // Invoice amount in satoshis (0 = any amount)
	PaymentHash string // Hex-encoded payment hash
	Expiry      int64  // Seconds until invoice expires
	Description string // Invoice description/memo
	IsExpired   bool   // true if invoice has expired
}

type OnChainResult struct {
	TxHash string // Hex-encoded transaction hash (64 chars)
}

type WalletBalance struct {
	ConfirmedSats   int64 // On-chain confirmed balance
	UnconfirmedSats int64 // On-chain unconfirmed (pending) balance
	TotalSats       int64 // Confirmed + Unconfirmed
}

type ChannelBalance struct {
	LocalSats  int64 // Our side of channels (spendable via Lightning)
	RemoteSats int64 // Remote side of channels (receivable capacity)
}

type NodeInfo struct {
	Alias         string
	PubKey        string
	SyncedToChain bool
	SyncedToGraph bool
	BlockHeight   uint32
	NumChannels   uint32
}

// OnChainTxStatus holds the confirmation status of an on-chain transaction.
type OnChainTxStatus struct {
	TxHash           string
	NumConfirmations int32
	BlockHeight      int32
	BlockHash        string
	Amount           int64
	Found            bool // false when LND has no record of this tx in its wallet
}

// ============================================================================
// Client — concrete implementation of LightningClient using LND gRPC
// ============================================================================
//
// IMPLEMENT:
// macaroonCredential implements grpc.PerRPCCredentials.
// It attaches the hex-encoded macaroon as gRPC metadata on every RPC call,
// so LND can authenticate and authorize the request.
type macaroonCredential struct {
	macaroon string // hex-encoded serialized macaroon
}

// GetRequestMetadata is called by gRPC before each RPC. It returns the
// "macaroon" key with the hex-encoded value that LND expects.
func (m macaroonCredential) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{"macaroon": m.macaroon}, nil
}

// RequireTransportSecurity returns true because macaroons are sensitive
// credentials that must only be sent over TLS-encrypted connections.
func (m macaroonCredential) RequireTransportSecurity() bool {
	return true
}

type Client struct {
	conn         *grpc.ClientConn       // gRPC connection (reused for all calls)
	lnClient     lnrpc.LightningClient  // Auto-generated gRPC stub
	routerClient routerrpc.RouterClient // Router sub-server client (SendPaymentV2)
	Cfg          Config                 // Connection & behavior config (exported for service access)
}

func NewClient(cfg Config) (*Client, error) {
	// NewClientTLSFromFile reads the PEM cert file and builds TLS credentials.
	// First arg is the file path (not contents), second is the server name
	// override ("" = use the name from the cert).
	creds, err := credentials.NewClientTLSFromFile(cfg.TLSCertPath, "")
	if err != nil {
		return nil, fmt.Errorf("could not load tls cert from %s: %w", cfg.TLSCertPath, err)
	}

	fileMacaroonData, err := os.ReadFile(cfg.MacaroonPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read macaroon file %s: %w", cfg.MacaroonPath, err)
	}
	macaroonCreds := macaroonCredential{macaroon: hex.EncodeToString(fileMacaroonData)}

	url := cfg.GRPCHost + ":" + cfg.GRPCPort
	conn, err := grpc.NewClient(url, grpc.WithTransportCredentials(creds), grpc.WithPerRPCCredentials(macaroonCreds))
	if err != nil {
		return nil, fmt.Errorf("could not dial %s: %w", url, err)
	}

	lnClient := lnrpc.NewLightningClient(conn)

	// Validate connection by calling GetInfo — fails fast if LND is not
	// running, wallet is locked, or credentials are wrong.
	info, err := lnClient.GetInfo(context.Background(), &lnrpc.GetInfoRequest{})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to connect to LND (is it running? wallet unlocked?): %w", err)
	}

	fmt.Printf("LND connected — alias=%s pubkey=%s height=%d synced_chain=%t synced_graph=%t\n",
		info.Alias, info.IdentityPubkey, info.BlockHeight, info.SyncedToChain, info.SyncedToGraph)

	if !info.SyncedToChain {
		fmt.Println("WARNING: LND is not synced to chain — payments may fail until sync completes")
	}

	return &Client{
		conn:         conn,
		lnClient:     lnClient,
		routerClient: routerrpc.NewRouterClient(conn),
		Cfg:          cfg,
	}, nil
}

// Close closes the underlying gRPC connection to LND.
func (c *Client) Close() error {
	return c.conn.Close()
}
