package store

import (
	"context"
	"fmt"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// AddAccount stores a new Account and returns it with its ID set.
func (s *Store) AddAccount(ctx context.Context, a dns.Account) (dns.Account, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO accounts (provider, name) VALUES (?, ?)`, a.Provider, a.Name)
	if err != nil {
		return dns.Account{}, fmt.Errorf("add account %s: %w", a.Label(), err)
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
