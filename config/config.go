package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
)

// структура конфига
type Config struct {
	ApiID   int
	ApiHash string
	Port    string
}

// конструктор конфига
func NewConfig(logger zerolog.Logger) *Config {
	if err := godotenv.Load(); err != nil {
		logger.Warn().Msg(".env file not exist")
	}

	idStr := os.Getenv("TG_API_ID")
	apiID, err := strconv.Atoi(idStr)
	if err != nil {
		logger.Fatal().Err(err).Str("input", idStr).Msg("TG_API_ID must be a valid number")
	}
	port := os.Getenv("PORT")
	if port != "" && !strings.HasPrefix(port, ":") {
		port = ":" + port
	} else if port == "" {
		port = ":8088"
	}

	return &Config{
		ApiID:   apiID,
		ApiHash: os.Getenv("TG_API_HASH"),
		Port:    port,
	}
}
