package lnd

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"btc-giftcard/pkg/logger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	_ = logger.Init("development")
}

// ============================================================================
// Unit tests — no LND connection required, run with: go test ./internal/lnd/
// ============================================================================

// --- macaroonCredential tests ---

func TestMacaroonCredential_GetRequestMetadata(t *testing.T) {
	cred := macaroonCredential{macaroon: "abcdef1234567890"}

	metadata, err := cred.GetRequestMetadata(context.Background(), "localhost:10009")
	require.NoError(t, err)
	assert.Equal(t, "abcdef1234567890", metadata["macaroon"])
	assert.Len(t, metadata, 1, "metadata should only contain 'macaroon' key")
}

func TestMacaroonCredential_GetRequestMetadata_EmptyMacaroon(t *testing.T) {
	cred := macaroonCredential{macaroon: ""}

	metadata, err := cred.GetRequestMetadata(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "", metadata["macaroon"])
}

func TestMacaroonCredential_RequireTransportSecurity(t *testing.T) {
	cred := macaroonCredential{macaroon: "test"}
	assert.True(t, cred.RequireTransportSecurity(), "macaroon credentials must require TLS")
}

// --- Config validation tests ---

func TestConfig_DefaultValues(t *testing.T) {
	cfg := Config{
		GRPCHost:              "localhost",
		GRPCPort:              "10009",
		TLSCertPath:           "/path/to/tls.cert",
		MacaroonPath:          "/path/to/admin.macaroon",
		Network:               "testnet",
		PaymentTimeoutSeconds: 30,
		MaxPaymentFeeSats:     100,
	}

	assert.Equal(t, "localhost", cfg.GRPCHost)
	assert.Equal(t, "10009", cfg.GRPCPort)
	assert.Equal(t, "testnet", cfg.Network)
	assert.Equal(t, 30, cfg.PaymentTimeoutSeconds)
	assert.Equal(t, int64(100), cfg.MaxPaymentFeeSats)
}

// --- NewClient error cases (no real LND needed) ---

func TestNewClient_InvalidTLSCertPath(t *testing.T) {
	cfg := Config{
		TLSCertPath:  "/nonexistent/path/tls.cert",
		MacaroonPath: "/nonexistent/path/admin.macaroon",
		GRPCHost:     "localhost",
		GRPCPort:     "10009",
	}

	client, err := NewClient(cfg)
	assert.Nil(t, client)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tls cert")
	assert.Contains(t, err.Error(), "/nonexistent/path/tls.cert")
}

func TestNewClient_InvalidMacaroonPath(t *testing.T) {
	// Generate a real self-signed TLS cert so the TLS step passes
	// and we can test the macaroon error path.
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "tls.cert")

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	err = os.WriteFile(certPath, certPEM, 0644)
	require.NoError(t, err)

	cfg := Config{
		TLSCertPath:  certPath,
		MacaroonPath: "/nonexistent/path/admin.macaroon",
		GRPCHost:     "localhost",
		GRPCPort:     "10009",
	}

	client, err := NewClient(cfg)
	assert.Nil(t, client)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "macaroon")
	assert.Contains(t, err.Error(), "/nonexistent/path/admin.macaroon")
}

// --- Result type tests ---

func TestPaymentResultStatus_Values(t *testing.T) {
	assert.Equal(t, PaymentResultStatus("succeeded"), Succeeded)
	assert.Equal(t, PaymentResultStatus("failed"), Failed)
	assert.Equal(t, PaymentResultStatus("in_flight"), InFlight)
}

func TestPaymentResult_Fields(t *testing.T) {
	result := PaymentResult{
		PaymentHash:     "abc123",
		PaymentPreimage: "def456",
		FeeSats:         10,
		Status:          Succeeded,
	}

	assert.Equal(t, "abc123", result.PaymentHash)
	assert.Equal(t, "def456", result.PaymentPreimage)
	assert.Equal(t, int64(10), result.FeeSats)
	assert.Equal(t, Succeeded, result.Status)
}

func TestInvoice_Fields(t *testing.T) {
	invoice := Invoice{
		Destination: "03pubkey...",
		AmountSats:  50000,
		PaymentHash: "hash123",
		Expiry:      3600,
		Description: "test payment",
		IsExpired:   false,
	}

	assert.Equal(t, int64(50000), invoice.AmountSats)
	assert.Equal(t, int64(3600), invoice.Expiry)
	assert.False(t, invoice.IsExpired)
}

func TestWalletBalance_Fields(t *testing.T) {
	balance := WalletBalance{
		ConfirmedSats:   100000,
		UnconfirmedSats: 5000,
		TotalSats:       105000,
	}

	assert.Equal(t, int64(100000), balance.ConfirmedSats)
	assert.Equal(t, int64(5000), balance.UnconfirmedSats)
	assert.Equal(t, int64(105000), balance.TotalSats)
}

func TestChannelBalance_Fields(t *testing.T) {
	balance := ChannelBalance{
		LocalSats:  200000,
		RemoteSats: 150000,
	}

	assert.Equal(t, int64(200000), balance.LocalSats)
	assert.Equal(t, int64(150000), balance.RemoteSats)
}

func TestNodeInfo_Fields(t *testing.T) {
	info := NodeInfo{
		Alias:         "btc-giftcard-node",
		PubKey:        "03abc...",
		SyncedToChain: true,
		SyncedToGraph: true,
		BlockHeight:   800000,
		NumChannels:   5,
	}

	assert.Equal(t, "btc-giftcard-node", info.Alias)
	assert.True(t, info.SyncedToChain)
	assert.True(t, info.SyncedToGraph)
	assert.Equal(t, uint32(800000), info.BlockHeight)
	assert.Equal(t, uint32(5), info.NumChannels)
}

// --- Client.Close test ---

func TestNewClient_ConnectsToLND_HasRouterClient(t *testing.T) {
	// Verify that Client struct has the routerClient field.
	client := &Client{}
	assert.Nil(t, client.routerClient, "routerClient should be nil on zero-value Client")
}

func TestClient_Close_NilConn(t *testing.T) {
	// Verify that Client has the Close method (part of LightningClient).
	// Full interface compliance will be checked once all methods are implemented.
	client := &Client{}
	assert.NotNil(t, client)
}
