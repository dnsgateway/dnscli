package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "dnscli: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	dbPath := flag.String("db", defaultDBPath(), "path to the dnscli database (not a disposable cache: it holds your Accounts)")
	flag.Parse()

	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o700); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}

	ctx := context.Background()

	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	// The Provider registry, resolver and Unseal Methods are wired here as they land.
	model := tui.New(ctx, tui.Deps{
		Store: s,
		Clock: time.Now,
	})

	_, err = tea.NewProgram(model, tea.WithAltScreen()).Run()

	return err
}

func defaultDBPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}

	return filepath.Join(dir, "dnscli", "dnscli.db")
}
