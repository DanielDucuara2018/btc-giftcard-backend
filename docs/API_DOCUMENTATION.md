# BTC Gift Card - API Documentation

> Auto-generated from Go documentation  
> Date: February 8, 2026

## Table of Contents

- [Message Queue](#message-queue-internalqueue)
- [Exchange Providers](#exchange-providers-internalexchange)  
- [Redis Streams](#redis-streams-pkgqueue)
- [Redis Cache](#redis-cache-pkgcache)
- [Card Service](#card-service-internalcard)
- [Encryption](#encryption-internalcrypto)
- [Database](#database-internaldatabase)

---

## Message Queue (internal/queue)

### FundCardMessage

Represents a request to fund a gift card with BTC.

```go
type FundCardMessage struct {
    CardID          string `json:"card_id"`
    FiatAmountCents int64  `json:"fiat_amount_cents"`
    FiatCurrency    string `json:"fiat_currency"`
}
```

**Methods:**
- `ToJSON() ([]byte, error)` - Serializes the message to JSON bytes
- `Validate() error` - Validates all required fields with valid values

**Functions:**
- `FromJSONFundCard(data []byte) (*FundCardMessage, error)` - Deserializes and validates JSON

**Example:**
```go
msg := &FundCardMessage{
    CardID:          "550e8400-e29b-41d4-a716-446655440000",
    FiatAmountCents: 5000,  // $50.00
    FiatCurrency:    "USD",
}
data, _ := msg.ToJSON()
```

---

## Exchange Providers (internal/exchange)

### PriceProvider Interface

```go
type PriceProvider interface {
    GetPrice(ctx context.Context, fiatCurrency string) (float64, error)
}
```

### NewProvider

Creates a new price provider instance by name.

```go
func NewProvider(providerName string, baseURL string, httpClient *http.Client) (PriceProvider, error)
```

**Supported providers:** `coinbase`, `coingecko`, `bitstamp` (case-insensitive)

**Parameters:**
- `providerName` - Name of the provider (e.g., "coinbase")
- `baseURL` - Base URL for the API (empty string "" uses production URLs)
- `httpClient` - HTTP client to use (nil creates default with 10s timeout)

**Production Usage:**
```go
provider, err := exchange.NewProvider("coinbase", "", nil)
if err != nil {
    log.Fatal(err)
}

ctx := context.Background()
price, err := provider.GetPrice(ctx, "USD")
// price = 67000.50
```

**Testing Usage:**
```go
mockServer := httptest.NewServer(handler)
provider, _ := exchange.NewProvider("coinbase", mockServer.URL, mockServer.Client())
```

---

## Redis Streams (pkg/queue)

### StreamQueue

Wraps Redis client for stream-based message queue operations.

```go
type StreamQueue struct {
    // unexported fields
}
```

### NewStreamQueue

```go
func NewStreamQueue(client *redis.Client) *StreamQueue
```

Creates a new StreamQueue instance with the provided Redis client.

---

### DeclareStream

```go
func (q *StreamQueue) DeclareStream(ctx context.Context, stream string, group string) error
```

Ensures a consumer group exists for the given stream. Creates the group if it doesn't exist.  
Handles `BUSYGROUP` error gracefully (group already exists).

**Example:**
```go
err := queue.DeclareStream(ctx, "fund_card", "workers")
```

---

### Publish

```go
func (q *StreamQueue) Publish(ctx context.Context, stream string, data []byte) (string, error)
```

Adds a message to the specified stream. Returns the generated message ID.

**Example:**
```go
msg := &FundCardMessage{CardID: "123", FiatAmountCents: 5000, FiatCurrency: "USD"}
data, _ := msg.ToJSON()
msgID, err := queue.Publish(ctx, "fund_card", data)
// msgID = "1234567890123-0"
```

---

### Consume

```go
func (q *StreamQueue) Consume(
    ctx context.Context,
    stream string,
    group string,
    consumer string,
    handler func(messageID string, data []byte) error,
) error
```

Starts consuming messages from the stream as part of a consumer group.  
Runs in a blocking loop until context is cancelled.  
Handler is called for each message; if it returns nil, message is ACKed.

**Example:**
```go
handler := func(messageID string, data []byte) error {
    msg, err := queue.FromJSONFundCard(data)
    if err != nil {
        return err
    }
    // Process message...
    return nil
}

err := queue.Consume(ctx, "fund_card", "workers", "worker-1", handler)
```

---

## Redis Cache (pkg/cache)

### Global Variable

```go
var Client *redis.Client
```

Global Redis client instance used by the cache package.

---

### Init

```go
func Init(cfg Config) error
```

Initializes the Redis client with the provided configuration.  
Tests the connection with a Ping command and sets the global Client variable.

**Config:**
```go
type Config struct {
    Host     string
    Port     string
    Password string
    DB       int
}
```

**Example:**
```go
err := cache.Init(cache.Config{
    Host:     "localhost",
    Port:     "6379",
    Password: "",
    DB:       0,
})
```

---

### Basic Operations

#### Get
```go
func Get(ctx context.Context, key string) (string, error)
```
Retrieves the value of a key from Redis. Returns empty string if key doesn't exist.

#### Set
```go
func Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
```
Stores a key-value pair with the specified expiration time. Use `0` for no expiration.

#### SetNX
```go
func SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) (bool, error)
```
Sets a key-value pair only if the key does not exist (SET if Not eXists).  
Returns `true` if the key was set, `false` if key already exists.  
Useful for distributed locking.

**Example (Distributed Lock):**
```go
locked, _ := cache.SetNX(ctx, "lock:card:123", "worker-1", 30*time.Second)
if !locked {
    return errors.New("card is locked by another process")
}
defer cache.Delete(ctx, "lock:card:123")
```

#### Other Operations
- `Delete(ctx, keys ...string) (int64, error)` - Removes keys
- `Exists(ctx, key string) (bool, error)` - Checks if key exists
- `Expire(ctx, key string, expiration time.Duration) error` - Sets expiration
- `Incr(ctx, key string) (int64, error)` - Increments value by one

---

## Command Reference

### View Package Documentation

```bash
# View all documentation for a package
go doc -all btc-giftcard/internal/queue
go doc -all btc-giftcard/internal/exchange
go doc -all btc-giftcard/pkg/queue

# View specific type
go doc btc-giftcard/internal/queue.FundCardMessage

# View specific function
go doc btc-giftcard/internal/exchange.NewProvider
```

### Run Tests

```bash
# All tests
go test ./...

# Specific package with verbose output
go test ./internal/queue/... -v

# Skip integration tests
go test ./... -short

# With coverage
go test ./... -cover
```
