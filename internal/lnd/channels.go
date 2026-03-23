package lnd

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/lightningnetwork/lnd/lnrpc"
)

// ConnectPeer establishes an outbound P2P connection to a Lightning node.
// This must be called before OpenChannel if we are not already connected.
//
// pubKey is the 33-byte compressed node public key as a hex string (66 chars).
// host is the remote address as "ip:port" or "onionaddress.onion:port".
//
// If we are already connected to the peer, LND returns an "already connected"
// error. This function normalises that case and returns the peer details anyway.
func (c *Client) ConnectPeer(ctx context.Context, pubKey, host string) (*ConnectPeerResult, error) {
	if pubKey == "" {
		return nil, fmt.Errorf("pubKey must not be empty")
	}
	if host == "" {
		return nil, fmt.Errorf("host must not be empty")
	}

	req := &lnrpc.ConnectPeerRequest{
		Addr: &lnrpc.LightningAddress{
			Pubkey: pubKey,
			Host:   host,
		},
		Perm: false, // one-shot — LND will still reconnect automatically
	}

	_, err := c.lnClient.ConnectPeer(ctx, req)
	if err != nil {
		// LND surfaces "already connected to peer" as an error. Treat it as
		// success so callers can safely call ConnectPeer before every OpenChannel.
		if strings.Contains(err.Error(), "already connected") {
			return &ConnectPeerResult{PubKey: pubKey, Address: host}, nil
		}
		return nil, fmt.Errorf("failed to connect to peer %s@%s: %w", pubKey, host, err)
	}

	return &ConnectPeerResult{PubKey: pubKey, Address: host}, nil
}

// ListPeers returns all currently connected P2P peers.
func (c *Client) ListPeers(ctx context.Context) ([]Peer, error) {
	resp, err := c.lnClient.ListPeers(ctx, &lnrpc.ListPeersRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list peers: %w", err)
	}

	peers := make([]Peer, 0, len(resp.Peers))
	for _, p := range resp.Peers {
		peers = append(peers, Peer{
			PubKey:    p.PubKey,
			Address:   p.Address,
			BytesSent: p.BytesSent,
			BytesRecv: p.BytesRecv,
			SatsSent:  p.SatSent,
			SatsRecv:  p.SatRecv,
			Inbound:   p.Inbound,
			PingTime:  p.PingTime,
		})
	}
	return peers, nil
}

// OpenChannel opens a Lightning payment channel to an already-connected peer.
//
// localAmtSats  — our initial local balance in satoshis (minimum ~20 000 on testnet).
// pushAmtSats   — sats to push to the remote side at channel open (usually 0).
// targetConf    — desired on-chain confirmation target for the funding tx
//
//	(1 = next block, 6 = ~1 h, 144 = ~1 day).
//
// The channel is not usable until the funding transaction reaches
// 3 confirmations (testnet) or 6 (mainnet). Monitor with ListChannels.
func (c *Client) OpenChannel(ctx context.Context, peerPubKey string, localAmtSats, pushAmtSats int64, targetConf int32) (*OpenChannelResult, error) {
	if peerPubKey == "" {
		return nil, fmt.Errorf("peerPubKey must not be empty")
	}
	if localAmtSats <= 0 {
		return nil, fmt.Errorf("localAmtSats must be positive")
	}

	pubKeyBytes, err := hex.DecodeString(peerPubKey)
	if err != nil {
		return nil, fmt.Errorf("invalid peerPubKey (must be hex): %w", err)
	}

	req := &lnrpc.OpenChannelRequest{
		NodePubkey:         pubKeyBytes,
		LocalFundingAmount: localAmtSats,
		PushSat:            pushAmtSats,
		TargetConf:         targetConf,
		Private:            false, // public channel — announces to the network
	}

	resp, err := c.lnClient.OpenChannelSync(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to open channel to %s: %w", peerPubKey, err)
	}

	// Build the canonical channel point string "txid:index" from the response.
	// LND returns the txid as raw bytes (little-endian), so we reverse it for
	// the display representation used everywhere else in the Bitcoin ecosystem.
	var fundingTxID string
	switch v := resp.FundingTxid.(type) {
	case *lnrpc.ChannelPoint_FundingTxidBytes:
		// Bytes are little-endian; reverse to get the human-readable txid.
		b := make([]byte, len(v.FundingTxidBytes))
		copy(b, v.FundingTxidBytes)
		for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
			b[i], b[j] = b[j], b[i]
		}
		fundingTxID = hex.EncodeToString(b)
	case *lnrpc.ChannelPoint_FundingTxidStr:
		fundingTxID = v.FundingTxidStr
	}

	channelPoint := fmt.Sprintf("%s:%d", fundingTxID, resp.OutputIndex)

	return &OpenChannelResult{
		FundingTxID:  fundingTxID,
		OutputIndex:  resp.OutputIndex,
		ChannelPoint: channelPoint,
	}, nil
}

// ListChannels returns all open channels (both active and inactive).
// A channel is "active" when both ends are online and the channel is usable.
// A channel is "inactive" when the peer is offline or the channel is pending close.
func (c *Client) ListChannels(ctx context.Context) ([]Channel, error) {
	resp, err := c.lnClient.ListChannels(ctx, &lnrpc.ListChannelsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list channels: %w", err)
	}

	channels := make([]Channel, 0, len(resp.Channels))
	for _, ch := range resp.Channels {
		channels = append(channels, Channel{
			Active:            ch.Active,
			RemotePubKey:      ch.RemotePubkey,
			ChannelPoint:      ch.ChannelPoint,
			ChanID:            ch.ChanId,
			CapacitySats:      ch.Capacity,
			LocalBalanceSats:  ch.LocalBalance,
			RemoteBalanceSats: ch.RemoteBalance,
			TotalSatsSent:     ch.TotalSatoshisSent,
			TotalSatsRecv:     ch.TotalSatoshisReceived,
			NumUpdates:        ch.NumUpdates,
			Private:           ch.Private,
		})
	}
	return channels, nil
}
