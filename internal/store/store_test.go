package store_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/store"
)

func open(t *testing.T, path string) *store.Store {
	t.Helper()

	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() { _ = s.Close() })

	return s
}

func TestOpenUsesThePathVerbatim(t *testing.T) {
	for _, name := range []string{"a#b.db", "a?b.db", "a%41b.db", "a b.db"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)

			s := open(t, path)
			if _, err := s.AddAccount(context.Background(), dns.Account{Provider: "cloudflare", Name: "work"}); err != nil {
				t.Fatalf("add account: %v", err)
			}

			if err := s.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}

			if _, err := os.Stat(path); err != nil {
				t.Fatalf("database is not at %q: %v", path, err)
			}

			accounts, err := open(t, path).Accounts(context.Background())
			if err != nil {
				t.Fatalf("accounts: %v", err)
			}

			if len(accounts) != 1 || accounts[0].Label() != "cloudflare.work" {
				t.Fatalf("reopened database has accounts %+v", accounts)
			}
		})
	}
}

func TestConcurrentOpensOfAFreshDatabaseAllSucceed(t *testing.T) {
	for range 20 {
		path := filepath.Join(t.TempDir(), "dnscli.db")

		var wg sync.WaitGroup

		errs := make([]error, 4)
		for i := range errs {
			wg.Add(1)

			go func() {
				defer wg.Done()

				s, err := store.Open(context.Background(), path)
				if err == nil {
					_, err = s.Accounts(context.Background())
					_ = s.Close()
				}

				errs[i] = err
			}()
		}

		wg.Wait()

		for _, err := range errs {
			if err != nil {
				t.Fatalf("concurrent open: %v", err)
			}
		}
	}
}

func TestOpenAcceptsARelativePath(t *testing.T) {
	t.Chdir(t.TempDir())

	if err := os.Mkdir("dnscli", 0o700); err != nil {
		t.Fatal(err)
	}

	open(t, filepath.Join("dnscli", "a#b.db"))

	if _, err := os.Stat(filepath.Join("dnscli", "a#b.db")); err != nil {
		t.Fatalf("database is not at the relative path: %v", err)
	}
}
