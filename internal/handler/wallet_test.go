package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	wallet "wallet/internal/model"
	"wallet/internal/service"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"golang.org/x/time/rate"
)

type MockRow struct {
	mock.Mock
}

func (m *MockRow) Scan(dest ...interface{}) error {
	args := m.Called(dest...)
	if args.Get(0) != nil {
		balance := dest[0].(*float64)
		*balance = 100.0
	}
	return args.Error(0)
}

type MockDB struct {
	mock.Mock
}

func (m *MockDB) QueryRowContext(ctx context.Context, query string, args ...interface{}) RowScanner {
	called := m.Called(ctx, query, args[0])
	return called.Get(0).(RowScanner)
}

func (m *MockDB) BeginTx(ctx context.Context) (TxInterface, error) {
	args := m.Called(ctx)
	return args.Get(0).(TxInterface), args.Error(1)
}

type MockTx struct {
	mock.Mock
}

func (m *MockTx) QueryRowContext(ctx context.Context, query string, args ...interface{}) RowInterface {
	called := m.Called(ctx, query, args)
	return called.Get(0).(RowInterface)
}

func (m *MockTx) ExecContext(ctx context.Context, query string, args ...interface{}) (ResultInterface, error) {
	called := m.Called(ctx, query, args)
	return called.Get(0).(ResultInterface), called.Error(1)
}

func (m *MockTx) Rollback() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockTx) Commit() error {
	args := m.Called()
	return args.Error(0)
}

type MockCache struct {
	mock.Mock
}

func (m *MockCache) Delete(ctx context.Context, key string) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

func (m *MockCache) LPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd {
	args := m.Called(ctx, key, values)
	return args.Get(0).(*redis.IntCmd)
}

func (m *MockCache) BRPop(ctx context.Context, timeout time.Duration, keys ...string) *redis.StringSliceCmd {
	args := m.Called(ctx, timeout, keys)
	return args.Get(0).(*redis.StringSliceCmd)
}

func (m *MockCache) Get(ctx context.Context, key string) (string, error) {
	args := m.Called(ctx, key)
	return args.String(0), args.Error(1)
}

func (m *MockCache) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	args := m.Called(ctx, key, value, expiration)
	return args.Error(0)
}

func TestAll(t *testing.T) {
	// Основные тесты обработчиков HTTP
	t.Run("GetWalletBalance", TestGetWalletBalance)
	t.Run("HandleWalletOperation", TestHandleWalletOperation)

	// Тесты обработки очереди
	t.Run("ProcessQueue", TestProcessQueue)

	// Тесты вспомогательных методов
	t.Run("HelperMethods", TestHelperMethods)

	// Тесты моков
	t.Run("Mocks", func(t *testing.T) {
		t.Run("MockRow", testMockRow)
		t.Run("MockDB", testMockDB)
		t.Run("MockTx", testMockTx)
		t.Run("MockCache", testMockCache)
		t.Run("MockResult", testMockResult)
	})
}

// Тесты для GetWalletBalance
func TestGetWalletBalance(t *testing.T) {
	tests := []struct {
		name          string
		walletID      string
		expectedCode  int
		expectedError string
		mockSetup     func(string, *MockDB, *MockCache)
	}{
		{
			name:          "Неверный UUID",
			walletID:      "invalid-uuid",
			expectedCode:  http.StatusBadRequest,
			expectedError: ErrInvalidUUID,
			mockSetup:     nil,
		},
		{
			name:         "Успешное получение баланса из кэша",
			walletID:     uuid.New().String(),
			expectedCode: http.StatusOK,
			mockSetup: func(walletID string, db *MockDB, cache *MockCache) {
				cacheKey := fmt.Sprintf("balance:%s", walletID)
				cache.On("Get", mock.Anything, cacheKey).Return("100.0", nil).Once()
			},
		},
		{
			name:         "Успешное получение баланса из БД",
			walletID:     uuid.New().String(),
			expectedCode: http.StatusOK,
			mockSetup: func(walletID string, db *MockDB, cache *MockCache) {
				cacheKey := fmt.Sprintf("balance:%s", walletID)
				cache.On("Get", mock.Anything, cacheKey).Return("", redis.Nil).Times(3)

				mockRow := new(MockRow)
				mockRow.On("Scan", mock.Anything).Return(nil).Once()

				parsedUUID, _ := uuid.Parse(walletID)
				db.On("QueryRowContext",
					mock.Anything,
					"SELECT balance FROM wallets WHERE id = $1",
					parsedUUID,
				).Return(mockRow).Once()

				// Добавляем ожидание вызова Set с любыми параметрами
				cache.On("Set",
					mock.Anything,
					mock.AnythingOfType("string"),
					mock.AnythingOfType("float64"),
					mock.AnythingOfType("time.Duration"),
				).Return(nil).Maybe() // Maybe() позволяет вызову быть опциональным
			},
		},
		{
			name:          "Кошелек не найден",
			walletID:      uuid.New().String(),
			expectedCode:  http.StatusNotFound,
			expectedError: ErrWalletNotFound,
			mockSetup: func(walletID string, db *MockDB, cache *MockCache) {
				cacheKey := fmt.Sprintf("balance:%s", walletID)
				cache.On("Get", mock.Anything, cacheKey).Return("", redis.Nil).Times(3)

				mockRow := new(MockRow)
				mockRow.On("Scan", mock.Anything).Return(sql.ErrNoRows).Once()

				parsedUUID, _ := uuid.Parse(walletID)
				db.On("QueryRowContext",
					mock.Anything,
					"SELECT balance FROM wallets WHERE id = $1",
					parsedUUID,
				).Return(mockRow).Once()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDB := new(MockDB)
			mockCache := new(MockCache)

			if tt.mockSetup != nil {
				tt.mockSetup(tt.walletID, mockDB, mockCache)
			}

			handler := NewWalletHandler(mockDB, mockCache, false)
			req := httptest.NewRequest("GET", "/api/v1/wallets/"+tt.walletID, nil)
			w := httptest.NewRecorder()

			handler.GetWalletBalance(w, req)

			assert.Equal(t, tt.expectedCode, w.Code)
			if tt.expectedError != "" {
				assert.Contains(t, w.Body.String(), tt.expectedError)
			}

			// Даем время на выполнение асинхронной операции
			time.Sleep(100 * time.Millisecond)

			mockDB.AssertExpectations(t)
			mockCache.AssertExpectations(t)
		})
	}
}

// Тесты для HandleWalletOperation
func TestHandleWalletOperation(t *testing.T) {
	tests := []struct {
		name          string
		request       wallet.WalletRequest
		expectedCode  int
		expectedError string
		mockSetup     func(*MockDB, *MockCache)
	}{
		{
			name: "Успешное добавление в очередь",
			request: wallet.WalletRequest{
				WalletID:      uuid.New().String(),
				OperationType: wallet.DEPOSIT,
				Amount:        100,
			},
			expectedCode: http.StatusAccepted,
			mockSetup: func(db *MockDB, cache *MockCache) {
				cache.On("LPush", mock.Anything, "wallet_operations", mock.Anything).
					Return(redis.NewIntCmd(context.Background())).Once()
			},
		},
		{
			name: "Неверная сумма",
			request: wallet.WalletRequest{
				WalletID:      uuid.New().String(),
				OperationType: wallet.DEPOSIT,
				Amount:        -100,
			},
			expectedCode:  http.StatusBadRequest,
			expectedError: fmt.Errorf(service.ErrValidationPrefix, service.ErrNegativeAmount).Error(),
		},
		{
			name: "Неверный формат UUID",
			request: wallet.WalletRequest{
				WalletID:      "invalid-uuid",
				OperationType: wallet.DEPOSIT,
				Amount:        100,
			},
			expectedCode:  http.StatusBadRequest,
			expectedError: ErrInvalidUUID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDB := new(MockDB)
			mockCache := new(MockCache)

			if tt.mockSetup != nil {
				tt.mockSetup(mockDB, mockCache)
			}

			handler := NewWalletHandler(mockDB, mockCache, false)

			body, _ := json.Marshal(tt.request)
			req := httptest.NewRequest("POST", "/api/v1/wallet", bytes.NewBuffer(body))
			w := httptest.NewRecorder()

			handler.HandleWalletOperation(w, req)

			assert.Equal(t, tt.expectedCode, w.Code)
			if tt.expectedError != "" {
				assert.Contains(t, w.Body.String(), tt.expectedError)
			}

			mockDB.AssertExpectations(t)
			mockCache.AssertExpectations(t)
		})
	}
}

// Тесты для ProcessQueue
func TestProcessQueue(t *testing.T) {
	tests := []struct {
		name      string
		operation wallet.WalletRequest
		mockSetup func(*MockDB, *MockCache)
	}{
		{
			name: "Успешная обработка операции",
			operation: wallet.WalletRequest{
				WalletID:      uuid.New().String(),
				OperationType: wallet.DEPOSIT,
				Amount:        100,
			},
			mockSetup: func(db *MockDB, cache *MockCache) {
				opJSON, _ := json.Marshal(wallet.WalletRequest{
					WalletID:      uuid.New().String(),
					OperationType: wallet.DEPOSIT,
					Amount:        100,
				})

				// Настраиваем первый ответ очереди
				successCmd := redis.NewStringSliceCmd(context.Background())
				successCmd.SetVal([]string{"wallet_operations", string(opJSON)})
				cache.On("BRPop", mock.Anything, time.Duration(0), []string{"wallet_operations"}).
					Return(successCmd).Once()

				// Настраиваем второй ответ с ошибкой для завершения цикла
				errorCmd := redis.NewStringSliceCmd(context.Background())
				errorCmd.SetErr(context.Canceled)
				cache.On("BRPop", mock.Anything, time.Duration(0), []string{"wallet_operations"}).
					Return(errorCmd).Maybe()

				// Настраиваем транзакцию
				mockTx := new(MockTx)
				db.On("BeginTx", mock.Anything).Return(mockTx, nil).Once()

				// Настраиваем получение баланса
				mockRow := new(MockRow)
				mockRow.On("Scan", mock.Anything).Return(nil).Once()
				mockTx.On("QueryRowContext", mock.Anything, mock.Anything, mock.Anything).
					Return(mockRow).Once()

				// Настраиваем обновление баланса и запись транзакции
				mockResult := &MockResult{}
				mockTx.On("ExecContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(mockResult, nil).Times(2)

				// Настраиваем Rollback и Commit
				mockTx.On("Rollback").Return(nil).Maybe()
				mockTx.On("Commit").Return(nil).Once()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDB := new(MockDB)
			mockCache := new(MockCache)

			if tt.mockSetup != nil {
				tt.mockSetup(mockDB, mockCache)
			}

			handler := &WalletHandler{
				db:          mockDB,
				cache:       mockCache,
				validator:   &service.WalletValidator{},
				config:      Config{ConcurrencyLimit: 1},
				rateLimiter: rate.NewLimiter(rate.Limit(100), 1),
			}

			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			handler.ProcessQueue(ctx)

			mockDB.AssertExpectations(t)
			mockCache.AssertExpectations(t)
		})
	}
}

// Добавляем MockResult
type MockResult struct {
	mock.Mock
}

func (m *MockResult) LastInsertId() (int64, error) {
	args := m.Called()
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockResult) RowsAffected() (int64, error) {
	args := m.Called()
	return args.Get(0).(int64), args.Error(1)
}

// Тесты для вспомоательных методов
func TestHelperMethods(t *testing.T) {
	mockDB := new(MockDB)
	handler := NewWalletHandler(mockDB, nil, false)

	t.Run("getCurrentBalance", func(t *testing.T) {
		mockTx := new(MockTx)
		mockRow := new(MockRow)
		walletID := uuid.New()
		expectedBalance := 100.0

		mockTx.On("QueryRowContext",
			mock.Anything,
			"SELECT balance FROM wallets WHERE id = $1 FOR UPDATE",
			mock.Anything,
		).Return(mockRow).Once()

		mockRow.On("Scan", mock.Anything).Run(func(args mock.Arguments) {
			balance := args.Get(0).(*float64)
			*balance = expectedBalance
		}).Return(nil).Once()

		balance, err := handler.getCurrentBalance(mockTx, walletID)
		assert.NoError(t, err)
		assert.Equal(t, expectedBalance, balance)

		mockTx.AssertExpectations(t)
		mockRow.AssertExpectations(t)
	})

	t.Run("beginTx", func(t *testing.T) {
		mockTx := new(MockTx)
		mockDB.On("BeginTx", mock.Anything).Return(mockTx, nil).Once()

		tx, err := handler.beginTx(context.Background())
		assert.NoError(t, err)
		assert.NotNil(t, tx)

		mockDB.AssertExpectations(t)
	})
}

func testMockRow(t *testing.T) {
	mockRow := new(MockRow)
	var balance float64
	mockRow.On("Scan", mock.Anything).Return(nil).Once()
	err := mockRow.Scan(&balance)
	assert.NoError(t, err)
	mockRow.AssertExpectations(t)
}

func testMockDB(t *testing.T) {
	mockDB := new(MockDB)
	ctx := context.Background()
	mockRow := new(MockRow)

	mockDB.On("QueryRowContext", mock.Anything, mock.Anything, mock.Anything).Return(mockRow).Once()
	row := mockDB.QueryRowContext(ctx, "query", "arg")
	assert.NotNil(t, row)

	mockDB.AssertExpectations(t)
}

func testMockTx(t *testing.T) {
	mockTx := new(MockTx)
	mockRow := new(MockRow)

	mockTx.On("QueryRowContext", mock.Anything, mock.Anything, mock.Anything).Return(mockRow).Once()
}

func testMockCache(t *testing.T) {
	mockCache := new(MockCache)
	ctx := context.Background()

	mockCache.On("Get", mock.Anything, mock.Anything).Return("", nil).Once()
	_, err := mockCache.Get(ctx, "test-key")
	assert.NoError(t, err)

	mockCache.AssertExpectations(t)
}

func testMockResult(t *testing.T) {
	mockResult := new(MockResult)

	mockResult.On("RowsAffected").Return(int64(1), nil).Once()
	rows, err := mockResult.RowsAffected()

	assert.NoError(t, err)
	assert.Equal(t, int64(1), rows)
	mockResult.AssertExpectations(t)
}
