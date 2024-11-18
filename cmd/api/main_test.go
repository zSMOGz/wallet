package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
	"wallet/internal/cache"
	db "wallet/internal/db"
	"wallet/internal/handler"
	wallet "wallet/internal/model"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if err := godotenv.Load("../../config.test.env"); err != nil {
		log.Printf(ErrLoadEnvFile, err)
	}

	code := m.Run()
	os.Exit(code)
}

func TestAll(t *testing.T) {
	t.Run("DatabaseConnection", TestDatabaseConnection)
	t.Run("RedisConnection", TestRedisConnection)
	t.Run("HTTPEndpoints", TestHTTPEndpoints)
	t.Run("LoadPerformance", TestLoadPerformance)
	t.Run("DatabaseConnectionErrors", TestDatabaseConnectionErrors)
	t.Run("EnvVariablesErrors", TestEnvVariablesErrors)
}

func TestDatabaseConnection(t *testing.T) {
	// Подготовка тестовых данных для подключения
	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_HOST"),
		os.Getenv("DB_PORT"),
		os.Getenv("DB_NAME"))

	// Проверка подключения к БД
	db, err := db.NewPostgresConnection(dbURL)
	assert.NoError(t, err, ErrDBConnection)
	assert.NotNil(t, db)

	defer db.Close()
}

func TestHTTPEndpoints(t *testing.T) {
	// Создаём тестовый сервер
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/wallets/{uuid}":
			assert.Equal(t, http.MethodGet, r.Method)
		case "/api/v1/wallet":
			assert.Equal(t, http.MethodPost, r.Method)
		}
	}))
	defer ts.Close()

	// Тестируем GET запрос
	resp, err := http.Get(ts.URL + "/api/v1/wallets/test-uuid")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Тестируем POST запрос
	resp, err = http.Post(ts.URL+"/api/v1/wallet", "application/json", nil)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func setupTestServer(t *testing.T) (*httptest.Server, func(), string) {
	// Инициализация БД
	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_HOST"),
		os.Getenv("DB_PORT"),
		os.Getenv("DB_NAME"))

	database, err := db.NewPostgresConnection(dbURL)
	if err != nil {
		t.Fatalf("Ошибка подключения к БД: %v", err)
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%s:%s", os.Getenv("REDIS_HOST"), os.Getenv("REDIS_PORT")),
	})
	cacheClient := cache.NewRedisCache(redisClient)

	// Создаём обработчики
	walletHandler := handler.NewWalletHandler(database, cacheClient, false)

	// Настраиваем роутер
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/wallets/", walletHandler.GetWalletBalance)
	mux.HandleFunc("/api/v1/wallet", walletHandler.HandleWalletOperation)

	// Создаём тестовый сервер
	ts := httptest.NewServer(mux)

	// Возвращаем функцию очистки
	cleanup := func() {
		ts.Close()
		database.Close()
	}

	testWalletID := "a5007790-ed69-4ba2-96ef-c1b1b62d8cce"

	return ts, cleanup, testWalletID
}

func TestLoadPerformance(t *testing.T) {
	ts, cleanup, testWalletID := setupTestServer(t)
	defer cleanup()

	// Делаем начальный депозит и ждем его обработки
	depositReq := wallet.WalletRequest{
		WalletID:      testWalletID,
		OperationType: wallet.DEPOSIT,
		Amount:        1000.0,
	}

	depositBody, _ := json.Marshal(depositReq)
	resp, err := http.Post(
		fmt.Sprintf("%s/api/v1/wallet", ts.URL),
		"application/json",
		bytes.NewBuffer(depositBody),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	time.Sleep(500 * time.Millisecond)

	requestsPerSecond := 1000
	testDuration := 4 * time.Second

	// Создаем HTTP клиент с таймаутом
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        1000,
			MaxIdleConnsPerHost: 1000,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	start := time.Now()
	ticker := time.NewTicker(time.Second / time.Duration(requestsPerSecond))
	defer ticker.Stop()
	// Создаём буфер для ошибок
	errors := make(chan error, requestsPerSecond*5)
	done := make(chan bool)

	var wg sync.WaitGroup

	go func() {
		time.Sleep(testDuration)
		done <- true
	}()

	for {
		select {
		case <-done:
			wg.Wait() // ждем завершения всех запросов
			close(errors)

			var errCount int
			for err := range errors {
				errCount++
				t.Errorf("Ошибка запроса: %v", err)
			}

			duration := time.Since(start)
			actualRPS := float64(requestsPerSecond*5-errCount) / duration.Seconds()

			t.Logf("Тест производительности занял: %v", duration)
			t.Logf("Фактический RPS: %.2f", actualRPS)
			require.Equal(t, 0, errCount, "Тест должен быть без ошибок")
			require.GreaterOrEqual(t, actualRPS, float64(requestsPerSecond), "RPS должен быть не менее 1000")
			return

		case <-ticker.C:
			wg.Add(1)
			go func() {
				defer wg.Done()

				url := fmt.Sprintf("%s/api/v1/wallets/%s", ts.URL, testWalletID)
				resp, err := client.Get(url)
				if err != nil {
					errors <- fmt.Errorf("ошибка запроса: %v", err)
					return
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					errors <- fmt.Errorf("неверный статус код: %d", resp.StatusCode)
					return
				}

				var result interface{}
				if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
					errors <- fmt.Errorf("ошибка декодирования ответа: %v", err)
					return
				}
			}()
		}
	}
}

func TestDatabaseConnectionErrors(t *testing.T) {
	tests := []struct {
		name    string
		dbURL   string
		errText string
	}{
		{
			name:    "Неверный хост",
			dbURL:   "postgres://user:pass@nonexistent-host:5432/dbname?sslmode=disable",
			errText: "dial tcp: lookup nonexistent-host: no such host",
		},
		{
			name:    "Неверный порт",
			dbURL:   "postgres://user:pass@localhost:9999/dbname?sslmode=disable",
			errText: "dial tcp [::1]:9999: connectex: No connection could be made because the target machine actively refused it.",
		},
		{
			name:    "Неверные учетные данные",
			dbURL:   "postgres://wronguser:wrongpass@localhost:5432/dbname?sslmode=disable",
			errText: "pq: password authentication failed for user \"wronguser\"",
		},
		{
			name: "Неверное имя базы данных",
			dbURL: fmt.Sprintf("postgres://%s:%s@%s:%s/nonexistent_db?sslmode=disable",
				os.Getenv("DB_USER"),
				os.Getenv("DB_PASSWORD"),
				os.Getenv("DB_HOST"),
				os.Getenv("DB_PORT")),
			errText: "pq: database \"nonexistent_db\" does not exist",
		},
		{
			name:    "Некорректный формат URL",
			dbURL:   "incorrect://url",
			errText: "missing \"=\" after \"incorrect://url\" in connection info string\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := db.NewPostgresConnection(tt.dbURL)

			// Проверяем, что плучили ожидаемую ошибку
			assert.Error(t, err)
			expectedErr := tt.errText
			assert.Contains(t, err.Error(), expectedErr)

			if db != nil {
				db.Close()
			}
		})
	}
}

func TestEnvVariablesErrors(t *testing.T) {
	// Сохраняем оригинальные значения
	originalEnv := map[string]string{
		"DB_HOST":     os.Getenv("DB_HOST"),
		"DB_PORT":     os.Getenv("DB_PORT"),
		"DB_USER":     os.Getenv("DB_USER"),
		"DB_PASSWORD": os.Getenv("DB_PASSWORD"),
		"DB_NAME":     os.Getenv("DB_NAME"),
	}

	// Восстанавливаем в конце
	defer func() {
		for k, v := range originalEnv {
			os.Setenv(k, v)
		}
	}()

	tests := []struct {
		name          string
		envVars       map[string]string
		expectedError bool
	}{
		{
			name: "Отсутствует DB_HOST",
			envVars: map[string]string{
				"DB_HOST":     "",
				"DB_PORT":     "",
				"DB_USER":     "",
				"DB_PASSWORD": "",
				"DB_NAME":     "",
			},
			expectedError: true,
		},
		{
			name: "Отсутствует DB_PORT",
			envVars: map[string]string{
				"DB_HOST":     "localhost",
				"DB_PORT":     "",
				"DB_USER":     "",
				"DB_PASSWORD": "",
				"DB_NAME":     "",
			},
			expectedError: true,
		},
		{
			name: "Отсутствует DB_USER",
			envVars: map[string]string{
				"DB_HOST":     "localhost",
				"DB_PORT":     "5432",
				"DB_USER":     "",
				"DB_PASSWORD": "",
				"DB_NAME":     "",
			},
			expectedError: true,
		},
		{
			name: "Отсутствуют все переменные",
			envVars: map[string]string{
				"DB_HOST":     "",
				"DB_PORT":     "",
				"DB_USER":     "",
				"DB_PASSWORD": "",
				"DB_NAME":     "",
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Очищаем все переменные
			for k := range originalEnv {
				os.Unsetenv(k)
			}

			// Устанавливаем тестовые значения
			for k, v := range tt.envVars {
				os.Setenv(k, v)
			}

			// Формируем строку подключения
			dbURL := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
				os.Getenv("DB_USER"),
				os.Getenv("DB_PASSWORD"),
				os.Getenv("DB_HOST"),
				os.Getenv("DB_PORT"),
				os.Getenv("DB_NAME"))

			// Пытаемся установить соединение
			db, err := db.NewPostgresConnection(dbURL)

			if err != nil {
				t.Logf("Полученная ошибка: %v", err)
			}

			if tt.expectedError {
				assert.Error(t, err, ErrDBConnection)
			} else {
				assert.NoError(t, err, ErrDBConnection)
			}

			if db != nil {
				db.Close()
			}
		})
	}
}

func TestRedisConnection(t *testing.T) {
	redisHost := os.Getenv("REDIS_HOST")
	redisPort := os.Getenv("REDIS_PORT")

	if redisHost == "" {
		redisHost = "localhost"
	}
	if redisPort == "" {
		redisPort = "6379"
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%s:%s", redisHost, redisPort),
	})
	cache := cache.NewRedisCache(redisClient)
	ctx := context.Background()

	err := cache.Client().Ping(ctx).Err()
	assert.NoError(t, err, "Ошибка подключения к Redis")
}
