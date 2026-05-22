//go:build integration

package database

import (
	"btc-giftcard/pkg/logger"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	// Initialize logger for tests
	_ = logger.Init("development")
}

func TestCardRepository_Create(t *testing.T) {
	db := SetupTestDB(t)
	defer db.Close()
	defer CleanupTestDB(t, db)

	repo := NewCardRepository(db)
	ctx := context.Background()

	now := time.Now().UTC()
	card := &Card{
		ID:                 uuid.New().String(),
		PurchaseEmail:      "test@example.com",
		OwnerEmail:         "test@example.com",
		Code:               "TEST-1234-5678-ABCD",
		BTCAmountSats:      100000,
		FiatAmountCents:    5000,
		FiatCurrency:       "USD",
		PaymentMethod:      CardBlue,
		PaymentStatus:      PaymentPending,
		Status:             Created,
		CreatedAt:          now,
	}

	err := repo.Create(ctx, card)
	require.NoError(t, err)

	// Verify card was created by retrieving it
	retrieved, err := repo.GetByCode(ctx, card.Code)
	require.NoError(t, err)
	assert.Equal(t, card.ID, retrieved.ID)
	assert.Equal(t, card.Code, retrieved.Code)
	assert.Equal(t, card.BTCAmountSats, retrieved.BTCAmountSats)
	assert.Equal(t, Created, retrieved.Status)
	assert.WithinDuration(t, now, retrieved.CreatedAt, time.Second)
}

func TestCardRepository_Create_DuplicateCode(t *testing.T) {
	db := SetupTestDB(t)
	defer db.Close()
	defer CleanupTestDB(t, db)

	repo := NewCardRepository(db)
	ctx := context.Background()

	card1 := &Card{
		ID:                 uuid.New().String(),
		PurchaseEmail:      "test@example.com",
		OwnerEmail:         "test@example.com",
		Code:               "DUPLICATE-CODE-TEST",
		BTCAmountSats:      100000,
		FiatAmountCents:    5000,
		FiatCurrency:       "USD",
		PaymentMethod:      CardBlue,
		PaymentStatus:      PaymentPending,
		Status:             Created,
		CreatedAt:          time.Now().UTC(),
	}

	err := repo.Create(ctx, card1)
	require.NoError(t, err)

	// Try to create another card with same code
	card2 := &Card{
		ID:                 uuid.New().String(),
		PurchaseEmail:      "test2@example.com",
		OwnerEmail:         "test2@example.com",
		Code:               "DUPLICATE-CODE-TEST", // Same code!
		BTCAmountSats:      200000,
		FiatAmountCents:    10000,
		FiatCurrency:       "USD",
		PaymentMethod:      CardBlue,
		PaymentStatus:      PaymentPending,
		Status:             Created,
		CreatedAt:          time.Now().UTC(),
	}

	err = repo.Create(ctx, card2)
	assert.ErrorIs(t, err, ErrCardCodeExists)
}

func TestCardRepository_GetByCode_NotFound(t *testing.T) {
	db := SetupTestDB(t)
	defer db.Close()
	defer CleanupTestDB(t, db)

	repo := NewCardRepository(db)
	ctx := context.Background()

	card, err := repo.GetByCode(ctx, "NONEXISTENT-CODE")
	assert.ErrorIs(t, err, ErrCardNotFound)
	assert.Nil(t, card)
}

func TestCardRepository_GetByID(t *testing.T) {
	db := SetupTestDB(t)
	defer db.Close()
	defer CleanupTestDB(t, db)

	repo := NewCardRepository(db)
	ctx := context.Background()

	// Create a card first
	cardID := uuid.New().String()
	card := &Card{
		ID:                 cardID,
		PurchaseEmail:      "test@example.com",
		OwnerEmail:         "test@example.com",
		Code:               "GET-BY-ID-TEST",
		BTCAmountSats:      100000,
		FiatAmountCents:    5000,
		FiatCurrency:       "USD",
		PaymentMethod:      CardBlue,
		PaymentStatus:      PaymentPending,
		Status:             Created,
		CreatedAt:          time.Now().UTC(),
	}

	err := repo.Create(ctx, card)
	require.NoError(t, err)

	// Retrieve by ID
	retrieved, err := repo.GetByID(ctx, cardID)
	require.NoError(t, err)
	assert.Equal(t, cardID, retrieved.ID)
	assert.Equal(t, "GET-BY-ID-TEST", retrieved.Code)
}

func TestCardRepository_Update(t *testing.T) {
	db := SetupTestDB(t)
	defer db.Close()
	defer CleanupTestDB(t, db)

	repo := NewCardRepository(db)
	ctx := context.Background()

	// Create a card
	cardID := uuid.New().String()
	card := &Card{
		ID:                 cardID,
		PurchaseEmail:      "test@example.com",
		OwnerEmail:         "test@example.com",
		Code:               "UPDATE-TEST",
		BTCAmountSats:      0,
		FiatAmountCents:    5000,
		FiatCurrency:       "USD",
		PaymentMethod:      CardBlue,
		PaymentStatus:      PaymentPending,
		Status:             Created,
		CreatedAt:          time.Now().UTC(),
	}

	err := repo.Create(ctx, card)
	require.NoError(t, err)

	// Update to Active status with BTCAmountSats and funded_at timestamp
	satoshis := int64(100000)
	fundedAt := time.Now().UTC()
	err = repo.Update(ctx, cardID, Active, &satoshis, &fundedAt, nil)
	require.NoError(t, err)

	// Verify update
	retrieved, err := repo.GetByID(ctx, cardID)
	require.NoError(t, err)
	assert.Equal(t, Active, retrieved.Status)
	assert.Equal(t, int64(100000), retrieved.BTCAmountSats)
	assert.NotNil(t, retrieved.FundedAt)
	assert.WithinDuration(t, fundedAt, *retrieved.FundedAt, time.Second)
	assert.Nil(t, retrieved.RedeemedAt)

	// Update to Redeemed status with redeemed_at timestamp (BTCAmountSats preserved via COALESCE)
	redeemedAt := time.Now().UTC()
	err = repo.Update(ctx, cardID, Redeemed, nil, nil, &redeemedAt)
	require.NoError(t, err)

	// Verify both timestamps and BTCAmountSats are preserved
	retrieved, err = repo.GetByID(ctx, cardID)
	require.NoError(t, err)
	assert.Equal(t, Redeemed, retrieved.Status)
	assert.Equal(t, int64(100000), retrieved.BTCAmountSats)                  // Preserved via COALESCE
	assert.NotNil(t, retrieved.FundedAt)                                     // Should be preserved (COALESCE)
	assert.WithinDuration(t, fundedAt, *retrieved.FundedAt, time.Second)     // Verify funded time preserved
	assert.NotNil(t, retrieved.RedeemedAt)                                   // Should be set
	assert.WithinDuration(t, redeemedAt, *retrieved.RedeemedAt, time.Second) // Verify redeemed time set correctly
}

func TestCardRepository_Update_NotFound(t *testing.T) {
	db := SetupTestDB(t)
	defer db.Close()
	defer CleanupTestDB(t, db)

	repo := NewCardRepository(db)
	ctx := context.Background()

	err := repo.Update(ctx, uuid.New().String(), Active, nil, nil, nil)
	assert.ErrorIs(t, err, ErrCardNotFound)
}

func TestCardRepository_ListByUserID(t *testing.T) {
	db := SetupTestDB(t)
	defer db.Close()
	defer CleanupTestDB(t, db)

	repo := NewCardRepository(db)
	ctx := context.Background()

	userID := uuid.New().String()

	// Create multiple cards for the same user
	for i := 0; i < 3; i++ {
		card := &Card{
			ID:                 uuid.New().String(),
			UserID:             &userID,
			PurchaseEmail:      "test@example.com",
			OwnerEmail:         "test@example.com",
			Code:               "CODE-" + uuid.New().String(),
			BTCAmountSats:      100000,
			FiatAmountCents:    5000,
			FiatCurrency:       "USD",
			PaymentMethod:      CardBlue,
			PaymentStatus:      PaymentPending,
			Status:             Created,
			CreatedAt:          time.Now().UTC().Add(-time.Duration(i) * time.Hour), // Different timestamps
		}
		err := repo.Create(ctx, card)
		require.NoError(t, err)
	}

	// List cards for user
	cards, err := repo.ListByUserID(ctx, userID)
	require.NoError(t, err)
	assert.Len(t, cards, 3)

	// Verify they're sorted by created_at DESC (newest first)
	assert.True(t, cards[0].CreatedAt.After(cards[1].CreatedAt))
	assert.True(t, cards[1].CreatedAt.After(cards[2].CreatedAt))
}

func TestCardRepository_ListByUserID_Empty(t *testing.T) {
	db := SetupTestDB(t)
	defer db.Close()
	defer CleanupTestDB(t, db)

	repo := NewCardRepository(db)
	ctx := context.Background()

	cards, err := repo.ListByUserID(ctx, uuid.New().String())
	require.NoError(t, err)
	assert.Empty(t, cards)
}
