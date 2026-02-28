ifneq ("$(wildcard .env)","")
    include .env
    export $(shell sed 's/=.*//' .env)
endif

PORT ?= 8088

.PHONY: proto run clean help

proto:
	@echo "🛠 Генерация gRPC кода..."
	protoc --go_out=. --go-grpc_out=. proto/telegram.proto

run:
	@echo "🚀 Запуск gRPC сервера на порту $(PORT)..."
	@mkdir -p sessions
	TG_API_ID=$(TG_API_ID) TG_API_HASH=$(TG_API_HASH) PORT=$(PORT) go run cmd/server/main.go

clean:
	@echo "🧹 Очистка проекта..."
	rm -rf sessions/*.json
	rm -f grpcurl
	go clean

install:
	@echo "📦 Установка зависимостей..."
	go install  github.com/fullstorydev/grpcurl/cmd/grpcurl@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest   
	go mod tidy

help:
	@echo "Команды:"
	@echo "🛠 Генерация gRPC кода в internal/gen..."
	@mkdir -p internal/gen/telegram
	protoc --go_out=. --go-grpc_out=. proto/telegram.proto
	@echo "  make proto        - сгенерировать код из .proto"
	@echo "  make run          - запустить сервер"
	@echo "  make clean        - удалить сессии и очистить кэш"
	@echo "  make install-deps - установить grpcurl и зависимости"