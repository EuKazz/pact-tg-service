package interfaces

import (
	"context"

	"github.com/gotd/td/tg"
)

// оттправитель сообщений
type MessageSender interface {
	SendText(ctx context.Context, peer, text string) (int64, error)
	GetMessageChan() <-chan *tg.UpdateNewMessage
}

// менеджер сессий
type SessionManager interface {
	CreateSession(ctx context.Context, sessionID string) (MessageSender, string, error)
	DeleteSession(ctx context.Context, sessionID string) error
	GetWorker(sessionID string) (MessageSender, bool)
}

// рабочий
type Worker interface {
	MessageSender
	Start()
	GetQRCodeChan() chan string
	SetQRCodeChan(chan string)
	LogoutAndStop(ctx context.Context) error
}

// клиент авторизации для проверки состояния: залогинен или требуется QR
type AuthClient interface {
	Status(ctx context.Context) (any, error)
}

// адаптер, который позволяет тестировать код, не подключаясь к Telegram
type TelegramClient interface {
	Run(ctx context.Context, f func(ctx context.Context) error) error
	API() *tg.Client
	Auth() AuthClient
	Self(ctx context.Context) (*tg.User, error)
}
