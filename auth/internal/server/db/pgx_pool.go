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
}

type PgxPool struct {
	pgxPool Pool
	mu      sync.Mutex
}

func (p *PgxPool) Begin(ctx context.Context) (pgx.Tx, error) {
	conn, err := p.pgxPool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return conn.Begin(ctx)
}

func (p *PgxPool) Exec(ctx context.Context, sql string, arguments ...interface{}) (pgconn.CommandTag, error) {
	conn, err := p.pgxPool.Acquire(ctx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	return conn.Exec(ctx, sql, arguments...)
}

func (p *PgxPool) Query(ctx context.Context, sql string, optionsAndArgs ...interface{}) (pgx.Rows, error) {
	conn, err := p.pgxPool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return conn.Query(ctx, sql, optionsAndArgs...)
}

func (p *PgxPool) QueryRow(ctx context.Context, sql string, optionsAndArgs ...interface{}) (pgx.Row, error) {
	conn, err := p.pgxPool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return conn.QueryRow(ctx, sql, optionsAndArgs...), nil
}
