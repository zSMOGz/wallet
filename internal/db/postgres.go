package db

import (
	"context"
	"database/sql"
	"time"

	_ "github.com/lib/pq"

	handler "wallet/internal/handler"
)

type DBAdapter struct {
	*sql.DB
}

func NewPostgresConnection(connStr string) (*DBAdapter, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(100)
	db.SetMaxIdleConns(50)
	db.SetConnMaxLifetime(time.Minute * 3)
	db.SetConnMaxIdleTime(time.Minute * 1)

	if err = db.Ping(); err != nil {
		return nil, err
	}

	return &DBAdapter{db}, nil
}

type TxAdapter struct {
	*sql.Tx
}

func (tx *TxAdapter) ExecContext(ctx context.Context, query string, args ...interface{}) (handler.ResultInterface, error) {
	return tx.Tx.ExecContext(ctx, query, args...)
}

func (d *DBAdapter) BeginTx(ctx context.Context) (handler.TxInterface, error) {
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &TxAdapter{tx}, nil
}

type RowWrapper struct {
	*sql.Row
}

func (a *DBAdapter) QueryRowContext(ctx context.Context, query string, args ...interface{}) handler.RowScanner {
	return &RowWrapper{a.DB.QueryRowContext(ctx, query, args...)}
}

func (tx *TxAdapter) QueryRowContext(ctx context.Context, query string, args ...interface{}) handler.RowInterface {
	return &RowWrapper{tx.Tx.QueryRowContext(ctx, query, args...)}
}
