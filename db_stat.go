package pg

import "time"

// PoolStat holds the database pool's statistics.
type PoolStat struct {
	// AcquireCount is the cumulative count of successful acquires from the pool.
	AcquireCount int64 `json:"acquire_count"`
	// AcquireDuration is the total duration of all successful acquires from
	// the pool.
	AcquireDuration time.Duration `json:"acquire_duration"`
	// AcquiredConns is the number of currently acquired connections in the pool.
	AcquiredConns int32 `json:"acquired_conns"`
	// CanceledAcquireCount is the cumulative count of acquires from the pool
	// that were canceled by a context.
	CanceledAcquireCount int64 `json:"canceled_acquire_count"`
	// ConstructingConns is the number of conns with construction in progress in
	// the pool.
	ConstructingConns int32 `json:"constructing_conns"`
	// EmptyAcquireCount is the cumulative count of successful acquires from the pool
	// that waited for a resource to be released or constructed because the pool was
	// empty.
	EmptyAcquireCount int64 `json:"empty_acquire_count"`
	// IdleConns is the number of currently idle conns in the pool.
	IdleConns int32 `json:"idle_conns"`
	// MaxConns is the maximum size of the pool.
	MaxConns int32 `json:"max_conns"`
	// TotalConns is the total number of resources currently in the pool.
	// The value is the sum of ConstructingConns, AcquiredConns, and
	// IdleConns.
	TotalConns int32 `json:"total_conns"`
}

// PoolStat returns a snapshot of the database pool statistics.
// The returned structure can be represented through JSON.
func (db *DB) PoolStat() PoolStat {
	stats := db.Pool.Stat()
	return PoolStat{
		AcquireCount:         stats.AcquireCount(),
		AcquireDuration:      stats.AcquireDuration(),
		AcquiredConns:        stats.AcquiredConns(),
		CanceledAcquireCount: stats.CanceledAcquireCount(),
		ConstructingConns:    stats.ConstructingConns(),
		EmptyAcquireCount:    stats.EmptyAcquireCount(),
		IdleConns:            stats.IdleConns(),
		MaxConns:             stats.MaxConns(),
		TotalConns:           stats.TotalConns(),
	}
}
