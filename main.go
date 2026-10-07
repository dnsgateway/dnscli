package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/pflag"

	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
	"github.com/sergelogvinov/dnscli/internal/vault"
)

// unsealEnv chooses the session's Unseal Method when --unseal is not given.
const unsealEnv = "DNSCLI_UNSEAL"

// providers maps each Provider type this build supports to its constructor.
// Cloudflare lands with its adapter; a build with the fakeprovider tag adds
// an in-memory fake for trying the UI.
var providers = provider.Registry{}

// unsealMethods are the Unseal Methods this build supports, by name.
var unsealMethods = map[string]vault.Method{
	vault.Passphrase{}.Name(): vault.Passphrase{},
}

func main() {
	os.Exit(runMain(os.Args[1:], os.Getenv, runtime.GOOS, os.Stderr))
}

// runMain runs dnscli and returns its exit status: 2 for an invalid
// command line, which also prints the usage, and 1 for any other error.
func runMain(args []string, getenv func(string) string, goos string, stderr io.Writer) int {
	opts, err := parseOptions(args, getenv, goos, stderr)

	switch {
	case errors.Is(err, pflag.ErrHelp):
		return 0
	case err != nil:
		return 2
	}

	if err := run(opts); err != nil {
		fmt.Fprintf(stderr, "dnscli: %v\n", err)

		return 1
	}

	return 0
}

type options struct {
	dbPath string
	unseal vault.Method
}

// parseOptions reads the command line and the environment. When they are
// invalid, it prints the error and the usage to stderr.
func parseOptions(args []string, getenv func(string) string, goos string, stderr io.Writer) (options, error) {
	fs := pflag.NewFlagSet("dnscli", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: dnscli [flags]\n\nFlags:\n%s", fs.FlagUsages())
	}

	dbPath := fs.String("db", defaultDBPath(), "path to the dnscli database (not a disposable cache: it holds your Accounts)")
	unseal := fs.String("unseal", "", fmt.Sprintf("Unseal Method for this session: %s (default $%s, else %s)",
		strings.Join(methodNames(), ", "), unsealEnv, defaultUnsealMethod(goos)))

	opts, err := func() (options, error) {
		if err := fs.Parse(args); err != nil {
			return options{}, err
		}

		if fs.NArg() > 0 {
			return options{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
		}

		name := *unseal
		if name == "" {
			name = getenv(unsealEnv)
		}

		if name == "" {
			name = defaultUnsealMethod(goos)
		}

		m, ok := unsealMethods[name]
		if !ok {
			return options{}, fmt.Errorf("unknown Unseal Method %q (available: %s)", name, strings.Join(methodNames(), ", "))
		}

		return options{dbPath: *dbPath, unseal: m}, nil
	}()

	// pflag has already printed the usage for --help.
	if err != nil && !errors.Is(err, pflag.ErrHelp) {
		fmt.Fprintf(stderr, "dnscli: %v\n", err)
		fs.Usage()
	}

	return opts, err
}

// defaultUnsealMethod is the macOS Keychain on macOS, once this build has
// it, and the passphrase everywhere else.
func defaultUnsealMethod(goos string) string {
	if _, ok := unsealMethods["keychain"]; ok && goos == "darwin" {
		return "keychain"
	}

	return vault.Passphrase{}.Name()
}

func methodNames() []string {
	names := make([]string, 0, len(unsealMethods))
	for n := range unsealMethods {
		names = append(names, n)
	}

	slices.Sort(names)

	return names
}

func run(opts options) error {
	if err := os.MkdirAll(filepath.Dir(opts.dbPath), 0o700); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}

	// Quitting cancels the model's own context; this also covers abnormal exits.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := store.Open(ctx, opts.dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	// The resolver is wired here as it lands.
	model := tui.New(ctx, tui.Deps{
		Store:        s,
		Clock:        time.Now,
		Providers:    providers,
		UnsealMethod: opts.unseal,
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
