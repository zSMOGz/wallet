package db

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

func TestAll(t *testing.T) {
	t.Run("DBAdapter", TestDBAdapter)
	t.Run("TxAdapter", TestTxAdapter)
}

func TestTxAdapter(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	ctx := context.Background()

	t.Run("QueryRowContext", func(t *testing.T) {
		// Настраиваем все ожидания до выполнения действий
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT (.+) FROM users").
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
				AddRow(1, "test"))
		mock.ExpectCommit()

		// Начинаем транзакцию
		tx, err := db.Begin()
		assert.NoError(t, err)

		txAdapter := &TxAdapter{tx}

		// Выполняем запрос
		row := txAdapter.QueryRowContext(ctx, "SELECT id, name FROM users")
		assert.NotNil(t, row)

		// Завершаем транзакцию
		err = tx.Commit()
		assert.NoError(t, err)

		// Проверяем, что все ожидания были выполнены
		err = mock.ExpectationsWereMet()
		assert.NoError(t, err)
	})

	t.Run("ExecContext", func(t *testing.T) {
		// Настраиваем все ожидания
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO users").
			WithArgs("test").
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		// Начинаем транзакцию
		tx, err := db.Begin()
		assert.NoError(t, err)

		txAdapter := &TxAdapter{tx}

		// Выполняем запрос
		result, err := txAdapter.ExecContext(ctx, "INSERT INTO users VALUES ($1)", "test")
		assert.NoError(t, err)
		assert.NotNil(t, result)

		rows, err := result.RowsAffected()
		assert.NoError(t, err)
		assert.Equal(t, int64(1), rows)

		// Завершаем транзакцию
		err = tx.Commit()
		assert.NoError(t, err)

		// Проверяем ожидания
		err = mock.ExpectationsWereMet()
		assert.NoError(t, err)
	})
}

func TestDBAdapter(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	assert.NoError(t, err)
	defer db.Close()

	t.Run("NewPostgresConnection", func(t *testing.T) {
		// Ожидаем пинг
		mock.ExpectPing()

		// Создаем новый мок-адаптер напрямую
		adapter := &DBAdapter{db}

		// Выполняем пинг
		err = adapter.Ping()
		assert.NoError(t, err)
		assert.NotNil(t, adapter)

		// Проверяем, что все ожидания выполнены
		err = mock.ExpectationsWereMet()
		assert.NoError(t, err)
	})

	// Создаем адаптер для остальных тестов
	dbAdapter := &DBAdapter{db}
	ctx := context.Background()

	t.Run("BeginTx", func(t *testing.T) {
		mock.ExpectBegin()

		tx, err := dbAdapter.BeginTx(ctx)
		assert.NoError(t, err)
		assert.NotNil(t, tx)

		err = mock.ExpectationsWereMet()
		assert.NoError(t, err)
	})

	t.Run("QueryRowContext_DBAdapter", func(t *testing.T) {
		mock.ExpectQuery("SELECT (.+) FROM users").
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
				AddRow(1, "test"))

		row := dbAdapter.QueryRowContext(ctx, "SELECT id, name FROM users")
		assert.NotNil(t, row)

		err = mock.ExpectationsWereMet()
		assert.NoError(t, err)
	})
}
