package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrCardNotFound is returned when a card is not found in the database
	ErrCardNotFound = errors.New("card not found")
	// ErrCardCodeExists is returned when trying to create a card with an existing code
	ErrCardCodeExists = errors.New("card code already exists")
)

// CardRepository handles all database operations for cards
type CardRepository struct {
	db Querier
}

// NewCardRepository creates a new card repository instance
func NewCardRepository(db *DB) *CardRepository {
	return &CardRepository{
		db: db.pool,
	}
}

// Create inserts a new card into the database.
// Returns ErrCardCodeExists if the code already exists.
func (r *CardRepository) Create(ctx context.Context, card *Card) error {
	query := `INSERT INTO cards (
		id,
		user_id, 
		purchase_email,
		owner_email, 
		code, 
		btc_amount_sats,
		fiat_amount_cents,
		fiat_currency,
		purchase_price_cents,
		status,
		created_at,
		funded_at,
		redeemed_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`

	_, err := r.db.Exec(
		ctx,
		query,
		card.ID,
		card.UserID,
		card.PurchaseEmail,
		card.OwnerEmail,
		card.Code,
		card.BTCAmountSats,
		card.FiatAmountCents,
		card.FiatCurrency,
		card.PurchasePriceCents,
		card.Status,
		card.CreatedAt,
		card.FundedAt,
		card.RedeemedAt,
	)

	if err != nil {
		// Check if it's a pgconn.PgError
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "23505" { // unique_violation
				if pgErr.ConstraintName == "cards_code_key" {
					return ErrCardCodeExists
				}
			}
		}
		return fmt.Errorf("failed to create card: %w", err)
	}

	return nil
}

// GetByCode retrieves a card by its redemption code.
// Returns ErrCardNotFound if the code does not exist.
func (r *CardRepository) GetByCode(ctx context.Context, code string) (*Card, error) {
	query := `SELECT 
        id, user_id, purchase_email, owner_email, code,
        btc_amount_sats, fiat_amount_cents, fiat_currency, purchase_price_cents,
        status, created_at, funded_at, redeemed_at
    FROM cards WHERE code = $1`

	var card Card

	err := r.db.QueryRow(ctx, query, code).Scan(
		&card.ID,
		&card.UserID,
		&card.PurchaseEmail,
		&card.OwnerEmail,
		&card.Code,
		&card.BTCAmountSats,
		&card.FiatAmountCents,
		&card.FiatCurrency,
		&card.PurchasePriceCents,
		&card.Status,
		&card.CreatedAt,
		&card.FundedAt,
		&card.RedeemedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCardNotFound
		}
		return nil, fmt.Errorf("failed to get card with code %s: %w", code, err)
	}

	return &card, nil
}

// GetByID retrieves a card by its UUID.
// Returns ErrCardNotFound if the ID does not exist.
func (r *CardRepository) GetByID(ctx context.Context, id string) (*Card, error) {
	query := `SELECT 
        id, user_id, purchase_email, owner_email, code,
        btc_amount_sats, fiat_amount_cents, fiat_currency, purchase_price_cents,
        status, created_at, funded_at, redeemed_at
    FROM cards WHERE id = $1`

	var card Card

	err := r.db.QueryRow(ctx, query, id).Scan(
		&card.ID,
		&card.UserID,
		&card.PurchaseEmail,
		&card.OwnerEmail,
		&card.Code,
		&card.BTCAmountSats,
		&card.FiatAmountCents,
		&card.FiatCurrency,
		&card.PurchasePriceCents,
		&card.Status,
		&card.CreatedAt,
		&card.FundedAt,
		&card.RedeemedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCardNotFound
		}
		return nil, fmt.Errorf("failed to get card with id %s: %w", id, err)
	}

	return &card, nil
}

// Update updates a card's status and related timestamps.
// Uses COALESCE to preserve existing timestamp values when nil is passed.
// Returns ErrCardNotFound if the card ID does not exist.
func (r *CardRepository) Update(ctx context.Context, id string, status CardStatus, BTCAmountSats *int64, fundedAt, redeemedAt *time.Time) error {
	query := `UPDATE cards 
		SET status = $2,
			btc_amount_sats = COALESCE($3, btc_amount_sats),
			funded_at = COALESCE($4, funded_at),
			redeemed_at = COALESCE($5, redeemed_at)
		WHERE id = $1`

	commandTag, err := r.db.Exec(ctx, query, id, status, BTCAmountSats, fundedAt, redeemedAt)
	if err != nil {
		return fmt.Errorf("failed to update card with id %s: %w", id, err)
	}

	if commandTag.RowsAffected() == 0 {
		return ErrCardNotFound
	}

	return nil
}

// ListByUserID retrieves all cards belonging to a user, ordered by creation date (newest first).
// Returns an empty slice if the user has no cards.
func (r *CardRepository) ListByUserID(ctx context.Context, userID string) ([]*Card, error) {
	query := `SELECT 
        id, user_id, purchase_email, owner_email, code,
        btc_amount_sats, fiat_amount_cents, fiat_currency, purchase_price_cents,
        status, created_at, funded_at, redeemed_at
    FROM cards WHERE user_id = $1 ORDER BY created_at DESC`

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get cards for user %s: %w", userID, err)
	}
	defer rows.Close()

	var cards []*Card
	for rows.Next() {
		var card Card

		err := rows.Scan(
			&card.ID,
			&card.UserID,
			&card.PurchaseEmail,
			&card.OwnerEmail,
			&card.Code,
			&card.BTCAmountSats,
			&card.FiatAmountCents,
			&card.FiatCurrency,
			&card.PurchasePriceCents,
			&card.Status,
			&card.CreatedAt,
			&card.FundedAt,
			&card.RedeemedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan card row: %w", err)
		}

		cards = append(cards, &card)
	}

	// Check for any errors that occurred during iteration
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error during row iteration: %w", err)
	}

	return cards, nil
}

// WithTx returns a shallow copy of the repository bound to the given
// transaction. Use this inside DB.RunInTx to make all queries on the copy
// participate in the same transaction.
func (r *CardRepository) WithTx(q Querier) *CardRepository {
	return &CardRepository{db: q}
}

// GetTotalReservedBalance returns the sum of btc_amount_sats for all cards
// with status 'active' or 'funding'. These represent reserved treasury funds.
func (r *CardRepository) GetTotalReservedBalance(ctx context.Context) (int64, error) {
	query := `SELECT COALESCE(SUM(btc_amount_sats), 0) FROM cards WHERE status IN ('active', 'funding')`

	var totalReservedBalance int64
	err := r.db.QueryRow(ctx, query).Scan(&totalReservedBalance)
	if err != nil {
		return 0, fmt.Errorf("failed to get total reserved balance: %w", err)
	}

	return totalReservedBalance, nil
}
