package pg

import (
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/multitracer"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WithQueryTracer is a ConnectionOption. It appends one or more pgx.QueryTracer implementations
// (e.g. an OpenTelemetry tracer such as otelpgx, or custom metrics) to the pool configuration,
// composing with any tracer already installed on it via pgx's vendored multitracer package -
// this lets callers plug in their own tracing without this library depending on it.
//
// If poolConfig.ConnConfig.Tracer is already set (e.g. by an earlier ConnectionOption in the
// same Open call), it is prepended to tracers and the combined set is installed as a single
// *multitracer.Tracer; passing WithQueryTracer with no existing tracer installs tracers[0]
// directly (or, for len(tracers) > 1, a *multitracer.Tracer over all of them).
//
// Ordering caveat: WithLogger and WithLoggerLevel both overwrite the tracer slot outright rather
// than composing with it, so when combining query tracing with logging, pass WithLogger (or
// WithLoggerLevel) BEFORE WithQueryTracer in the Open call's option list. The reverse order
// silently drops the tracer(s) installed by WithQueryTracer.
//
// Calling WithQueryTracer with no tracers is a no-op that leaves any already-installed tracer
// untouched. It never clears poolConfig.ConnConfig.Tracer.
func WithQueryTracer(tracers ...pgx.QueryTracer) ConnectionOption {
	return func(poolConfig *pgxpool.Config) error {
		if len(tracers) == 0 {
			return nil
		}

		if existing := poolConfig.ConnConfig.Tracer; existing != nil {
			tracers = append([]pgx.QueryTracer{existing}, tracers...)
		}

		if len(tracers) == 1 {
			poolConfig.ConnConfig.Tracer = tracers[0]
			return nil
		}

		poolConfig.ConnConfig.Tracer = multitracer.New(tracers...)
		return nil
	}
}

// WithDefaultQueryExecMode is a ConnectionOption. It sets pgx's query execution mode, e.g.
// pgx.QueryExecModeSimpleProtocol for PgBouncer transaction-pooling deployments where prepared
// statements cannot be reused across pooled connections. Equivalent to setting
// default_query_exec_mode in the connection string passed to Open.
func WithDefaultQueryExecMode(mode pgx.QueryExecMode) ConnectionOption {
	return func(poolConfig *pgxpool.Config) error {
		poolConfig.ConnConfig.DefaultQueryExecMode = mode
		return nil
	}
}

// WithStatementCacheCapacity is a ConnectionOption. It sets the automatic prepared-statement
// cache size (0 disables it). Equivalent to setting statement_cache_capacity in the connection
// string passed to Open. Like WithDefaultQueryExecMode, disabling this cache (or lowering its
// exec mode) is typically required for PgBouncer transaction-pooling compatibility.
func WithStatementCacheCapacity(n int) ConnectionOption {
	return func(poolConfig *pgxpool.Config) error {
		poolConfig.ConnConfig.StatementCacheCapacity = n
		return nil
	}
}

// WithDescriptionCacheCapacity is a ConnectionOption. It sets the statement-description cache
// size used by pgx's describe exec mode. Equivalent to setting description_cache_capacity in
// the connection string passed to Open.
func WithDescriptionCacheCapacity(n int) ConnectionOption {
	return func(poolConfig *pgxpool.Config) error {
		poolConfig.ConnConfig.DescriptionCacheCapacity = n
		return nil
	}
}
