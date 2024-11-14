package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"

	db "wallet/internal/db"
	wallet "wallet/internal/handler"
)

func main() {
	if err := godotenv.Load("../../.env"); err != nil {
		log.Printf("Ошибка загрузки .env файла: %v", err)
	}

	// Строка подключения к БД
	dbHost := os.Getenv("DB_HOST")
	dbPort := os.Getenv("DB_PORT")
	dbUser := os.Getenv("DB_USER")
	dbPass := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")

	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		dbUser, dbPass, dbHost, dbPort, dbName)

	database, err := db.NewPostgresConnection(dbURL)
	if err != nil {
		log.Fatalf("Ошибка подключения к БД: %v", err)
	}
	defer database.Close()
	// Инициализация обработчиков с подключением к БД
	walletHandler := wallet.NewWalletHandler(database)

	http.HandleFunc("/api/v1/wallets/{uuid}", walletHandler.GetWalletBalance)
	http.HandleFunc("/api/v1/wallet", walletHandler.HandleWalletOperation)

	log.Fatal(http.ListenAndServe(":8081", nil))
}
