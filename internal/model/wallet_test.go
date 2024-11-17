package wallet

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestAll(t *testing.T) {
	t.Run("OperationTypeConstants", TestOperationTypeConstants)
	t.Run("WalletRequestJSONMarshaling", TestWalletRequestJSONMarshaling)
}

func TestOperationTypeConstants(t *testing.T) {
	// Проверяем, что константы имеют ожидаемые значения
	assert.Equal(t, OperationType("DEPOSIT"), DEPOSIT)
	assert.Equal(t, OperationType("WITHDRAW"), WITHDRAW)
}

func TestWalletRequestJSONMarshaling(t *testing.T) {
	testUUID := uuid.MustParse("123e4567-e89b-12d3-a456-426614174000")

	tests := []struct {
		name    string
		request WalletRequest
	}{
		{
			name: "Запрос депозита",
			request: WalletRequest{
				WalletID:      testUUID.String(),
				OperationType: DEPOSIT,
				Amount:        100.50,
			},
		},
		{
			name: "Запрос вывода",
			request: WalletRequest{
				WalletID:      testUUID.String(),
				OperationType: WITHDRAW,
				Amount:        50.25,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// структура -> json
			jsonData, err := json.Marshal(tt.request)
			assert.NoError(t, err)

			// json -> структура
			var decoded WalletRequest
			err = json.Unmarshal(jsonData, &decoded)
			assert.NoError(t, err)

			// Сравниваем структуры
			assert.Equal(t, tt.request.WalletID, decoded.WalletID)
			assert.Equal(t, tt.request.OperationType, decoded.OperationType)
			assert.Equal(t, tt.request.Amount, decoded.Amount)
		})
	}
}
