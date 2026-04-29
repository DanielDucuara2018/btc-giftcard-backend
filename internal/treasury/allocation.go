package treasury

// Config holds treasury-level settings, sourced from config.toml [treasury].
type Config struct {
	// CryptoComIBAN is the Crypto.com OTC SEPA account IBAN that receives
	// fiat transfers from our Qonto account before BTC is purchased.
	CryptoComIBAN string

	// CryptoComBeneficiaryName is the legal name of the Crypto.com account
	// holder (required by SEPA transfer regulations).
	CryptoComBeneficiaryName string

	// CryptoComBeneficiaryID is the pre-registered Qonto beneficiary UUID
	// for the Crypto.com OTC account. Required for Qonto v2 SendTransfer.
	CryptoComBeneficiaryID string

	// LNDChannelPeerPubKey is the hex-encoded 33-byte public key of the Lightning
	// node to open a channel with.
	//
	// Production workflow: open the first channel manually via lncli:
	//   lncli connect <pubkey>@<host>
	//   lncli openchannel --node_key <pubkey> --local_amt <sats>
	// Once that channel is live, FundLNDChannel auto-discovers the peer from
	// ListChannels/ListPeers — these fields can remain empty from that point on.
	//
	// Only set these fields if you want the code itself to open channel #1
	// (e.g. automated testnet/regtest deployments).
	LNDChannelPeerPubKey string

	// LNDChannelPeerHost is the P2P address of the channel peer ("ip:port" or
	// "host.onion:port"). See LNDChannelPeerPubKey for when this is needed.
	LNDChannelPeerHost string

	// LNDChannelThresholdSats triggers a channel-open when the LND on-chain
	// confirmed balance exceeds this value (satoshis). Set to 0 to disable
	// automatic channel funding entirely.
	LNDChannelThresholdSats int64

	// LNDChannelTargetSats is the on-chain balance (in satoshis) to keep in
	// LND after opening a channel. Channel size = confirmed_balance - target.
	LNDChannelTargetSats int64
}

// LNDBalance is a point-in-time snapshot of the LND node's treasury.
type LNDBalance struct {
	OnChainConfirmedSats   int64
	OnChainUnconfirmedSats int64
	LightningLocalSats     int64 // our side of channels (spendable)
	LightningRemoteSats    int64 // counterparty side (receivable capacity)
	TotalSats              int64 // all confirmed funds
}
