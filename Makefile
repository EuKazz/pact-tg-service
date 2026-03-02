ifneq ("$(wildcard .env)","")
    include .env
    export $(shell sed 's/=.*//' .env)
endif

PORT ?= 8088

.PHONY: proto run clean help install test test-cover test-html

proto:
	@echo "Генерация gRPC кода..."
	protoc --go_out=. --go-grpc_out=. proto/telegram.proto

run:
	@echo "Запуск gRPC сервера на порту $(PORT)..."
	@mkdir -p sessions
	TG_API_ID=$(TG_API_ID) TG_API_HASH=$(TG_API_HASH) PORT=$(PORT) go run cmd/server/main.go

test:
	@echo "Запуск всех тестов..."
	go test -v ./internal/telegram/...

test-cover:
	@echo "Генерация отчета о покрытии..."
	go test -coverprofile=coverage.out ./internal/telegram/...
	go tool cover -func=coverage.out

clean:
	@echo "Очистка проекта..."
	rm -rf sessions/*.json
	rm -f grpcurl
	rm -f coverage.out
	go clean

install:
	@echo "Установка зависимостей..."
	go install  github.com/fullstorydev/grpcurl/cmd/grpcurl@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest   
	go mod tidy

help:
	@echo "Команды:"
	@echo "  make proto        - сгенерировать код из .proto"
	@echo "  make run          - запустить сервер"
	@echo "  make test         - запустить тесты"
	@echo "  make test-cover   - показать покрытие в консоли"
	@echo "  make test-html    - открыть детальное покрытие в браузере"
	@echo "  make clean        - удалить сессии и очистить кэш"
	@echo "  make install-deps - установить grpcurl и зависимости"