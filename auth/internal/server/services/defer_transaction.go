package services

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
)

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
