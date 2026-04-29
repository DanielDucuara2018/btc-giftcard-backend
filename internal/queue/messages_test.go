package queue

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// FundCardMessage Tests
// =============================================================================

func TestFundCardMessage_ToJSON(t *testing.T) {
	msg := &FundCardMessage{
		CardID:          "550e8400-e29b-41d4-a716-446655440000",
		FiatAmountCents: 5000,
		FiatCurrency:    "USD",
	}

	data, err := msg.ToJSON()
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Verify it's valid JSON
	var result map[string]interface{}
	err = json.Unmarshal(data, &result)
	require.NoError(t, err)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", result["card_id"])
	assert.Equal(t, float64(5000), result["fiat_amount_cents"])
	assert.Equal(t, "USD", result["fiat_currency"])
}

func TestFromJSONFundCard_Success(t *testing.T) {
	jsonData := []byte(`{
		"card_id": "550e8400-e29b-41d4-a716-446655440000",
		"fiat_amount_cents": 10000,
		"fiat_currency": "EUR"
	}`)

	msg, err := FromJSONFundCard(jsonData)
	require.NoError(t, err)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", msg.CardID)
	assert.Equal(t, int64(10000), msg.FiatAmountCents)
	assert.Equal(t, "EUR", msg.FiatCurrency)
}

func TestFromJSONFundCard_InvalidJSON(t *testing.T) {
	jsonData := []byte(`invalid json`)

	msg, err := FromJSONFundCard(jsonData)
	assert.Error(t, err)
	assert.Nil(t, msg)
	assert.Contains(t, err.Error(), "failed to unmarshal")
}

func TestFromJSONFundCard_ValidationErrors(t *testing.T) {
	tests := []struct {
		name        string
		jsonData    string
		expectError string
	}{
		{
			name:        "Missing card_id",
			jsonData:    `{"fiat_amount_cents": 5000, "fiat_currency": "USD"}`,
			expectError: "card_id is required",
		},
		{
			name:        "Missing fiat_currency",
			jsonData:    `{"card_id": "123", "fiat_amount_cents": 5000}`,
			expectError: "fiat_currency is required",
		},
		{
			name:        "Zero amount",
			jsonData:    `{"card_id": "123", "fiat_amount_cents": 0, "fiat_currency": "USD"}`,
			expectError: "fiat_amount_cents must be greater than 0",
		},
		{
			name:        "Negative amount",
			jsonData:    `{"card_id": "123", "fiat_amount_cents": -100, "fiat_currency": "USD"}`,
			expectError: "fiat_amount_cents must be greater than 0",
		},
		{
			name:        "Invalid currency length",
			jsonData:    `{"card_id": "123", "fiat_amount_cents": 5000, "fiat_currency": "US"}`,
			expectError: "fiat_currency must be 3 characters",
		},
		{
			name:        "Currency too long",
			jsonData:    `{"card_id": "123", "fiat_amount_cents": 5000, "fiat_currency": "USDD"}`,
			expectError: "fiat_currency must be 3 characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := FromJSONFundCard([]byte(tt.jsonData))
			assert.Error(t, err)
			assert.Nil(t, msg)
			assert.Contains(t, err.Error(), tt.expectError)
		})
	}
}

func TestFundCardMessage_RoundTrip(t *testing.T) {
	original := &FundCardMessage{
		CardID:          "550e8400-e29b-41d4-a716-446655440000",
		FiatAmountCents: 7500,
		FiatCurrency:    "GBP",
	}

	// Serialize
	data, err := original.ToJSON()
	require.NoError(t, err)

	// Deserialize
	msg, err := FromJSONFundCard(data)
	require.NoError(t, err)

	// Compare
	assert.Equal(t, original.CardID, msg.CardID)
	assert.Equal(t, original.FiatAmountCents, msg.FiatAmountCents)
	assert.Equal(t, original.FiatCurrency, msg.FiatCurrency)
}

func TestFundCardMessage_Validate(t *testing.T) {
	tests := []struct {
		name        string
		msg         *FundCardMessage
		expectError bool
		errorText   string
	}{
		{
			name: "Valid message",
			msg: &FundCardMessage{
				CardID:          "123",
				FiatAmountCents: 1000,
				FiatCurrency:    "USD",
			},
			expectError: false,
		},
		{
			name: "Empty card_id",
			msg: &FundCardMessage{
				CardID:          "",
				FiatAmountCents: 1000,
				FiatCurrency:    "USD",
			},
			expectError: true,
			errorText:   "card_id is required",
		},
		{
			name: "Zero amount",
			msg: &FundCardMessage{
				CardID:          "123",
				FiatAmountCents: 0,
				FiatCurrency:    "USD",
			},
			expectError: true,
			errorText:   "fiat_amount_cents must be greater than 0",
		},
		{
			name: "Negative amount",
			msg: &FundCardMessage{
				CardID:          "123",
				FiatAmountCents: -500,
				FiatCurrency:    "USD",
			},
			expectError: true,
			errorText:   "fiat_amount_cents must be greater than 0",
		},
		{
			name: "Empty currency",
			msg: &FundCardMessage{
				CardID:          "123",
				FiatAmountCents: 1000,
				FiatCurrency:    "",
			},
			expectError: true,
			errorText:   "fiat_currency is required",
		},
		{
			name: "Invalid currency length",
			msg: &FundCardMessage{
				CardID:          "123",
				FiatAmountCents: 1000,
				FiatCurrency:    "US",
			},
			expectError: true,
			errorText:   "fiat_currency must be 3 characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.msg.Validate()
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorText)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
