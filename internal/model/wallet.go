package wallet

type OperationType string

const (
	DEPOSIT  OperationType = "DEPOSIT"
	WITHDRAW OperationType = "WITHDRAW"
)

type WalletRequest struct {
	WalletID      string        `json:"wallet_id"`
	OperationType OperationType `json:"operation_type"`
	Amount        float64       `json:"amount"`
}
