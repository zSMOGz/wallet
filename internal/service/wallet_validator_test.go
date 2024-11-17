package service

import (
	"fmt"
	"testing"
	wallet "wallet/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestAll(t *testing.T) {
	t.Run("WalletValidator", TestWalletValidator)
	t.Run("ValidateNilRequest", TestWalletValidator_ValidateNilRequest)
}

func TestWalletValidator(t *testing.T) {
	validator := NewWalletValidator()
	validUUID := uuid.New()

	tests := []struct {
		name        string
		request     *wallet.WalletRequest
		expectedErr string
	}{
		{
			name: "Валидный запрос депозита",
			request: &wallet.WalletRequest{
				WalletID:      validUUID.String(),
				OperationType: wallet.DEPOSIT,
				Amount:        100.0,
			},
			expectedErr: "",
		},
		{
			name: "Валидный запрос вывода",
			request: &wallet.WalletRequest{
				WalletID:      validUUID.String(),
				OperationType: wallet.WITHDRAW,
				Amount:        50.0,
			},
			expectedErr: "",
		},
		{
			name: "Пустой UUID",
			request: &wallet.WalletRequest{
				WalletID:      uuid.Nil.String(),
				OperationType: wallet.DEPOSIT,
				Amount:        100.0,
			},
			expectedErr: fmt.Errorf(ErrValidationPrefix, ErrEmptyWalletID).Error(),
		},
		{
			name: "Отрицательная сумма",
			request: &wallet.WalletRequest{
				WalletID:      validUUID.String(),
				OperationType: wallet.DEPOSIT,
				Amount:        -100.0,
			},
			expectedErr: fmt.Errorf(ErrValidationPrefix, ErrNegativeAmount).Error(),
		},
		{
			name: "Неверный тип операции",
			request: &wallet.WalletRequest{
				WalletID:      validUUID.String(),
				OperationType: "INVALID",
				Amount:        100.0,
			},
			expectedErr: fmt.Errorf(ErrValidationPrefix, fmt.Errorf(ErrInvalidOperationType, "INVALID")).Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateWalletRequest(tt.request)
			if tt.expectedErr == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.expectedErr)
			}
		})
	}
}

// Тест на nil request
func TestWalletValidator_ValidateNilRequest(t *testing.T) {
	validator := NewWalletValidator()
	err := validator.ValidateWalletRequest(nil)
	assert.EqualError(t, err, fmt.Errorf(ErrValidationPrefix, ErrNilRequest).Error())
}
