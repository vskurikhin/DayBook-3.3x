package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

type mockPool struct {
	conn *pgxpool.Conn
	err  error
}

func (m *mockPool) Acquire(_ context.Context) (*pgxpool.Conn, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.conn, nil
}

func (m *mockPool) Close() {
	return
}

func TestBegin_AcquireError(t *testing.T) {
	pool := &PgxPool{
		pgxPool: &mockPool{
			err: errors.New("acquire error"),
		},
	}

	_, err := pool.Begin(context.Background())

	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExec_AcquireError(t *testing.T) {
	pool := &PgxPool{
		pgxPool: &mockPool{
			err: errors.New("acquire error"),
		},
	}

	_, err := pool.Exec(context.Background(), "SELECT 1")

	if err == nil {
		t.Fatal("expected error")
	}
}

func TestQuery_AcquireError(t *testing.T) {
	pool := &PgxPool{
		pgxPool: &mockPool{
			err: errors.New("acquire error"),
		},
	}

	_, err := pool.Query(context.Background(), "SELECT 1")

	if err == nil {
		t.Fatal("expected error")
	}
}

func TestQueryRow_AcquireError(t *testing.T) {
	pool := &PgxPool{
		pgxPool: &mockPool{
			err: errors.New("acquire error"),
		},
	}

	_, err := pool.QueryRow(context.Background(), "SELECT 1")

	if err == nil {
		t.Fatal("expected error")
	}
}
