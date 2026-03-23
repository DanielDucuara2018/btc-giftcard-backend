package main

import (
	"encoding/json"
	"net/http"
)

// ============================================================================
// LND node-management handlers
// ============================================================================

// getNodeInfo handles GET /api/node/info
//
// Returns basic information about the connected LND node: alias, pubkey,
// sync status, chain, and number of active/pending channels.
//
// Response 200:
//
//	{
//	  "alias": "alice",
//	  "pubkey": "03abc...",
//	  "synced_to_chain": true,
//	  "block_height": 840000,
//	  "num_active_channels": 3,
//	  "num_pending_channels": 1,
//	  "num_peers": 5
//	}
func (h *handler) getNodeInfo(w http.ResponseWriter, r *http.Request) {
	info, err := h.lndClient.GetInfo(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to get node info", nil)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// getWalletBalance handles GET /api/node/wallet/balance
//
// Returns the on-chain wallet balances (confirmed, unconfirmed, total).
//
// Response 200:
//
//	{
//	  "total_balance_sats": 5000000,
//	  "confirmed_balance_sats": 4900000,
//	  "unconfirmed_balance_sats": 100000
//	}
func (h *handler) getWalletBalance(w http.ResponseWriter, r *http.Request) {
	bal, err := h.lndClient.GetWalletBalance(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to get wallet balance", nil)
		return
	}
	writeJSON(w, http.StatusOK, bal)
}

// getChannelBalance handles GET /api/node/channels/balance
//
// Returns aggregate Lightning channel balance (local, remote, pending).
//
// Response 200: lnd.ChannelBalance JSON
func (h *handler) getChannelBalance(w http.ResponseWriter, r *http.Request) {
	bal, err := h.lndClient.GetChannelBalance(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to get channel balance", nil)
		return
	}
	writeJSON(w, http.StatusOK, bal)
}

// newWalletAddress handles POST /api/node/wallet/address
//
// Generates a new on-chain Bitcoin address for the LND wallet.
// A new address is generated on every call (HD wallet derivation).
//
// Response 201:
//
//	{ "address": "tb1q..." }
func (h *handler) newWalletAddress(w http.ResponseWriter, r *http.Request) {
	addr, err := h.lndClient.NewAddress(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to generate address", nil)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"address": addr})
}

// listPeers handles GET /api/node/peers
//
// Returns all currently connected P2P peers.
//
// Response 200: array of lnd.Peer JSON objects
func (h *handler) listPeers(w http.ResponseWriter, r *http.Request) {
	peers, err := h.lndClient.ListPeers(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list peers", nil)
		return
	}
	writeJSON(w, http.StatusOK, peers)
}

// connectPeer handles POST /api/node/peers
//
// Establishes an outbound P2P connection to a Lightning node. Call this
// before openChannel when not already connected.
//
// Request body:
//
//	{
//	  "pub_key": "03abc...66hex...",
//	  "host":    "192.0.2.1:9735"
//	}
//
// Response 200:
//
//	{
//	  "pub_key": "03abc...",
//	  "address": "192.0.2.1:9735"
//	}
func (h *handler) connectPeer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PubKey string `json:"pub_key"`
		Host   string `json:"host"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON", nil)
		return
	}
	if req.PubKey == "" || req.Host == "" {
		writeError(w, http.StatusBadRequest, "pub_key and host are required", nil)
		return
	}

	result, err := h.lndClient.ConnectPeer(r.Context(), req.PubKey, req.Host)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to connect to peer", nil)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// listChannels handles GET /api/node/channels
//
// Returns all open channels (active and inactive).
//
// Response 200: array of lnd.Channel JSON objects
func (h *handler) listChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := h.lndClient.ListChannels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list channels", nil)
		return
	}
	writeJSON(w, http.StatusOK, channels)
}

// openChannel handles POST /api/node/channels
//
// Opens a Lightning payment channel to an already-connected peer.
// Ensure the node is connected via POST /api/node/peers first.
//
// localAmtSats  — our initial local balance in satoshis (min ~20 000 on testnet).
// pushAmtSats   — sats pushed to the remote side at open (usually 0).
// targetConf    — on-chain confirmation target (1 = next block, 6 = ~1 h).
//
// Request body:
//
//	{
//	  "peer_pub_key":   "03abc...",
//	  "local_amt_sats": 100000,
//	  "push_amt_sats":  0,
//	  "target_conf":    6
//	}
//
// Response 201:
//
//	{
//	  "funding_txid":  "abc123...",
//	  "output_index":  0,
//	  "channel_point": "abc123...:0"
//	}
func (h *handler) openChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PeerPubKey   string `json:"peer_pub_key"`
		LocalAmtSats int64  `json:"local_amt_sats"`
		PushAmtSats  int64  `json:"push_amt_sats"`
		TargetConf   int32  `json:"target_conf"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON", nil)
		return
	}
	if req.PeerPubKey == "" {
		writeError(w, http.StatusBadRequest, "peer_pub_key is required", nil)
		return
	}
	if req.LocalAmtSats <= 0 {
		writeError(w, http.StatusBadRequest, "local_amt_sats must be positive", nil)
		return
	}
	if req.TargetConf <= 0 {
		req.TargetConf = 6 // sensible default (~1 hour)
	}

	result, err := h.lndClient.OpenChannel(r.Context(), req.PeerPubKey, req.LocalAmtSats, req.PushAmtSats, req.TargetConf)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to open channel", nil)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}
