//go:build integration

package card

import (
	"btc-giftcard/internal/database"
	messages "btc-giftcard/internal/queue"
	"btc-giftcard/pkg/logger"
	streams "btc-giftcard/pkg/queue"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	// Initialize logger for tests
	_ = logger.Init("development")
}

// setupTestService creates a test service instance with database and repositories
func setupTestService(t *testing.T) (*Service, *database.DB, *database.CardRepository, *redis.Client) {
	t.Helper()

	db := database.SetupTestDB(t)

	cardRepo := database.NewCardRepository(db)
	txRepo := database.NewTransactionRepository(db)

	// Setup Redis for queue
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   1, // Use DB 1 for tests to avoid conflicts
	})

	// Clear test stream
	ctx := context.Background()
	redisClient.Del(ctx, "fund_card")

	// Create queue
	queue := streams.NewStreamQueue(redisClient)
	err := queue.DeclareStream(ctx, "fund_card", "test_workers")
	require.NoError(t, err)

	service := NewService(db, cardRepo, txRepo, "testnet", queue, nil)

	return service, db, cardRepo, redisClient
}

func TestService_CreateCard(t *testing.T) {
	service, db, cardRepo, redisClient := setupTestService(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	ctx := context.Background()
	userID := uuid.New().String()
	email := "test@example.com"

	req := CreateCardRequest{
		FiatAmountCents:    10000, // $100
		FiatCurrency:       "USD",
		PurchasePriceCents: 10500, // $105 with fees
		UserID:             &userID,
		PurchaseEmail:      email,
	}

	// Execute
	resp, err := service.CreateCard(ctx, req)

	// Assert
	require.NoError(t, err)
	assert.NotEmpty(t, resp.CardID)
	assert.NotEmpty(t, resp.Code)
	assert.Equal(t, int64(0), resp.BTCAmountSats) // 0 until funded by worker
	assert.Equal(t, database.Created, resp.Status)
	assert.WithinDuration(t, time.Now().UTC(), resp.CreatedAt, 2*time.Second)

	// Verify code format: GIFT-XXXX-YYYY-ZZZZ
	assert.Regexp(t, `^GIFT-[A-Z2-9]{4}-[A-Z2-9]{4}-[A-Z2-9]{4}$`, resp.Code)

	// Verify card was saved in database
	savedCard, err := cardRepo.GetByID(ctx, resp.CardID)
	require.NoError(t, err)
	assert.Equal(t, resp.Code, savedCard.Code)
	assert.Equal(t, userID, *savedCard.UserID)
	assert.Equal(t, email, savedCard.PurchaseEmail)
	assert.Equal(t, email, savedCard.OwnerEmail) // Initially same as purchaser
	assert.Equal(t, int64(0), savedCard.BTCAmountSats)
	assert.Nil(t, savedCard.FundedAt)
	assert.Nil(t, savedCard.RedeemedAt)

	// Verify message was published to queue
	time.Sleep(100 * time.Millisecond) // Give Redis time to process

	result, err := redisClient.XRead(ctx, &redis.XReadArgs{
		Streams: []string{"fund_card", "0"},
		Count:   1,
	}).Result()
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Len(t, result[0].Messages, 1)

	// Verify message content
	msgData := result[0].Messages[0].Values["data"].(string)
	msg, err := messages.FromJSONFundCard([]byte(msgData))
	require.NoError(t, err)
	assert.Equal(t, resp.CardID, msg.CardID)
	assert.Equal(t, int64(10000), msg.FiatAmountCents)
	assert.Equal(t, "USD", msg.FiatCurrency)
}

func TestService_CreateCard_WithoutOptionalFields(t *testing.T) {
	service, db, cardRepo, redisClient := setupTestService(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	ctx := context.Background()

	req := CreateCardRequest{
		FiatAmountCents:    5000,
		FiatCurrency:       "EUR",
		PurchasePriceCents: 5200,
		PurchaseEmail:      "anonymous@example.com",
		UserID:             nil, // No user ID
	}

	// Execute
	resp, err := service.CreateCard(ctx, req)

	// Assert
	require.NoError(t, err)
	assert.NotEmpty(t, resp.CardID)
	assert.NotEmpty(t, resp.Code)

	// Verify in database
	savedCard, err := cardRepo.GetByID(ctx, resp.CardID)
	require.NoError(t, err)
	assert.Nil(t, savedCard.UserID)
	assert.Equal(t, "anonymous@example.com", savedCard.PurchaseEmail)
	assert.Equal(t, "anonymous@example.com", savedCard.OwnerEmail)
	assert.Equal(t, "EUR", savedCard.FiatCurrency)
}

func TestService_CreateCard_GeneratesUniqueCode(t *testing.T) {
	service, db, _, redisClient := setupTestService(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	ctx := context.Background()

	// Create multiple cards
	codes := make(map[string]bool)
	for i := 0; i < 10; i++ {
		req := CreateCardRequest{
			FiatAmountCents:    10000,
			FiatCurrency:       "USD",
			PurchasePriceCents: 10500,
			PurchaseEmail:      "test@example.com",
		}

		resp, err := service.CreateCard(ctx, req)
		require.NoError(t, err)

		// Verify code is unique
		assert.False(t, codes[resp.Code], "Duplicate code generated: %s", resp.Code)
		codes[resp.Code] = true
	}

	assert.Equal(t, 10, len(codes), "Should generate 10 unique codes")
}

func TestService_CreateCard_AllFieldsPopulated(t *testing.T) {
	service, db, cardRepo, redisClient := setupTestService(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	ctx := context.Background()
	userID := uuid.New().String()
	email := "buyer@test.com"

	req := CreateCardRequest{
		FiatAmountCents:    25000,
		FiatCurrency:       "GBP",
		PurchasePriceCents: 26000,
		UserID:             &userID,
		PurchaseEmail:      email,
	}

	// Execute
	resp, err := service.CreateCard(ctx, req)
	require.NoError(t, err)

	// Verify all fields in database
	savedCard, err := cardRepo.GetByID(ctx, resp.CardID)
	require.NoError(t, err)

	assert.Equal(t, resp.CardID, savedCard.ID)
	assert.Equal(t, userID, *savedCard.UserID)
	assert.Equal(t, email, savedCard.PurchaseEmail)
	assert.Equal(t, email, savedCard.OwnerEmail) // Initially same as purchaser
	assert.Equal(t, resp.Code, savedCard.Code)
	assert.Equal(t, int64(0), savedCard.BTCAmountSats) // 0 until funded by worker
	assert.Equal(t, int64(25000), savedCard.FiatAmountCents)
	assert.Equal(t, "GBP", savedCard.FiatCurrency)
	assert.Equal(t, int64(26000), savedCard.PurchasePriceCents)
	assert.Equal(t, database.Created, savedCard.Status)
	assert.WithinDuration(t, time.Now().UTC(), savedCard.CreatedAt, 2*time.Second)
	assert.Nil(t, savedCard.FundedAt)
	assert.Nil(t, savedCard.RedeemedAt)
}

func TestService_CreateCard_CodeExcludesConfusingCharacters(t *testing.T) {
	service, db, _, redisClient := setupTestService(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	ctx := context.Background()

	// Create multiple cards and verify no confusing characters
	confusingChars := []string{"O", "0", "I", "1", "L"}

	for i := 0; i < 20; i++ {
		req := CreateCardRequest{
			FiatAmountCents:    10000,
			FiatCurrency:       "USD",
			PurchasePriceCents: 10500,
			PurchaseEmail:      "test@example.com",
		}

		resp, err := service.CreateCard(ctx, req)
		require.NoError(t, err)

		// Check code doesn't contain confusing characters
		for _, char := range confusingChars {
			assert.NotContains(t, strings.TrimPrefix(resp.Code, "GIFT-"), char,
				"Code should not contain confusing character: %s", char)
		}
	}
}

func TestService_generateCardCode_RetriesOnDuplicate(t *testing.T) {
	service, db, cardRepo, redisClient := setupTestService(t)
	defer db.Close()
	defer redisClient.Close()
	defer database.CleanupTestDB(t, db)

	ctx := context.Background()

	// Create a card with a specific code
	existingCard := &database.Card{
		ID:                 uuid.New().String(),
		PurchaseEmail:      "test@example.com",
		OwnerEmail:         "test@example.com",
		Code:               "GIFT-TEST-CODE-0001",
		BTCAmountSats:      100000,
		FiatAmountCents:    1000,
		FiatCurrency:       "USD",
		PurchasePriceCents: 1050,
		Status:             database.Created,
		CreatedAt:          time.Now().UTC(),
	}

	err := cardRepo.Create(ctx, existingCard)
	require.NoError(t, err)

	// Try to generate codes - should not return the existing code
	codes := make(map[string]bool)
	for i := 0; i < 10; i++ {
		code, err := service.generateCardCode(ctx)
		require.NoError(t, err)
		codes[code] = true
	}

	// Verify the existing code is not in the generated codes
	assert.NotContains(t, codes, "GIFT-TEST-CODE-0001")
}
