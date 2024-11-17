 -- Механизм блокировки строки при чтении
ALTER TABLE wallets ADD CONSTRAINT positive_balance CHECK (balance >= 0);
-- Индекс для оптимизации запросов
CREATE INDEX idx_wallet_transactions ON transactions(wallet_id, created_at);