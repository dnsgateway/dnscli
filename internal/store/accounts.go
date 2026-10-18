// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package store

import (
	"context"
	"errors"
	"fmt"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// ErrAccountExists means an Account with the same label is already stored.
var ErrAccountExists = errors.New("the Account already exists")

// ErrAccountNotFound means the Account is not stored, for example because it
// was removed while background work for it was running.
var ErrAccountNotFound = errors.New("the Account no longer exists")

// addAccountError wraps an error from inserting an Account, reporting a
// duplicate label as ErrAccountExists.
func addAccountError(a dns.Account, err error) error {
	return fmt.Errorf("add account %s: %w", a.Label(), uniqueError(err))
}

// uniqueError reports a duplicate Account label as ErrAccountExists.
func uniqueError(err error) error {
	var se *sqlite.Error
	if errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return ErrAccountExists
	}

	return err
}

// AddAccount stores a new Account and returns it with its ID set.
func (s *Store) AddAccount(ctx context.Context, a dns.Account) (dns.Account, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO accounts (provider, name) VALUES (?, ?)`, a.Provider, a.Name)
	if err != nil {
		return dns.Account{}, addAccountError(a, err)
	}

	a.ID, err = res.LastInsertId()
	if err != nil {
		return dns.Account{}, fmt.Errorf("add account %s: %w", a.Label(), err)
	}

	return a, nil
}

// Accounts returns every Account, ordered by label.
func (s *Store) Accounts(ctx context.Context) ([]dns.Account, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, provider, name FROM accounts ORDER BY provider, name`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()

	var out []dns.Account

	for rows.Next() {
		var a dns.Account
		if err := rows.Scan(&a.ID, &a.Provider, &a.Name); err != nil {
			return nil, fmt.Errorf("list accounts: %w", err)
		}

		out = append(out, a)
	}

	return out, rows.Err()
}

// RemoveAccount deletes an Account with its sealed secret; the schema
// cascades the delete to its cached Zones and their Records, in the same
// statement. The key slots stay: the data key remains enrolled even when
// the last Account is removed. Removing an Account that is already gone is
// not an error.
func (s *Store) RemoveAccount(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("remove account: %w", err)
	}

	return nil
}
