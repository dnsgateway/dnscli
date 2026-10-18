// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/vault"
)

// AddAccountWithSecret stores a new Account with its credentials, already
// sealed with the data key, and returns it with its ID set.
func (s *Store) AddAccountWithSecret(ctx context.Context, a dns.Account, sealed []byte) (dns.Account, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO accounts (provider, name, secret) VALUES (?, ?, ?)`, a.Provider, a.Name, sealed)
	if err != nil {
		return dns.Account{}, addAccountError(a, err)
	}

	a.ID, err = res.LastInsertId()
	if err != nil {
		return dns.Account{}, fmt.Errorf("add account %s: %w", a.Label(), err)
	}

	return a, nil
}

// UpdateAccountWithSecret renames a stored Account and replaces its
// credentials, already sealed with the data key for its new label: the
// sealed credentials are bound to the label, so the two change together;
// nil keeps an Account without credentials without them. It returns
// ErrAccountExists when another Account has the new label, and
// ErrAccountNotFound when the Account is not stored.
func (s *Store) UpdateAccountWithSecret(ctx context.Context, a dns.Account, sealed []byte) error {
	res, err := s.db.ExecContext(ctx, `UPDATE accounts SET name = ?, secret = ? WHERE id = ?`, a.Name, sealed, a.ID)
	if err != nil {
		return fmt.Errorf("update account %s: %w", a.Label(), uniqueError(err))
	}

	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err == nil {
			err = ErrAccountNotFound
		}

		return fmt.Errorf("update account %s: %w", a.Label(), err)
	}

	return nil
}

// ReplaceAccountSecret replaces an Account's sealed credentials with
// sealed, for its label as it is, provided they are still old, as when
// they were read. It reports whether they were: credentials saved
// meanwhile, as by an edit or a rename, are kept.
func (s *Store) ReplaceAccountSecret(ctx context.Context, accountID int64, old, sealed []byte) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE accounts SET secret = ? WHERE id = ? AND secret = ?`, sealed, accountID, old)
	if err != nil {
		return false, fmt.Errorf("replace account secret: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("replace account secret: %w", err)
	}

	return n > 0, nil
}

// AccountSecret returns an Account's sealed credentials; nil when it has none.
// It returns ErrAccountNotFound when the Account is not stored.
func (s *Store) AccountSecret(ctx context.Context, accountID int64) ([]byte, error) {
	var sealed []byte
	err := s.db.QueryRowContext(ctx, `SELECT secret FROM accounts WHERE id = ?`, accountID).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrAccountNotFound
	}

	if err != nil {
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
	slot, _, err := s.enroll(ctx, slot, true)

	return slot, err
}

// ErrNotEnrolled means no key slot, and so no data key, exists yet.
var ErrNotEnrolled = errors.New("no Unseal Method is enrolled yet")

// EnrollKeySlot stores another key slot, which must wrap the existing data
// key, and returns it with its ID set. It replaces the earlier key slots of
// the same Unseal Method, so a method can be enrolled again, such as after
// its Keychain item was lost, and returns those replaced slots so the
// caller can clean up what they referenced. It returns ErrNotEnrolled when
// no key slot exists: the first one goes through EnrollFirstKeySlot.
func (s *Store) EnrollKeySlot(ctx context.Context, slot vault.Slot) (vault.Slot, []vault.Slot, error) {
	return s.enroll(ctx, slot, false)
}

// enroll stores a key slot, checking under the write lock that key slots
// exist (or, for the first one, that none does), and replaces the earlier
// slots of the same Unseal Method, which it returns.
func (s *Store) enroll(ctx context.Context, slot vault.Slot, first bool) (vault.Slot, []vault.Slot, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return vault.Slot{}, nil, fmt.Errorf("enroll key slot: %w", err)
	}
	defer conn.Close()

	var replaced []vault.Slot

	err = writeLocked(ctx, conn, func() error {
		var exists bool
		if err := conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM key_slots)`).Scan(&exists); err != nil {
			return err
		}

		switch {
		case first && exists:
			return ErrAlreadyEnrolled
		case !first && !exists:
			return ErrNotEnrolled
		}

		rows, err := conn.QueryContext(ctx, `DELETE FROM key_slots WHERE method = ? RETURNING id, method, params, wrapped`, slot.Method)
		if err != nil {
			return err
		}

		if replaced, err = scanKeySlots(rows); err != nil {
			return err
		}

		res, err := conn.ExecContext(ctx, `INSERT INTO key_slots (method, params, wrapped) VALUES (?, ?, ?)`, slot.Method, slot.Params, slot.Wrapped)
		if err != nil {
			return err
		}

		slot.ID, err = res.LastInsertId()

		return err
	})
	if err != nil {
		return vault.Slot{}, nil, fmt.Errorf("enroll key slot: %w", err)
	}

	return slot, replaced, nil
}

// KeySlots returns every enrolled key slot, oldest first.
func (s *Store) KeySlots(ctx context.Context) ([]vault.Slot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, method, params, wrapped FROM key_slots ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list key slots: %w", err)
	}

	out, err := scanKeySlots(rows)
	if err != nil {
		return nil, fmt.Errorf("list key slots: %w", err)
	}

	return out, nil
}

// scanKeySlots reads and closes rows of id, method, params, wrapped.
func scanKeySlots(rows *sql.Rows) ([]vault.Slot, error) {
	defer rows.Close()

	var out []vault.Slot

	for rows.Next() {
		var slot vault.Slot
		if err := rows.Scan(&slot.ID, &slot.Method, &slot.Params, &slot.Wrapped); err != nil {
			return nil, err
		}

		out = append(out, slot)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return out, rows.Close()
}
