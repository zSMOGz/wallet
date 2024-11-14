package wallet

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"errors"

	"github.com/google/uuid"

	wallet "wallet/internal/model"
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
)

type WalletHandler struct {
	db *sql.DB
}

func NewWalletHandler(db *sql.DB) *WalletHandler {
	return &WalletHandler{db: db}
}

func (h *WalletHandler) GetWalletBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, ErrMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}

	// Получаем UUID кошелька из URL
	walletID := r.URL.Path[len("/api/v1/wallets/"):]

	// Проверяем валидность UUID
	_, err := uuid.Parse(walletID)
	if err != nil {
		http.Error(w, ErrInvalidUUID, http.StatusBadRequest)
		return
	}

	// Запрос баланса из БД
	var balance float64
	err = h.db.QueryRow("SELECT balance FROM wallets WHERE id = $1", walletID).Scan(&balance)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, ErrWalletNotFound, http.StatusNotFound)
			return
		}
		http.Error(w, ErrBalanceRetrievalFail, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"walletId": walletID,
		"balance":  balance,
	})
}

func (h *WalletHandler) HandleWalletOperation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, ErrMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}

	var req wallet.WalletRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, ErrJSONParseFail, http.StatusBadRequest)
		return
	}

	switch req.OperationType {
	case wallet.DEPOSIT:
		h.handleDeposit(w, &req)
	case wallet.WITHDRAW:
		h.handleWitdraw(w, &req)
	default:
		http.Error(w, ErrInvalidOperation, http.StatusBadRequest)
	}
}

func (h *WalletHandler) beginTx() (*sql.Tx, error) {
	tx, err := h.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("ошибка при создании транзакции: %w", err)
	}
	return tx, nil
}

func (h *WalletHandler) getCurrentBalance(tx *sql.Tx, walletID uuid.UUID) (float64, error) {
	var currentBalance float64
	err := tx.QueryRow("SELECT balance FROM wallets WHERE id = $1", walletID).Scan(&currentBalance)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, errors.New(ErrWalletNotFound)
		}
		return 0, fmt.Errorf("ошибка при получении баланса: %w", err)
	}
	return currentBalance, nil
}

func (h *WalletHandler) updateBalance(tx *sql.Tx, walletID uuid.UUID, newBalance float64) error {
	_, err := tx.Exec("UPDATE wallets SET balance = $1 WHERE id = $2", newBalance, walletID)
	if err != nil {
		return fmt.Errorf("ошибка при обновлении баланса: %w", err)
	}
	return nil
}

func (h *WalletHandler) recordTransaction(tx *sql.Tx, walletID uuid.UUID, operationType wallet.OperationType, amount float64) error {
	_, err := tx.Exec(`
		INSERT INTO transactions (wallet_id, operation_type, amount, created_at)
		VALUES ($1, $2, $3, NOW())
	`, walletID, operationType, amount)
	if err != nil {
		return fmt.Errorf("ошибка при записи транзакции: %w", err)
	}
	return nil
}

func (h *WalletHandler) sendSuccessResponse(w http.ResponseWriter, message string) error {
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(map[string]string{
		"status": message,
	})
}

func (h *WalletHandler) handleDeposit(w http.ResponseWriter, req *wallet.WalletRequest) error {
	tx, err := h.beginTx()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}
	defer tx.Rollback()

	currentBalance, err := h.getCurrentBalance(tx, req.WalletID)
	if err != nil {
		if err.Error() == ErrWalletNotFound {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return err
	}

	newBalance := currentBalance + req.Amount
	if err := h.updateBalance(tx, req.WalletID, newBalance); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}

	if err := h.recordTransaction(tx, req.WalletID, req.OperationType, req.Amount); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}

	// Подтверждаем транзакцию
	if err = tx.Commit(); err != nil {
		http.Error(w, ErrTransactionCommit, http.StatusInternalServerError)
		return err
	}

	return h.sendSuccessResponse(w, SuccessDeposit)
}

func (h *WalletHandler) handleWitdraw(w http.ResponseWriter, req *wallet.WalletRequest) error {
	tx, err := h.beginTx()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}
	defer tx.Rollback()

	currentBalance, err := h.getCurrentBalance(tx, req.WalletID)
	if err != nil {
		if err.Error() == ErrWalletNotFound {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return err
	}

	// Проверяем достаточно ли средств
	if currentBalance < req.Amount {
		err := errors.New(ErrInsufficientFunds)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return err
	}

	newBalance := currentBalance - req.Amount
	if err := h.updateBalance(tx, req.WalletID, newBalance); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}

	if err := h.recordTransaction(tx, req.WalletID, req.OperationType, -req.Amount); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}

	// Подтверждаем транзакцию
	if err = tx.Commit(); err != nil {
		http.Error(w, ErrTransactionCommit, http.StatusInternalServerError)
		return err
	}

	return h.sendSuccessResponse(w, SuccessWithdraw)
}
