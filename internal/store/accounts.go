package store

import (
	"context"
	"fmt"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

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
