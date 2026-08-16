package pg

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/multitracer"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/tracelog"
)

// fakeQueryTracer is a minimal pgx.QueryTracer implementation used to verify tracer
// composition without depending on a live database or a real tracing backend.
type fakeQueryTracer struct{ name string }

func (f *fakeQueryTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return ctx
}

func (f *fakeQueryTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
}

// discardLogger is a no-op tracelog.Logger, used only so WithLoggerLevel has something to install.
type discardLogger struct{}

func (discardLogger) Log(ctx context.Context, level tracelog.LogLevel, msg string, data map[string]any) {
}

func parseTestPoolConfig(t *testing.T) *pgxpool.Config {
	t.Helper()

	config, err := pgxpool.ParseConfig("postgres://user:pass@localhost:5432/db")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	return config
}

// TestWithQueryTracerAlone verifies that a single WithQueryTracer call, with no tracer already
// installed, sets the tracer slot directly (not wrapped in a *multitracer.Tracer).
func TestWithQueryTracerAlone(t *testing.T) {
	config := parseTestPoolConfig(t)

	tracer := &fakeQueryTracer{name: "solo"}
	if err := WithQueryTracer(tracer)(config); err != nil {
		t.Fatalf("WithQueryTracer: %v", err)
	}

	got, ok := config.ConnConfig.Tracer.(*fakeQueryTracer)
	if !ok {
		t.Fatalf("expected ConnConfig.Tracer to be the *fakeQueryTracer directly, got %T", config.ConnConfig.Tracer)
	}
	if got != tracer {
		t.Fatalf("expected the installed tracer to be the same instance passed in, got a different pointer")
	}
}

// TestWithQueryTracerComposesWithExistingTracer verifies that WithLoggerLevel followed by
// WithQueryTracer composes the two into a *multitracer.Tracer containing both, with the
// pre-existing (logger) tracer first, per WithQueryTracer's documented ordering caveat.
func TestWithQueryTracerComposesWithExistingTracer(t *testing.T) {
	config := parseTestPoolConfig(t)

	if err := WithLoggerLevel(discardLogger{}, tracelog.LogLevelWarn)(config); err != nil {
		t.Fatalf("WithLoggerLevel: %v", err)
	}

	logTracer, ok := config.ConnConfig.Tracer.(*tracelog.TraceLog)
	if !ok {
		t.Fatalf("expected WithLoggerLevel to install a *tracelog.TraceLog, got %T", config.ConnConfig.Tracer)
	}

	extra := &fakeQueryTracer{name: "extra"}
	if err := WithQueryTracer(extra)(config); err != nil {
		t.Fatalf("WithQueryTracer: %v", err)
	}

	multi, ok := config.ConnConfig.Tracer.(*multitracer.Tracer)
	if !ok {
		t.Fatalf("expected ConnConfig.Tracer to be a *multitracer.Tracer after composing, got %T", config.ConnConfig.Tracer)
	}

	if len(multi.QueryTracers) != 2 {
		t.Fatalf("expected 2 composed query tracers, got %d: %#v", len(multi.QueryTracers), multi.QueryTracers)
	}
	if multi.QueryTracers[0] != pgx.QueryTracer(logTracer) {
		t.Fatalf("expected the pre-existing logger tracer to be first (WithLogger-before-WithQueryTracer ordering), got %#v", multi.QueryTracers[0])
	}
	if multi.QueryTracers[1] != pgx.QueryTracer(extra) {
		t.Fatalf("expected the tracer passed to WithQueryTracer to be second, got %#v", multi.QueryTracers[1])
	}
}

// TestWithQueryTracerMultipleNoExisting verifies that passing multiple tracers to
// WithQueryTracer with no tracer already installed still composes them via multitracer.
func TestWithQueryTracerMultipleNoExisting(t *testing.T) {
	config := parseTestPoolConfig(t)

	first := &fakeQueryTracer{name: "first"}
	second := &fakeQueryTracer{name: "second"}
	if err := WithQueryTracer(first, second)(config); err != nil {
		t.Fatalf("WithQueryTracer: %v", err)
	}

	multi, ok := config.ConnConfig.Tracer.(*multitracer.Tracer)
	if !ok {
		t.Fatalf("expected ConnConfig.Tracer to be a *multitracer.Tracer, got %T", config.ConnConfig.Tracer)
	}
	if len(multi.QueryTracers) != 2 || multi.QueryTracers[0] != pgx.QueryTracer(first) || multi.QueryTracers[1] != pgx.QueryTracer(second) {
		t.Fatalf("unexpected composed tracers: %#v", multi.QueryTracers)
	}
}

// TestWithDefaultQueryExecMode verifies that WithDefaultQueryExecMode sets
// ConnConfig.DefaultQueryExecMode, e.g. for PgBouncer transaction-pooling compatibility.
func TestWithDefaultQueryExecMode(t *testing.T) {
	config := parseTestPoolConfig(t)

	if err := WithDefaultQueryExecMode(pgx.QueryExecModeSimpleProtocol)(config); err != nil {
		t.Fatalf("WithDefaultQueryExecMode: %v", err)
	}

	if config.ConnConfig.DefaultQueryExecMode != pgx.QueryExecModeSimpleProtocol {
		t.Fatalf("expected DefaultQueryExecMode=%v, got %v", pgx.QueryExecModeSimpleProtocol, config.ConnConfig.DefaultQueryExecMode)
	}
}

// TestWithStatementCacheCapacity verifies that WithStatementCacheCapacity sets
// ConnConfig.StatementCacheCapacity, including the documented 0-disables-it case.
func TestWithStatementCacheCapacity(t *testing.T) {
	config := parseTestPoolConfig(t)

	if err := WithStatementCacheCapacity(42)(config); err != nil {
		t.Fatalf("WithStatementCacheCapacity: %v", err)
	}
	if config.ConnConfig.StatementCacheCapacity != 42 {
		t.Fatalf("expected StatementCacheCapacity=42, got %d", config.ConnConfig.StatementCacheCapacity)
	}

	if err := WithStatementCacheCapacity(0)(config); err != nil {
		t.Fatalf("WithStatementCacheCapacity(0): %v", err)
	}
	if config.ConnConfig.StatementCacheCapacity != 0 {
		t.Fatalf("expected StatementCacheCapacity=0, got %d", config.ConnConfig.StatementCacheCapacity)
	}
}

// TestWithDescriptionCacheCapacity verifies that WithDescriptionCacheCapacity sets
// ConnConfig.DescriptionCacheCapacity.
func TestWithDescriptionCacheCapacity(t *testing.T) {
	config := parseTestPoolConfig(t)

	if err := WithDescriptionCacheCapacity(7)(config); err != nil {
		t.Fatalf("WithDescriptionCacheCapacity: %v", err)
	}
	if config.ConnConfig.DescriptionCacheCapacity != 7 {
		t.Fatalf("expected DescriptionCacheCapacity=7, got %d", config.ConnConfig.DescriptionCacheCapacity)
	}
}
