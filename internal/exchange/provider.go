package exchange

import (
	"btc-giftcard/pkg/logger"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

type PriceProvider interface {
	GetPrice(ctx context.Context, fiatCurrency string) (float64, error)
}

type coinbase struct {
	httpClient *http.Client
	baseURL    string
}

type coingecko struct {
	httpClient *http.Client
	baseURL    string
}

type bitstamp struct {
	httpClient *http.Client
	baseURL    string
}

type cryptocom struct {
	httpClient *http.Client
	baseURL    string
}

const (
	coinbaseBaseURL   = "https://api.coinbase.com"
	coingeckoBaseURL  = "https://api.coingecko.com"
	bitstampBaseURL   = "https://www.bitstamp.net"
	cryptocompBaseURL = "https://api.crypto.com/exchange/v1"
)

type coinbasePriceResponse struct {
	Data struct {
		Amount   string `json:"amount"`
		Base     string `json:"base"`
		Currency string `json:"currency"`
	} `json:"data"`
}

type coingeckoPriceResponse map[string]map[string]float64

type bitstampPriceResponse struct {
	Last string `json:"last"`
	Ask  string `json:"ask"`
	Bid  string `json:"bid"`
}

type cryptocomPriceResponse struct {
	Result struct {
		Data []struct {
			LastTradePrice string `json:"a"`
		} `json:"data"`
	} `json:"result"`
}

// NewProvider creates a new price provider instance by name.
// Supported providers: "coinbase", "coingecko", "bitstamp", "cryptocom"
//
// Parameters:
//   - providerName: Name of the provider (case-insensitive)
//   - baseURL: Base URL for the API (empty string uses production URLs)
//   - httpClient: HTTP client to use (nil creates default with 10s timeout)
//
// Usage:
//   - Production: NewProvider("coinbase", "", nil)
//   - Testing: NewProvider("coinbase", "http://localhost:8080", testClient)
func NewProvider(providerName string, baseURL string, httpClient *http.Client) (PriceProvider, error) {
	providerName = strings.ToLower(providerName)

	// Use default HTTP client if none provided
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	// Use production URLs if baseURL is empty
	if baseURL == "" {
		switch providerName {
		case "coinbase":
			baseURL = coinbaseBaseURL
		case "coingecko":
			baseURL = coingeckoBaseURL
		case "bitstamp":
			baseURL = bitstampBaseURL
		case "cryptocom":
			baseURL = cryptocompBaseURL
		default:
			return nil, fmt.Errorf("unknown provider: %s (supported: coinbase, coingecko, bitstamp, cryptocom)", providerName)
		}
	}

	// Create provider instance
	switch providerName {
	case "coinbase":
		return &coinbase{httpClient: httpClient, baseURL: baseURL}, nil
	case "coingecko":
		return &coingecko{httpClient: httpClient, baseURL: baseURL}, nil
	case "bitstamp":
		return &bitstamp{httpClient: httpClient, baseURL: baseURL}, nil
	case "cryptocom":
		return &cryptocom{httpClient: httpClient, baseURL: baseURL}, nil
	default:
		return nil, fmt.Errorf("unknown provider: %s (supported: coinbase, coingecko, bitstamp, cryptocom)", providerName)
	}
}

// fetchJSON makes an HTTP GET request and decodes the JSON response into target.
// Uses the provided context for cancellation and the HTTP client for timeout.
func fetchJSON(ctx context.Context, client *http.Client, url string, target interface{}) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Make HTTP request
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("Failed to fetch price data", zap.String("url", url), zap.Error(err))
		return fmt.Errorf("failed to fetch data: %w", err)
	}
	defer resp.Body.Close()

	// Check HTTP status
	if resp.StatusCode != http.StatusOK {
		logger.Error("API returned error", zap.String("url", url), zap.Int("status", resp.StatusCode))
		return fmt.Errorf("API error: status %d", resp.StatusCode)
	}

	// Decode JSON response
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		logger.Error("Failed to decode JSON response", zap.String("url", url), zap.Error(err))
		return fmt.Errorf("failed to parse response: %w", err)
	}

	return nil
}

// GetPrice fetches the current BTC price in the specified fiat currency from Coinbase.
// Supported currencies: USD, EUR, GBP, etc.
func (c *coinbase) GetPrice(ctx context.Context, fiatCurrency string) (float64, error) {
	fiatCurrency = strings.ToUpper(fiatCurrency)
	apiURL := fmt.Sprintf("%s/v2/prices/BTC-%s/spot", c.baseURL, fiatCurrency)

	var response coinbasePriceResponse
	if err := fetchJSON(ctx, c.httpClient, apiURL, &response); err != nil {
		return 0, fmt.Errorf("coinbase: %w", err)
	}

	amount, err := strconv.ParseFloat(response.Data.Amount, 64)
	if err != nil {
		return 0, fmt.Errorf("coinbase: invalid price format: %w", err)
	}

	if amount <= 0 {
		return 0, fmt.Errorf("coinbase: invalid price value: %f", amount)
	}

	logger.Info("Fetched BTC price from Coinbase",
		zap.String("currency", fiatCurrency),
		zap.Float64("price", amount))

	return amount, nil
}

// GetPrice fetches the current BTC price in the specified fiat currency from CoinGecko.
// Supported currencies: usd, eur, gbp, etc. (lowercase)
func (c *coingecko) GetPrice(ctx context.Context, fiatCurrency string) (float64, error) {
	fiatCurrency = strings.ToLower(fiatCurrency)
	apiURL := fmt.Sprintf("%s/api/v3/simple/price?ids=bitcoin&vs_currencies=%s", c.baseURL, fiatCurrency)

	var response coingeckoPriceResponse
	if err := fetchJSON(ctx, c.httpClient, apiURL, &response); err != nil {
		return 0, fmt.Errorf("coingecko: %w", err)
	}

	if btcData, ok := response["bitcoin"]; ok {
		if amount, ok := btcData[fiatCurrency]; ok {
			if amount <= 0 {
				return 0, fmt.Errorf("coingecko: invalid price value: %f", amount)
			}
			logger.Info("Fetched BTC price from CoinGecko",
				zap.String("currency", fiatCurrency),
				zap.Float64("price", amount))
			return amount, nil
		}
	}

	return 0, fmt.Errorf("coingecko: currency %s not found in response", fiatCurrency)
}

// GetPrice fetches the current BTC price in the specified fiat currency from Bitstamp.
// Supported currencies: usd, eur, gbp (lowercase)
func (c *bitstamp) GetPrice(ctx context.Context, fiatCurrency string) (float64, error) {
	fiatCurrency = strings.ToLower(fiatCurrency)
	apiURL := fmt.Sprintf("%s/api/v2/ticker/btc%s", c.baseURL, fiatCurrency)

	var response bitstampPriceResponse
	if err := fetchJSON(ctx, c.httpClient, apiURL, &response); err != nil {
		return 0, fmt.Errorf("bitstamp: %w", err)
	}

	amount, err := strconv.ParseFloat(response.Last, 64)
	if err != nil {
		return 0, fmt.Errorf("bitstamp: invalid price format: %w", err)
	}

	if amount <= 0 {
		return 0, fmt.Errorf("bitstamp: invalid price value: %f", amount)
	}

	logger.Info("Fetched BTC price from Bitstamp",
		zap.String("currency", fiatCurrency),
		zap.Float64("price", amount))

	return amount, nil
}

func (c *cryptocom) GetPrice(ctx context.Context, fiatCurrency string) (float64, error) {
	// TODO: GET /public/get-ticker?instrument_name=BTC_<FIATCURRENCY> (uppercase).
	// Decode response into struct{ Result struct{ Data []struct{ A string `json:"a"` } `json:"data"` } `json:"result"` }.
	// Parse Data[0].A (last trade price) with strconv.ParseFloat, validate > 0, return.
	fiatCurrency = strings.ToUpper(fiatCurrency)
	apiURL := fmt.Sprintf("%s/public/get-tickers?instrument_name=BTC_%s", c.baseURL, fiatCurrency)

	var response cryptocomPriceResponse
	if err := fetchJSON(ctx, c.httpClient, apiURL, &response); err != nil {
		return 0, fmt.Errorf("crypto.com: %w", err)
	}

	amount, err := strconv.ParseFloat(response.Result.Data[0].LastTradePrice, 64)
	if err != nil {
		return 0, fmt.Errorf("crypto.com: invalid price format: %w", err)
	}

	if amount <= 0 {
		return 0, fmt.Errorf("crypto.com: invalid price value: %f", amount)
	}

	logger.Info("Fetched BTC price from Crypto.com",
		zap.String("currency", fiatCurrency),
		zap.Float64("price", amount))

	return amount, nil
}
