package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	ApiID   int
	ApiHash string
	Port    string
}

func NewConfig() *Config {
	idStr := os.Getenv("TG_API_ID")
	apiID, err := strconv.Atoi(idStr)
	if err != nil {
		fmt.Printf("❌ ОШИБКА КОНФИГА: TG_API_ID '%s' не является числом!\n", idStr)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = ":8088"
	}

	return &Config{
		ApiID:   apiID,
		ApiHash: os.Getenv("TG_API_HASH"),
		Port:    port,
	}
}
