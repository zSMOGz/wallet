package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/joho/godotenv"

	"wallet/internal/cache"
	db "wallet/internal/db"
	handler "wallet/internal/handler"
)

const (
	ErrWorkingDir   = "Ошибка получения рабочей директории: %v"
	ErrLoadEnvFile  = "Ошибка загрузки .env файла: %v"
	ErrDBConnection = "Ошибка подключения к БД: %v"
)

func main() {
	log.Println("Запуск сервера...")

	// Получаем путь к корню проекта
	_, filename, _, _ := runtime.Caller(0)
	projectRoot := filepath.Join(filepath.Dir(filename), "../..")
	if err := godotenv.Load(filepath.Join(projectRoot, "config.env")); err != nil {
		log.Printf(ErrLoadEnvFile, err)
	}
	log.Println("Конфигурация загружена")

	// Строка подключения к БД
	dbHost := os.Getenv("DB_HOST")
	dbPort := os.Getenv("DB_PORT")
	dbUser := os.Getenv("DB_USER")
	dbPass := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")

	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		dbUser, dbPass, dbHost, dbPort, dbName)
	log.Printf("Подключение к БД: %s", dbHost)

	database, err := db.NewPostgresConnection(dbURL)
	if err != nil {
		log.Fatalf(ErrDBConnection, err)
	}
	log.Println("БД подключена успешно")

	// Подключаемся к Redis
	redisHost := os.Getenv("REDIS_HOST")
	redisPort := os.Getenv("REDIS_PORT")
	log.Printf("Подключение к Redis: %s:%s", redisHost, redisPort)

	cache := cache.NewRedisCache(fmt.Sprintf("%s:%s", redisHost, redisPort))
	log.Println("Redis подключен успешно")

	debugMode := os.Getenv("DEBUG_MODE") == "true"

	// Инициализация обработчиков с подключением к БД и к Redis
	walletHandler := handler.NewWalletHandler(database, cache, debugMode)

	http.HandleFunc("/api/v1/wallets/{uuid}", walletHandler.GetWalletBalance)
	http.HandleFunc("/api/v1/wallet", walletHandler.HandleWalletOperation)

	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Сервер запущен на порту :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
