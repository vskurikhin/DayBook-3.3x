package services

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

//go:generate mockgen -destination=tx_mock_test.go -package=services github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services Tx
type Tx interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
	CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error)
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
	LargeObjects() pgx.LargeObjects
	Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error)
	Exec(ctx context.Context, sql string, arguments ...any) (commandTag pgconn.CommandTag, err error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Conn() *pgx.Conn
}

func deferTransaction(ctx context.Context, tx pgx.Tx, err error) {
	if tx == nil {
		slog.ErrorContext(ctx, "transaction cannot be nil")
		return
	}
	if err != nil {
		slog.ErrorContext(ctx,
			"rollback failed transaction",
			slog.String("error", err.Error()),
			slog.String("errorType", fmt.Sprintf("%T", err)),
		)
		err = tx.Rollback(ctx)
		if err != nil {
			slog.ErrorContext(ctx,
				"rollback error",
				slog.String("error", err.Error()),
				slog.String("errorType", fmt.Sprintf("%T", err)),
			)
		}
		return
	}
	err = tx.Commit(ctx)
	if err != nil {
		slog.ErrorContext(ctx,
			"commit error",
			slog.String("error", err.Error()),
			slog.String("errorType", fmt.Sprintf("%T", err)),
		)
	}
}
