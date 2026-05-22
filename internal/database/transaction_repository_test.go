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

func TestTransactionRepository_Create(t *testing.T) {
	db := SetupTestDB(t)
	defer db.Close()
	defer CleanupTestDB(t, db)

	cardRepo := NewCardRepository(db)
	txRepo := NewTransactionRepository(db)
	ctx := context.Background()

	// Create a card first (transactions need a valid card_id)
	cardID := uuid.New().String()
	card := &Card{
		ID:                 cardID,
		PurchaseEmail:      "test@example.com",
		OwnerEmail:         "test@example.com",
		Code:               "TX-TEST-CARD",
		BTCAmountSats:      100000,
		FiatAmountCents:    5000,
		FiatCurrency:       "USD",
		PaymentMethod:      CardBlue,
		PaymentStatus:      PaymentPending,
		Status:             Created,
		CreatedAt:          time.Now().UTC(),
	}
	err := cardRepo.Create(ctx, card)
	require.NoError(t, err)

	// Create a transaction
	txID := uuid.New().String()
	now := time.Now().UTC()
	toAddr := "tb1qtestaddr"
	tx := &Transaction{
		ID:            txID,
		CardID:        cardID,
		Type:          Fund,
		TxHash:        nil, // Not broadcast yet
		FromAddress:   nil,
		ToAddress:     &toAddr,
		BTCAmountSats: 100000,
		Status:        Pending,
		Confirmations: 0,
		CreatedAt:     now,
		BroadcastAt:   nil,
		ConfirmedAt:   nil,
	}

	err = txRepo.Create(ctx, tx)
	require.NoError(t, err)

	// Verify transaction was created
	retrieved, err := txRepo.GetByID(ctx, txID)
	require.NoError(t, err)
	assert.Equal(t, txID, retrieved.ID)
	assert.Equal(t, cardID, retrieved.CardID)
	assert.Equal(t, Fund, retrieved.Type)
	assert.Equal(t, Pending, retrieved.Status)
	assert.Equal(t, int64(100000), retrieved.BTCAmountSats)
	assert.Nil(t, retrieved.TxHash)
	assert.WithinDuration(t, now, retrieved.CreatedAt, time.Second)
}

func TestTransactionRepository_Create_WithTxHash(t *testing.T) {
	db := SetupTestDB(t)
	defer db.Close()
	defer CleanupTestDB(t, db)

	cardRepo := NewCardRepository(db)
	txRepo := NewTransactionRepository(db)
	ctx := context.Background()

	// Create a card
	cardID := uuid.New().String()
	card := &Card{
		ID:                 cardID,
		PurchaseEmail:      "test@example.com",
		OwnerEmail:         "test@example.com",
		Code:               "TX-HASH-TEST",
		BTCAmountSats:      100000,
		FiatAmountCents:    5000,
		FiatCurrency:       "USD",
		PaymentMethod:      CardBlue,
		PaymentStatus:      PaymentPending,
		Status:             Created,
		CreatedAt:          time.Now().UTC(),
	}
	err := cardRepo.Create(ctx, card)
	require.NoError(t, err)

	// Create transaction with tx_hash (broadcast transaction)
	txHash := "a1b2c3d4e5f67890123456789012345678901234567890123456789012345678"
	fromAddr := "tb1qfromaddress"
	toAddr := "tb1qtoaddress"
	broadcastTime := time.Now().UTC()

	tx := &Transaction{
		ID:            uuid.New().String(),
		CardID:        cardID,
		Type:          Fund,
		TxHash:        &txHash,
		FromAddress:   &fromAddr,
		ToAddress:     &toAddr,
		BTCAmountSats: 100000,
		Status:        Pending,
		Confirmations: 0,
		CreatedAt:     time.Now().UTC(),
		BroadcastAt:   &broadcastTime,
		ConfirmedAt:   nil,
	}

	err = txRepo.Create(ctx, tx)
	require.NoError(t, err)

	// Retrieve by tx_hash
	retrieved, err := txRepo.GetByTxHash(ctx, txHash)
	require.NoError(t, err)
	assert.Equal(t, tx.ID, retrieved.ID)
	assert.NotNil(t, retrieved.TxHash)
	assert.Equal(t, txHash, *retrieved.TxHash)
	assert.NotNil(t, retrieved.BroadcastAt)
	assert.WithinDuration(t, broadcastTime, *retrieved.BroadcastAt, time.Second)
}

func TestTransactionRepository_GetByID_NotFound(t *testing.T) {
	db := SetupTestDB(t)
	defer db.Close()
	defer CleanupTestDB(t, db)

	txRepo := NewTransactionRepository(db)
	ctx := context.Background()

	tx, err := txRepo.GetByID(ctx, uuid.New().String())
	assert.ErrorIs(t, err, ErrTransactionNotFound)
	assert.Nil(t, tx)
}

func TestTransactionRepository_GetByTxHash_NotFound(t *testing.T) {
	db := SetupTestDB(t)
	defer db.Close()
	defer CleanupTestDB(t, db)

	txRepo := NewTransactionRepository(db)
	ctx := context.Background()

	tx, err := txRepo.GetByTxHash(ctx, "nonexistent_tx_hash")
	assert.ErrorIs(t, err, ErrTransactionNotFound)
	assert.Nil(t, tx)
}

func TestTransactionRepository_ListByCardID(t *testing.T) {
	db := SetupTestDB(t)
	defer db.Close()
	defer CleanupTestDB(t, db)

	cardRepo := NewCardRepository(db)
	txRepo := NewTransactionRepository(db)
	ctx := context.Background()

	// Create a card
	cardID := uuid.New().String()
	card := &Card{
		ID:                 cardID,
		PurchaseEmail:      "test@example.com",
		OwnerEmail:         "test@example.com",
		Code:               "LIST-TX-TEST",
		BTCAmountSats:      100000,
		FiatAmountCents:    5000,
		FiatCurrency:       "USD",
		PaymentMethod:      CardBlue,
		PaymentStatus:      PaymentPending,
		Status:             Created,
		CreatedAt:          time.Now().UTC(),
	}
	err := cardRepo.Create(ctx, card)
	require.NoError(t, err)

	// Create multiple transactions
	toAddr := "tb1qtestaddr"
	for i := 0; i < 3; i++ {
		tx := &Transaction{
			ID:            uuid.New().String(),
			CardID:        cardID,
			Type:          Fund,
			TxHash:        nil,
			FromAddress:   nil,
			ToAddress:     &toAddr,
			BTCAmountSats: int64(10000 * (i + 1)),
			Status:        Pending,
			Confirmations: 0,
			CreatedAt:     time.Now().UTC().Add(-time.Duration(i) * time.Hour), // Different timestamps
			BroadcastAt:   nil,
			ConfirmedAt:   nil,
		}
		err := txRepo.Create(ctx, tx)
		require.NoError(t, err)
	}

	// List transactions
	transactions, err := txRepo.ListByCardID(ctx, cardID)
	require.NoError(t, err)
	assert.Len(t, transactions, 3)

	// Verify ordering (newest first)
	assert.True(t, transactions[0].CreatedAt.After(transactions[1].CreatedAt))
	assert.True(t, transactions[1].CreatedAt.After(transactions[2].CreatedAt))

	// Verify amounts
	assert.Equal(t, int64(10000), transactions[0].BTCAmountSats)
	assert.Equal(t, int64(20000), transactions[1].BTCAmountSats)
	assert.Equal(t, int64(30000), transactions[2].BTCAmountSats)
}

func TestTransactionRepository_ListByCardID_Empty(t *testing.T) {
	db := SetupTestDB(t)
	defer db.Close()
	defer CleanupTestDB(t, db)

	txRepo := NewTransactionRepository(db)
	ctx := context.Background()

	transactions, err := txRepo.ListByCardID(ctx, uuid.New().String())
	require.NoError(t, err)
	assert.Empty(t, transactions)
}

func TestTransactionRepository_Update(t *testing.T) {
	db := SetupTestDB(t)
	defer db.Close()
	defer CleanupTestDB(t, db)

	cardRepo := NewCardRepository(db)
	txRepo := NewTransactionRepository(db)
	ctx := context.Background()

	// Create a card
	cardID := uuid.New().String()
	card := &Card{
		ID:                 cardID,
		PurchaseEmail:      "test@example.com",
		OwnerEmail:         "test@example.com",
		Code:               "UPDATE-TX-TEST",
		BTCAmountSats:      100000,
		FiatAmountCents:    5000,
		FiatCurrency:       "USD",
		PaymentMethod:      CardBlue,
		PaymentStatus:      PaymentPending,
		Status:             Created,
		CreatedAt:          time.Now().UTC(),
	}
	err := cardRepo.Create(ctx, card)
	require.NoError(t, err)

	// Create transaction
	txID := uuid.New().String()
	toAddr := "tb1qtestaddr"
	tx := &Transaction{
		ID:            txID,
		CardID:        cardID,
		Type:          Fund,
		TxHash:        nil,
		FromAddress:   nil,
		ToAddress:     &toAddr,
		BTCAmountSats: 100000,
		Status:        Pending,
		Confirmations: 0,
		CreatedAt:     time.Now().UTC(),
		BroadcastAt:   nil,
		ConfirmedAt:   nil,
	}
	err = txRepo.Create(ctx, tx)
	require.NoError(t, err)

	// Update: Mark as broadcast
	broadcastTime := time.Now().UTC()
	err = txRepo.Update(ctx, txID, Pending, 0, &broadcastTime, nil)
	require.NoError(t, err)

	// Verify broadcast_at is set
	retrieved, err := txRepo.GetByID(ctx, txID)
	require.NoError(t, err)
	assert.NotNil(t, retrieved.BroadcastAt)
	assert.WithinDuration(t, broadcastTime, *retrieved.BroadcastAt, time.Second)
	assert.Nil(t, retrieved.ConfirmedAt)

	// Update: Mark as confirmed with 6 confirmations
	confirmedTime := time.Now().UTC()
	err = txRepo.Update(ctx, txID, Confirmed, 6, nil, &confirmedTime)
	require.NoError(t, err)

	// Verify both timestamps are preserved and confirmations updated
	retrieved, err = txRepo.GetByID(ctx, txID)
	require.NoError(t, err)
	assert.Equal(t, Confirmed, retrieved.Status)
	assert.Equal(t, 6, retrieved.Confirmations)
	assert.NotNil(t, retrieved.BroadcastAt)                                      // Should be preserved (COALESCE)
	assert.WithinDuration(t, broadcastTime, *retrieved.BroadcastAt, time.Second) // Verify broadcast time preserved
	assert.NotNil(t, retrieved.ConfirmedAt)                                      // Should be set
	assert.WithinDuration(t, confirmedTime, *retrieved.ConfirmedAt, time.Second) // Verify confirmed time set correctly
}

func TestTransactionRepository_Update_NotFound(t *testing.T) {
	db := SetupTestDB(t)
	defer db.Close()
	defer CleanupTestDB(t, db)

	txRepo := NewTransactionRepository(db)
	ctx := context.Background()

	err := txRepo.Update(ctx, uuid.New().String(), Confirmed, 6, nil, nil)
	assert.ErrorIs(t, err, ErrTransactionNotFound)
}

func TestTransactionRepository_MultipleTypes(t *testing.T) {
	db := SetupTestDB(t)
	defer db.Close()
	defer CleanupTestDB(t, db)

	cardRepo := NewCardRepository(db)
	txRepo := NewTransactionRepository(db)
	ctx := context.Background()

	// Create a card
	cardID := uuid.New().String()
	card := &Card{
		ID:                 cardID,
		PurchaseEmail:      "test@example.com",
		OwnerEmail:         "test@example.com",
		Code:               "TYPES-TEST",
		BTCAmountSats:      100000,
		FiatAmountCents:    5000,
		FiatCurrency:       "USD",
		PaymentMethod:      CardBlue,
		PaymentStatus:      PaymentPending,
		Status:             Created,
		CreatedAt:          time.Now().UTC(),
	}
	err := cardRepo.Create(ctx, card)
	require.NoError(t, err)

	// Create transactions of different types
	toAddr := "tb1qtestaddr"
	types := []TransactionType{Fund, Redeem, Payment}
	for _, txType := range types {
		tx := &Transaction{
			ID:            uuid.New().String(),
			CardID:        cardID,
			Type:          txType,
			TxHash:        nil,
			FromAddress:   nil,
			ToAddress:     &toAddr,
			BTCAmountSats: 100000,
			Status:        Pending,
			Confirmations: 0,
			CreatedAt:     time.Now().UTC(),
			BroadcastAt:   nil,
			ConfirmedAt:   nil,
		}
		err := txRepo.Create(ctx, tx)
		require.NoError(t, err)
	}

	// Verify all types were stored correctly
	transactions, err := txRepo.ListByCardID(ctx, cardID)
	require.NoError(t, err)
	assert.Len(t, transactions, 3)

	// Check that each type exists
	foundTypes := make(map[TransactionType]bool)
	for _, tx := range transactions {
		foundTypes[tx.Type] = true
	}
	assert.True(t, foundTypes[Fund])
	assert.True(t, foundTypes[Redeem])
	assert.True(t, foundTypes[Payment])
}

func TestTransactionRepository_MultipleStatuses(t *testing.T) {
	db := SetupTestDB(t)
	defer db.Close()
	defer CleanupTestDB(t, db)

	cardRepo := NewCardRepository(db)
	txRepo := NewTransactionRepository(db)
	ctx := context.Background()

	// Create a card
	cardID := uuid.New().String()
	card := &Card{
		ID:                 cardID,
		PurchaseEmail:      "test@example.com",
		OwnerEmail:         "test@example.com",
		Code:               "STATUS-TEST",
		BTCAmountSats:      100000,
		FiatAmountCents:    5000,
		FiatCurrency:       "USD",
		PaymentMethod:      CardBlue,
		PaymentStatus:      PaymentPending,
		Status:             Created,
		CreatedAt:          time.Now().UTC(),
	}
	err := cardRepo.Create(ctx, card)
	require.NoError(t, err)

	// Create transactions with different statuses
	toAddr := "tb1qtestaddr"
	statuses := []TransactionStatus{Pending, Confirmed, Failed}
	for _, status := range statuses {
		tx := &Transaction{
			ID:            uuid.New().String(),
			CardID:        cardID,
			Type:          Fund,
			TxHash:        nil,
			FromAddress:   nil,
			ToAddress:     &toAddr,
			BTCAmountSats: 100000,
			Status:        status,
			Confirmations: 0,
			CreatedAt:     time.Now().UTC(),
			BroadcastAt:   nil,
			ConfirmedAt:   nil,
		}
		err := txRepo.Create(ctx, tx)
		require.NoError(t, err)
	}

	// Verify all statuses were stored correctly
	transactions, err := txRepo.ListByCardID(ctx, cardID)
	require.NoError(t, err)
	assert.Len(t, transactions, 3)

	// Check that each status exists
	foundStatuses := make(map[TransactionStatus]bool)
	for _, tx := range transactions {
		foundStatuses[tx.Status] = true
	}
	assert.True(t, foundStatuses[Pending])
	assert.True(t, foundStatuses[Confirmed])
	assert.True(t, foundStatuses[Failed])
}
