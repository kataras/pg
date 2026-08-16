package pg

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/tracelog"
)

// TestOpenErrorHidesPassword is the regression test for the secret-leak fix in Open: the old
// code embedded the full, unparsed connection string (including the plaintext password)
// straight into the error returned when pgxpool.NewWithConfig failed
// (fmt.Errorf("open: %w: full connection string: <%s>", err, connString)). It covers both
// error sites inside Open that can fail for a bad connection string/target.
func TestOpenErrorHidesPassword(t *testing.T) {
	const passwordSentinel = "sswrd_sentinel"

	t.Run("unroutable host", func(t *testing.T) {
		// port 1 on the loopback address refuses the connection immediately, so this fails
		// fast without needing a live database. This mainly exercises pool.Ping's error path,
		// which never embedded the connection string; it is kept as a belt-and-suspenders
		// check on top of the "pool construction failure" case below, which does exercise the
		// exact line that used to leak.
		connString := "host=127.0.0.1 port=1 user=u password=" + passwordSentinel + " dbname=x sslmode=disable connect_timeout=1"

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := Open(ctx, NewSchema(), connString)
		if err == nil {
			t.Fatal("expected Open to fail against an unroutable host, got nil error")
		}

		if strings.Contains(err.Error(), passwordSentinel) {
			t.Fatalf("error must not contain the connection password, got: %v", err)
		}
	})

	t.Run("pool construction failure", func(t *testing.T) {
		// Forces pgxpool.NewWithConfig itself to fail (MaxConns must be >= 1), which is
		// precisely the call whose error used to have the raw connString appended to it.
		// No network access happens at all, so this is fast and deterministic.
		connString := "host=example.invalid port=5432 user=u password=" + passwordSentinel + " dbname=x sslmode=disable"

		zeroMaxConns := func(cfg *pgxpool.Config) error {
			cfg.MaxConns = 0
			return nil
		}

		_, err := Open(context.Background(), NewSchema(), connString, zeroMaxConns)
		if err == nil {
			t.Fatal("expected Open to fail when MaxConns is 0, got nil error")
		}

		if strings.Contains(err.Error(), passwordSentinel) {
			t.Fatalf("error must not contain the connection password, got: %v", err)
		}
	})
}

// TestWithLoggerLevel verifies that WithLoggerLevel installs a *tracelog.TraceLog tracer
// configured with the caller-chosen tracelog.LogLevel, unlike WithLogger which always hardcodes
// tracelog.LogLevelTrace.
func TestWithLoggerLevel(t *testing.T) {
	config, err := pgxpool.ParseConfig("postgres://user:pass@localhost:5432/db")
	if err != nil {
		t.Fatal(err)
	}

	logger := tracelog.LoggerFunc(func(context.Context, tracelog.LogLevel, string, map[string]any) {})

	const wantLevel = tracelog.LogLevelWarn

	opt := WithLoggerLevel(logger, wantLevel)
	if err = opt(config); err != nil {
		t.Fatal(err)
	}

	tracer, ok := config.ConnConfig.Tracer.(*tracelog.TraceLog)
	if !ok {
		t.Fatalf("expected config.ConnConfig.Tracer to be a *tracelog.TraceLog, got: %T", config.ConnConfig.Tracer)
	}

	if tracer.LogLevel != wantLevel {
		t.Fatalf("expected LogLevel: %s, got: %s", wantLevel, tracer.LogLevel)
	}
}
