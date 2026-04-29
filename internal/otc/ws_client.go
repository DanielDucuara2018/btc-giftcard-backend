package otc

// ws_client.go implements the WebSocket RFQ (Request-for-Quote) flow required
// by the Crypto.com OTC 2.0 API.
//
// The REST client handles all stateless calls (request-deal, create-withdrawal,
// etc.). Getting a live quote requires a WebSocket connection because quotes
// expire in seconds and must be streamed in real time.
//
// Typical flow:
//  1. Call RequestQuote(ctx, params) — this method manages the full lifecycle:
//     a. Dial the private WebSocket endpoint.
//     b. Authenticate with HMAC-SHA256 (same algorithm as REST).
//     c. Subscribe to user.otc_qr.quotes.
//     d. Send private/otc/request-quote.
//     e. Block until a Quote arrives with matching ClQuoteReqID.
//  2. Immediately call the REST RequestDeal with the returned Quote's QuoteID
//     and exact leg prices — the quote expires in ~5 seconds.
//
// WebSocket endpoints:
//   - Production: wss://stream.crypto.com/exchange/v1/user
//   - UAT:        wss://uat-stream.3ona.co/exchange/v1/user
//
// The endpoint is derived automatically from Config.BaseURL; no extra config
// field is needed.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"context"

	"github.com/gorilla/websocket"
)

// ============================================================================
// Wire types
// ============================================================================

// wsRequest is the generic envelope for outbound WebSocket messages.
type wsRequest struct {
	ID     int64          `json:"id"`
	Method string         `json:"method"`
	APIKey string         `json:"api_key,omitempty"`
	Sig    string         `json:"sig,omitempty"`
	Nonce  int64          `json:"nonce,omitempty"`
	Params map[string]any `json:"params,omitempty"`
}

// wsResponse is the generic envelope for inbound WebSocket messages.
type wsResponse struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Code   int             `json:"code"`
	Result json.RawMessage `json:"result"`
}

// wsChannelResult is unpacked from wsResponse.Result for subscription pushes.
type wsChannelResult struct {
	Channel string          `json:"channel"`
	Data    json.RawMessage `json:"data"`
}

// ============================================================================
// URL derivation
// ============================================================================

// wsURLFromBase derives the private WebSocket endpoint from the REST base URL.
//
//	https://api.crypto.com/exchange/v1     → wss://stream.crypto.com/exchange/v1/user
//	https://uat-api.3ona.co/exchange/v1   → wss://uat-stream.3ona.co/exchange/v1/user
func wsURLFromBase(base string) string {
	u := strings.Replace(base, "https://", "wss://", 1)
	u = strings.Replace(u, "http://", "ws://", 1)
	// api.crypto.com → stream.crypto.com
	u = strings.Replace(u, "//api.", "//stream.", 1)
	// uat-api.3ona.co → uat-stream.3ona.co
	u = strings.Replace(u, "-api.", "-stream.", 1)
	return u + "/user"
}

// ============================================================================
// RequestQuote — the public entry point
// ============================================================================

// RequestQuote connects to the Crypto.com private WebSocket, authenticates,
// subscribes to the OTC quote channel, sends a quote request, and waits for a
// Quote to arrive with a matching ClQuoteReqID.
//
// The returned Quote expires within seconds. Pass its QuoteID and exact leg
// prices to RequestDeal immediately after this call returns.
//
// ctx controls the overall deadline. A 30-second timeout is recommended.
func (c *Client) RequestQuote(ctx context.Context, params RequestQuoteParams) (*Quote, error) {
	wsURL := wsURLFromBase(c.cfg.BaseURL)

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("otc: ws dial %s: %w", wsURL, err)
	}
	defer conn.Close()

	// Propagate context deadline to the underlying socket.
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetReadDeadline(deadline)
		conn.SetWriteDeadline(deadline)
	}

	if err := c.wsAuth(conn); err != nil {
		return nil, fmt.Errorf("otc: ws auth: %w", err)
	}
	if err := c.wsSubscribe(conn, "user.otc_qr.quotes"); err != nil {
		return nil, fmt.Errorf("otc: ws subscribe: %w", err)
	}
	if err := c.wsSendQuoteRequest(conn, params); err != nil {
		return nil, fmt.Errorf("otc: ws request-quote: %w", err)
	}
	quote, err := c.wsWaitForQuote(conn, params.ClQuoteReqID)
	if err != nil {
		return nil, fmt.Errorf("otc: ws wait for quote: %w", err)
	}
	return quote, nil
}

// ============================================================================
// Internal helpers
// ============================================================================

// wsAuth sends a public/auth message and waits for the acknowledgement.
// The signing algorithm is identical to the REST client (HMAC-SHA256).
func (c *Client) wsAuth(conn *websocket.Conn) error {
	nonce := time.Now().UnixMilli()
	id := nonce
	sig := c.sign("public/auth", strconv.FormatInt(id, 10), nonce, map[string]any{})

	msg := wsRequest{
		ID:     id,
		Method: "public/auth",
		APIKey: c.cfg.APIKey,
		Sig:    sig,
		Nonce:  nonce,
	}
	if err := conn.WriteJSON(msg); err != nil {
		return fmt.Errorf("send auth: %w", err)
	}

	resp, err := c.wsRead(conn)
	if err != nil {
		return fmt.Errorf("read auth response: %w", err)
	}
	if resp.Method != "public/auth" || resp.Code != 0 {
		return fmt.Errorf("auth failed: method=%s code=%d", resp.Method, resp.Code)
	}
	return nil
}

// wsSubscribe subscribes to a channel and waits for the subscription confirm.
func (c *Client) wsSubscribe(conn *websocket.Conn, channel string) error {
	nonce := time.Now().UnixMilli()
	msg := wsRequest{
		ID:     nonce,
		Method: "subscribe",
		Nonce:  nonce,
		Params: map[string]any{"channels": []string{channel}},
	}
	if err := conn.WriteJSON(msg); err != nil {
		return fmt.Errorf("send subscribe: %w", err)
	}

	// The server echoes back a subscribe response with code 0 on success.
	resp, err := c.wsRead(conn)
	if err != nil {
		return fmt.Errorf("read subscribe response: %w", err)
	}
	if resp.Method != "subscribe" || resp.Code != 0 {
		return fmt.Errorf("subscribe failed: method=%s code=%d", resp.Method, resp.Code)
	}
	return nil
}

// wsSendQuoteRequest sends a private/otc/request-quote message.
func (c *Client) wsSendQuoteRequest(conn *websocket.Conn, params RequestQuoteParams) error {
	nonce := time.Now().UnixMilli()
	id := nonce

	paramsMap, err := structToParams(params)
	if err != nil {
		return fmt.Errorf("encode params: %w", err)
	}

	sig := c.sign("private/otc/request-quote", strconv.FormatInt(id, 10), nonce, paramsMap)
	msg := wsRequest{
		ID:     id,
		Method: "private/otc/request-quote",
		APIKey: c.cfg.APIKey,
		Sig:    sig,
		Nonce:  nonce,
		Params: paramsMap,
	}
	if err := conn.WriteJSON(msg); err != nil {
		return fmt.Errorf("send request-quote: %w", err)
	}
	return nil
}

// wsWaitForQuote reads messages until a Quote arrives with the given clQuoteReqID,
// or until the context deadline is exceeded.
//
// The server may send intermediate messages (heartbeats, request ACKs) before
// the quote arrives — those are skipped transparently.
func (c *Client) wsWaitForQuote(conn *websocket.Conn, clQuoteReqID string) (*Quote, error) {
	for {
		resp, err := c.wsRead(conn)
		if err != nil {
			return nil, err
		}

		// Quote pushes arrive as subscribe channel events.
		if resp.Method != "subscribe" || len(resp.Result) == 0 {
			continue // heartbeat or other non-quote message — skip
		}

		var ch wsChannelResult
		if err := json.Unmarshal(resp.Result, &ch); err != nil {
			continue
		}
		if ch.Channel != "user.otc_qr.quotes" {
			continue
		}

		// Data is an array of quotes.
		var quotes []Quote
		if err := json.Unmarshal(ch.Data, &quotes); err != nil {
			return nil, fmt.Errorf("decode quotes: %w", err)
		}

		for i := range quotes {
			if quotes[i].ClQuoteReqID == clQuoteReqID {
				return &quotes[i], nil
			}
		}
		// Received quotes for a different request — keep waiting.
	}
}

// wsRead reads a single JSON message from the connection.
func (c *Client) wsRead(conn *websocket.Conn) (*wsResponse, error) {
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("read ws: %w", err)
	}
	var resp wsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode ws message: %w", err)
	}
	return &resp, nil
}
