package pg

import "context"

// Ping verifies the database connection pool is alive, acquiring a connection if
// necessary. It is intended for readiness/liveness handlers.
//
// Ping always goes through db.Pool, even when db is transaction-scoped (db.IsTransaction
// reports true): pgx.Tx has no Ping method of its own, and a liveness/readiness check is
// about whether the database is reachable at all, not about the specific connection a given
// transaction happens to be pinned to. Calling Ping on a transaction-scoped *DB therefore does
// not touch, and cannot invalidate, that transaction.
func (db *DB) Ping(ctx context.Context) error {
	return db.Pool.Ping(ctx)
}

// Health describes a point-in-time health snapshot of the database connection.
type Health struct {
	// ServerVersion is the PostgreSQL server version number, as returned by DB.GetVersion.
	ServerVersion string `json:"server_version"`
	// Pool is a snapshot of the connection pool statistics, as returned by DB.PoolStat.
	Pool PoolStat `json:"pool"`
}

// Health pings the database and returns its server version together with pool
// statistics; it fails when the database is unreachable.
//
// Like Ping, Health always checks db.Pool, even when db.IsTransaction reports true: pgx.Tx has
// no Ping method of its own, and a liveness/readiness check is about whether the database is
// reachable at all, not about the specific connection a given transaction happens to be pinned
// to, so this never touches or invalidates an in-flight transaction.
func (db *DB) Health(ctx context.Context) (Health, error) {
	if err := db.Ping(ctx); err != nil {
		return Health{}, err
	}

	version, err := db.GetVersion(ctx)
	if err != nil {
		return Health{}, err
	}

	return Health{
		ServerVersion: version,
		Pool:          db.PoolStat(),
	}, nil
}
