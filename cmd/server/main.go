package main

import (
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/pact-tg-service/config"
	tg_pb "github.com/pact-tg-service/internal/gen/telegram"
	"github.com/pact-tg-service/internal/service"
	"github.com/pact-tg-service/internal/telegram"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	app()
}

func app() {
	// создание логера
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout}).With().Timestamp().Logger()
	logger.Info().Msg("Запуск gRPC сервиса Telegram Manager")

	// загрузка конфига
	cfg := config.NewConfig()
	// создание папки для сессий
	if err := os.MkdirAll("sessions", 0755); err != nil {
		logger.Fatal().Err(err).Msg("Не удалось создать папку для сессий")
	}

	// инициализация менеджера
	manager := telegram.NewManager(cfg.ApiID, cfg.ApiHash, logger)

	// настройка сервера
	grpcServer := grpc.NewServer()

	// регистрация сервиса
	tgHandler := service.NewTelegramGRPCService(manager, logger)
	tg_pb.RegisterTelegramServiceServer(grpcServer, tgHandler)

	// включение reflection
	reflection.Register(grpcServer)

	// запуск Listener
	lis, err := net.Listen("tcp", cfg.Port)
	if err != nil {
		logger.Fatal().Err(err).Msg("Ошибка запуска TCP listener")
	}

	// graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		logger.Warn().Msg("Остановка сервера")
		grpcServer.GracefulStop()
	}()

	logger.Info().Msgf("gRPC сервер прослушивает порт %s", cfg.Port)
	if err := grpcServer.Serve(lis); err != nil {
		logger.Fatal().Err(err).Msg("Произошла ошибка в работе сервера")
	}
}
