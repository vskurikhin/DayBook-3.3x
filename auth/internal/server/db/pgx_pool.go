package db

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Pool interface {
	Acquire(ctx context.Context) (c *pgxpool.Conn, err error)
	Close()
	Ping(ctx context.Context) error
	Stat() *pgxpool.Stat
}

type PgxPool struct {
	pgxPool Pool
	mu      sync.RWMutex
}

// Begin starts a new database transaction using a connection acquired
// from the underlying pgx connection pool.
//
// The method acquires a connection from the pool and calls Begin on it.
// If acquiring the connection fails, an error is returned.
func (p *PgxPool) Begin(ctx context.Context) (pgx.Tx, error) {
	p.mu.RLock()
	conn, err := p.pgxPool.Acquire(ctx)
	p.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return conn.Begin(ctx)
}

// Exec executes the given SQL statement using a connection acquired
// from the underlying pgx connection pool.
//
// It returns a CommandTag containing execution metadata (such as the
// number of affected rows). If acquiring a connection or executing the
// statement fails, an error is returned.
func (p *PgxPool) Exec(ctx context.Context, sql string, arguments ...interface{}) (pgconn.CommandTag, error) {
	p.mu.RLock()
	conn, err := p.pgxPool.Acquire(ctx)
	p.mu.RUnlock()
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	return conn.Exec(ctx, sql, arguments...)
}

// Query executes a SQL query using a connection acquired from the
// underlying pgx connection pool and returns the resulting rows.
//
// The caller is responsible for closing the returned pgx.Rows.
// An error is returned if the connection cannot be acquired or
// if the query execution fails.
func (p *PgxPool) Query(ctx context.Context, sql string, optionsAndArgs ...interface{}) (pgx.Rows, error) {
	p.mu.RLock()
	conn, err := p.pgxPool.Acquire(ctx)
	p.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return conn.Query(ctx, sql, optionsAndArgs...)
}

// QueryRow executes a SQL query expected to return at most one row
// using a connection acquired from the underlying pgx connection pool.
//
// It returns a pgx.Row that can be scanned by the caller.
// An error is returned if acquiring the connection from the pool fails.
func (p *PgxPool) QueryRow(ctx context.Context, sql string, optionsAndArgs ...interface{}) (pgx.Row, error) {
	p.mu.RLock()
	conn, err := p.pgxPool.Acquire(ctx)
	p.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return conn.QueryRow(ctx, sql, optionsAndArgs...), nil
}

func IsNotEqual(a *pgxpool.Pool, p *PgxPool) bool {
	if a == nil || p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	b, ok := p.pgxPool.(*pgxpool.Pool)
	if !ok {
		return false
	}
	if a != b {
		return true
	}
	return false
}
