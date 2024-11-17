package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"errors"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"

	wallet "wallet/internal/model"
	"wallet/internal/service"
)

const (
	ErrInsufficientFunds    = "недостаточно средств"
	ErrWalletNotFound       = "кошелек не найден"
	ErrMethodNotAllowed     = "Метод не поддерживается"
	ErrInvalidUUID          = "Неверный формат UUID кошелька"
	ErrBalanceRetrievalFail = "Ошибка при получении баланса"
	ErrJSONParseFail        = "Ошибка при разборе JSON"
	ErrInvalidOperation     = "Неверный тип операции"
	ErrTransactionCommit    = "Ошибка при подтверждении транзакции"
	SuccessDeposit          = "Средства успешно внесены"
	SuccessWithdraw         = "Средства успешно сняты"
	ErrServerBusy           = "Server is busy"
	ErrParseRequest         = "Ошибка при разборе запроса"
	ErrTooManyRequests      = "Too many requests"
	ErrSerialization        = "Ошибка сериализации"
	ErrQueueAdd             = "Ошибка добавления в очередь"
	ErrSendResponse         = "Ошибка при отправке ответа"
	SuccessQueueAdd         = "Операция добавлена в очередь"
	SuccessOperation        = "Операция выполнена успешно"
	ErrTxCreate             = "ошибка при создании транзакции"
	ErrBalanceGet           = "ошибка при получении баланса"
	ErrBalanceUpdate        = "ошибка при обновлении баланса"
	ErrTxRecord             = "ошибка при записи транзакции"
	ErrTxCommit             = "ошибка при подтверждении транзакции"
	ErrBalanceGetDB         = "ошибка при получении баланса"
)

type WalletError struct {
	Code    int
	Message string
	Err     error
}

// Добавляем метод Error()
func (e *WalletError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

type Config struct {
	MaxRetries       int
	OperationTimeout time.Duration
	ConcurrencyLimit int
}

type WalletHandler struct {
	db          DBInterface
	cache       CacheInterface
	validator   *service.WalletValidator
	config      Config
	rateLimiter *rate.Limiter
	debugMode   bool
	semaphore   chan struct{}
}

type DBInterface interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) RowScanner
	BeginTx(ctx context.Context) (TxInterface, error)
}

type RowScanner interface {
	Scan(dest ...interface{}) error
}

type RowInterface interface {
	Scan(dest ...interface{}) error
}

type ResultInterface interface {
	LastInsertId() (int64, error)
	RowsAffected() (int64, error)
}

type TxInterface interface {
	QueryRowContext(ctx context.Context, query string, args ...any) RowInterface
	ExecContext(ctx context.Context, query string, args ...interface{}) (ResultInterface, error)
	Rollback() error
	Commit() error
}

type CacheInterface interface {
	LPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd
	BRPop(ctx context.Context, timeout time.Duration, keys ...string) *redis.StringSliceCmd
	Delete(ctx context.Context, key string) error
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
}

func NewWalletHandler(db DBInterface, cache CacheInterface, debugMode bool) *WalletHandler {
	return &WalletHandler{
		db:          db,
		cache:       cache,
		validator:   service.NewWalletValidator(),
		rateLimiter: rate.NewLimiter(rate.Limit(2000), 1000),
		debugMode:   debugMode,
		semaphore:   make(chan struct{}, 1000),
	}
}

func (h *WalletHandler) GetWalletBalance(w http.ResponseWriter, r *http.Request) {
	// Добавляем CORS заголовки
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Обрабатываем preflight запрос
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	select {
	case h.semaphore <- struct{}{}:
		defer func() { <-h.semaphore }()
	default:
		http.Error(w, ErrServerBusy, http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	walletID, err := uuid.Parse(r.URL.Path[len("/api/v1/wallets/"):])
	if err != nil {
		http.Error(w, ErrInvalidUUID, http.StatusBadRequest)
		return
	}

	cacheKey := fmt.Sprintf("balance:%s", walletID)

	for i := 0; i < 3; i++ {
		if balance, err := h.cache.Get(ctx, cacheKey); err == nil {
			if err := h.sendResponse(w, balance); err == nil {
				return
			}
		}
		time.Sleep(time.Millisecond * 50 * time.Duration(i+1))
	}

	var balance float64
	var dbErr error
	for i := 0; i < 3; i++ {
		balance, dbErr = h.getBalanceFromDB(ctx, walletID)
		if dbErr == nil {
			break
		}
		if dbErr.Error() == ErrWalletNotFound {
			http.Error(w, ErrWalletNotFound, http.StatusNotFound)
			return
		}
		time.Sleep(time.Millisecond * 50 * time.Duration(i+1))
	}

	if dbErr != nil {
		http.Error(w, ErrBalanceRetrievalFail, http.StatusServiceUnavailable)
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		h.cache.Set(ctx, cacheKey, balance, 30*time.Second)
	}()

	if err := h.sendResponse(w, map[string]float64{"balance": balance}); err != nil {
		http.Error(w, ErrSendResponse, http.StatusServiceUnavailable)
		return
	}
}

func (h *WalletHandler) HandleWalletOperation(w http.ResponseWriter, r *http.Request) {
	if !h.rateLimiter.Allow() {
		http.Error(w, ErrTooManyRequests, http.StatusTooManyRequests)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, ErrMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}

	// Декодируем запрос
	var request wallet.WalletRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, ErrParseRequest, http.StatusBadRequest)
		return
	}

	// Преобразуем string в uuid.UUID после декодирования
	walletUUID, err := uuid.Parse(request.WalletID)
	if err != nil {
		http.Error(w, ErrInvalidUUID, http.StatusBadRequest)
		return
	}

	// Создаем новый запрос с правильным UUID
	validatedRequest := wallet.WalletRequest{
		WalletID:      walletUUID.String(),
		OperationType: request.OperationType,
		Amount:        request.Amount,
	}

	// Валидируем запрос перед обработкой
	if err := h.validator.ValidateWalletRequest(&validatedRequest); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// В режиме отладки обрабатываем операцию напрямую
	if h.debugMode {
		if err := h.handleOperation(r.Context(), &validatedRequest); err != nil {
			http.Error(w, err.Message, err.Code)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status": SuccessOperation,
		})
		return
	}

	// Стандартная обработка через очередь
	operationJSON, err := json.Marshal(validatedRequest)
	if err != nil {
		http.Error(w, ErrSerialization, http.StatusInternalServerError)
		return
	}

	// Отправляем в очередь
	ctx := context.Background()
	err = h.cache.LPush(ctx, "wallet_operations", operationJSON).Err()
	if err != nil {
		http.Error(w, ErrQueueAdd, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"status": SuccessQueueAdd,
	})
}

func (h *WalletHandler) ProcessQueue(ctx context.Context) {
	// Добавляем worker pool
	workers := make(chan struct{}, h.config.ConcurrencyLimit)

	for {
		select {
		case <-ctx.Done():
			return
		case workers <- struct{}{}:
			go func() {
				defer func() { <-workers }()
				h.processQueueItem(ctx)
			}()
		}
	}
}

func (h *WalletHandler) processQueueItem(ctx context.Context) {
	// Ожидаем новую операцию из очереди с таймаутом
	result := h.cache.BRPop(ctx, 0, "wallet_operations")
	if result.Err() != nil {
		return
	}

	// Получаем операцию из результата
	var operation wallet.WalletRequest
	if err := json.Unmarshal([]byte(result.Val()[1]), &operation); err != nil {
		return
	}

	// Обрабатываем операцию
	h.ProcessQueueOperation(operation)
}

func (h *WalletHandler) ProcessQueueOperation(op wallet.WalletRequest) error {
	if err := h.handleOperation(context.Background(), &op); err != nil {
		return err.Err
	}
	return nil
}

func (h *WalletHandler) beginTx(ctx context.Context) (TxInterface, error) {
	tx, err := h.db.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ErrTxCreate, err)
	}
	return tx, nil
}

func (h *WalletHandler) getCurrentBalance(tx TxInterface, walletID uuid.UUID) (float64, error) {
	var currentBalance float64
	err := tx.QueryRowContext(context.Background(), "SELECT balance FROM wallets WHERE id = $1 FOR UPDATE", walletID).Scan(&currentBalance)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, errors.New(ErrWalletNotFound)
		}
		return 0, fmt.Errorf("%s: %w", ErrBalanceGet, err)
	}
	return currentBalance, nil
}

func (h *WalletHandler) updateBalance(tx TxInterface, walletID uuid.UUID, newBalance float64) error {
	_, err := tx.ExecContext(context.Background(), "UPDATE wallets SET balance = $1 WHERE id = $2", newBalance, walletID)
	if err != nil {
		return fmt.Errorf("%s: %w", ErrBalanceUpdate, err)
	}
	return nil
}

func (h *WalletHandler) recordTransaction(tx TxInterface, walletID uuid.UUID, amount float64, operationType wallet.OperationType) error {
	_, err := tx.ExecContext(context.Background(), `
		INSERT INTO transactions (wallet_id, amount, operation_type, created_at)
		VALUES ($1, $2, $3, NOW())
	`, walletID, amount, operationType)
	if err != nil {
		return fmt.Errorf("%s: %w", ErrTxRecord, err)
	}
	return nil
}

func (h *WalletHandler) handleOperation(ctx context.Context, req *wallet.WalletRequest) *WalletError {
	// Валидация перед операцией
	if err := h.validator.ValidateAmount(req.Amount); err != nil {
		return &WalletError{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
			Err:     err,
		}
	}

	if err := h.validator.ValidateOperationType(req.OperationType); err != nil {
		return &WalletError{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
			Err:     err,
		}
	}

	tx, err := h.beginTx(ctx)
	if err != nil {
		return &WalletError{
			Code:    http.StatusInternalServerError,
			Message: ErrTxCreate,
			Err:     err,
		}
	}
	defer tx.Rollback()

	walletUUID, err := uuid.Parse(req.WalletID)
	if err != nil {
		return &WalletError{
			Code:    http.StatusBadRequest,
			Message: ErrInvalidUUID,
			Err:     err,
		}
	}

	currentBalance, err := h.getCurrentBalance(tx, walletUUID)
	if err != nil {
		if err.Error() == ErrWalletNotFound {
			return &WalletError{
				Code:    http.StatusNotFound,
				Message: ErrWalletNotFound,
				Err:     err,
			}
		}
		return &WalletError{
			Code:    http.StatusInternalServerError,
			Message: ErrBalanceGet,
			Err:     err,
		}
	}

	// Проверяем достаточно ли средств
	if err := h.validator.ValidateBalance(currentBalance, req.Amount); err != nil {
		return &WalletError{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
			Err:     err,
		}
	}

	switch req.OperationType {
	case wallet.DEPOSIT:
		newBalance := currentBalance + req.Amount
		if err := h.updateBalance(tx, walletUUID, newBalance); err != nil {
			return &WalletError{
				Code:    http.StatusInternalServerError,
				Message: ErrBalanceUpdate,
				Err:     err,
			}
		}
	case wallet.WITHDRAW:
		if err := h.handleWithdraw(nil, req); err != nil {
			return &WalletError{
				Code:    http.StatusInternalServerError,
				Message: err.Error(),
				Err:     err,
			}
		}
	default:
		return &WalletError{
			Code:    http.StatusBadRequest,
			Message: ErrInvalidOperation,
		}
	}

	if err := h.recordTransaction(tx, walletUUID, req.Amount, req.OperationType); err != nil {
		return &WalletError{
			Code:    http.StatusInternalServerError,
			Message: ErrTxRecord,
			Err:     err,
		}
	}

	// Пдтвеждаем транзакцию
	if err = tx.Commit(); err != nil {
		return &WalletError{
			Code:    http.StatusInternalServerError,
			Message: ErrTxCommit,
			Err:     err,
		}
	}

	return nil
}

func (h *WalletHandler) handleWithdraw(w http.ResponseWriter, req *wallet.WalletRequest) error {
	tx, err := h.beginTx(context.Background())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}
	defer tx.Rollback()

	walletUUID, err := uuid.Parse(req.WalletID)
	if err != nil {
		return &WalletError{
			Code:    http.StatusBadRequest,
			Message: ErrInvalidUUID,
			Err:     err,
		}
	}

	currentBalance, err := h.getCurrentBalance(tx, walletUUID)
	if err != nil {
		if err.Error() == ErrWalletNotFound {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return err
	}

	// Проверяем достаточно ли средств
	if err := h.validator.ValidateBalance(currentBalance, req.Amount); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return err
	}

	newBalance := currentBalance - req.Amount
	if err := h.updateBalance(tx, walletUUID, newBalance); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}

	if err := h.recordTransaction(tx, walletUUID, -req.Amount, req.OperationType); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}

	if err = tx.Commit(); err != nil {
		http.Error(w, ErrTransactionCommit, http.StatusInternalServerError)
		return err
	}

	return h.sendSuccessResponse(w, SuccessWithdraw)
}

func (h *WalletHandler) sendResponse(w http.ResponseWriter, data interface{}) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(data)
}

func (h *WalletHandler) sendSuccessResponse(w http.ResponseWriter, message string) error {
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(map[string]string{
		"status": message,
	})
}

func (h *WalletHandler) getBalanceFromDB(ctx context.Context, walletID uuid.UUID) (float64, error) {
	var balance float64
	log.Printf("Получение баланса для кошелька: %s", walletID)

	err := h.db.QueryRowContext(
		ctx,
		"SELECT balance FROM wallets WHERE id = $1",
		walletID,
	).Scan(&balance)

	if err != nil {
		log.Printf("Ошибка при получении баланса: %v", err)
		if err == sql.ErrNoRows {
			return 0, errors.New(ErrWalletNotFound)
		}
		return 0, fmt.Errorf("%s: %w", ErrBalanceGetDB, err)
	}

	log.Printf("Получен баланс: %f", balance)
	return balance, nil
}
