package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/vault"
)

// AddAccountWithSecret stores a new Account with its API token, already
// sealed with the data key, and returns it with its ID set.
func (s *Store) AddAccountWithSecret(ctx context.Context, a dns.Account, sealed []byte) (dns.Account, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO accounts (provider, name, secret) VALUES (?, ?, ?)`, a.Provider, a.Name, sealed)
	if err != nil {
		return dns.Account{}, fmt.Errorf("add account %s: %w", a.Label(), err)
	}

	a.ID, err = res.LastInsertId()
	if err != nil {
		return dns.Account{}, fmt.Errorf("add account %s: %w", a.Label(), err)
	}

	return a, nil
}

// AccountSecret returns an Account's sealed API token; nil when it has none.
func (s *Store) AccountSecret(ctx context.Context, accountID int64) ([]byte, error) {
	var sealed []byte
	if err := s.db.QueryRowContext(ctx, `SELECT secret FROM accounts WHERE id = ?`, accountID).Scan(&sealed); err != nil {
		return nil, fmt.Errorf("read account secret: %w", err)
	}

	return sealed, nil
}

// ErrAlreadyEnrolled means a key slot, and so a data key, already exists.
var ErrAlreadyEnrolled = errors.New("an Unseal Method is already enrolled")

// EnrollFirstKeySlot stores the first key slot, which wraps a new data key,
// and returns it with its ID set. It returns ErrAlreadyEnrolled when any key
// slot exists: the check and the insert hold the write lock together, so
// even two processes enrolling at once leave exactly one data key.
func (s *Store) EnrollFirstKeySlot(ctx context.Context, slot vault.Slot) (vault.Slot, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return vault.Slot{}, fmt.Errorf("enroll key slot: %w", err)
	}
	defer conn.Close()

	err = writeLocked(ctx, conn, func() error {
		var exists bool
		if err := conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM key_slots)`).Scan(&exists); err != nil {
			return err
		}

		if exists {
			return ErrAlreadyEnrolled
		}

		res, err := conn.ExecContext(ctx, `INSERT INTO key_slots (method, params, wrapped) VALUES (?, ?, ?)`, slot.Method, slot.Params, slot.Wrapped)
		if err != nil {
			return err
		}

		slot.ID, err = res.LastInsertId()

		return err
	})
	if err != nil {
		return vault.Slot{}, fmt.Errorf("enroll key slot: %w", err)
	}

	return slot, nil
}

// KeySlots returns every enrolled key slot, oldest first.
func (s *Store) KeySlots(ctx context.Context) ([]vault.Slot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, method, params, wrapped FROM key_slots ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list key slots: %w", err)
	}
	defer rows.Close()

	var out []vault.Slot

	for rows.Next() {
		var slot vault.Slot
		if err := rows.Scan(&slot.ID, &slot.Method, &slot.Params, &slot.Wrapped); err != nil {
			return nil, fmt.Errorf("list key slots: %w", err)
		}

		out = append(out, slot)
	}

	return out, rows.Err()
}
