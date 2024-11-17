package service

import (
	"fmt"
	wallet "wallet/internal/model"

	"errors"

	"github.com/google/uuid"
)

const (
	ErrInvalidOperationType = "неверный тип операции: %s"
	ErrValidationPrefix     = "ошибка валидации: %w"
)

var (
	ErrNilRequest        = errors.New("request не может быть nil")
	ErrEmptyWalletID     = errors.New("wallet ID не может быть пустым")
	ErrNegativeAmount    = errors.New("сумма должна быть положительной")
	ErrInsufficientFunds = errors.New("недостаточно средств")
	ErrInvalidAmount     = errors.New("некорректная сумма")
)

type WalletValidator struct{}

func NewWalletValidator() *WalletValidator {
	return &WalletValidator{}
}

func (v *WalletValidator) ValidateWalletRequest(req *wallet.WalletRequest) error {
	if err := v.validateRequest(req); err != nil {
		return fmt.Errorf(ErrValidationPrefix, err)
	}
	return nil
}

func (v *WalletValidator) validateRequest(req *wallet.WalletRequest) error {
	if req == nil {
		return ErrNilRequest
	}

	walletID, err := uuid.Parse(req.WalletID)
	if err != nil {
		return fmt.Errorf("неверный формат UUID: %w", err)
	}

	if err := v.ValidateWalletID(walletID); err != nil {
		return err
	}

	if err := v.ValidateAmount(req.Amount); err != nil {
		return err
	}

	if err := v.ValidateOperationType(req.OperationType); err != nil {
		return err
	}

	return nil
}

func (v *WalletValidator) ValidateWalletID(id uuid.UUID) error {
	if id == uuid.Nil {
		return ErrEmptyWalletID
	}
	return nil
}

func (v *WalletValidator) ValidateAmount(amount float64) error {
	if amount < 0 {
		return ErrNegativeAmount
	}
	return nil
}

func (v *WalletValidator) ValidateOperationType(opType wallet.OperationType) error {
	if opType != wallet.DEPOSIT && opType != wallet.WITHDRAW {
		return fmt.Errorf(ErrInvalidOperationType, opType)
	}
	return nil
}

func (v *WalletValidator) ValidateBalance(currentBalance, requestAmount float64) error {
	if currentBalance < requestAmount {
		return ErrInsufficientFunds
	}
	return nil
}
