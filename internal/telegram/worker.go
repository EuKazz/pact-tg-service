package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	qrlogin "github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/rs/zerolog"
	"go.uber.org/zap"
)

// управляет циклом одного соединения
type Worker struct {
	SessionID   string
	apiID       int
	apiHash     string
	client      *telegram.Client
	ctx         context.Context
	cancel      context.CancelFunc
	logger      zerolog.Logger
	MessageChan chan *tg.UpdateNewMessage
	QRCodeChan  chan string
}

func NewWorker(sessionID string, apiID int, apiHash string, logger zerolog.Logger) *Worker {
	ctx, cancel := context.WithCancel(context.Background())
	msgChan := make(chan *tg.UpdateNewMessage, 100)
	qrChan := make(chan string, 1)
	z, _ := zap.NewDevelopment()
	sessionPath := fmt.Sprintf("sessions/%s.json", sessionID)

	w := &Worker{
		SessionID:   sessionID,
		apiID:       apiID,
		apiHash:     apiHash,
		ctx:         ctx,
		cancel:      cancel,
		logger:      logger,
		MessageChan: msgChan,
		QRCodeChan:  qrChan,
	}

	// диспетчер обновлений
	dispatcher := tg.NewUpdateDispatcher()
	dispatcher.OnNewMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewMessage) error {
		w.logger.Debug().Msg("New message received")
		w.MessageChan <- u
		return nil
	})

	// инициализация клиента gotd
	w.client = telegram.NewClient(apiID, apiHash, telegram.Options{
		UpdateHandler: dispatcher,
		Logger:        z,
		SessionStorage: &telegram.FileSessionStorage{
			Path: sessionPath,
		},
	})
	return w
}

type QRAuthWrapper struct {
	*qrlogin.QR
	OnCode func(ctx context.Context, token qrlogin.Token) error
}

// 1. Метод для запуска QR (мы выяснили его сигнатуру из прошлой ошибки)
func (q QRAuthWrapper) Authenticate(ctx context.Context) (auth.UserInfo, error) {
	// Аргументы: контекст, loggedIn (nil), show (наш колбэк)
	_, err := q.QR.Auth(ctx, nil, q.OnCode)
	return auth.UserInfo{}, err
}

// 2. Заглушка для телефона (требуется интерфейсом)
func (q QRAuthWrapper) Phone(ctx context.Context) (string, error) {
	return "", fmt.Errorf("QR-flow does not use phone")
}

// 3. Заглушка для пароля (2FA)
func (q QRAuthWrapper) Password(ctx context.Context) (string, error) {
	return "", fmt.Errorf("2FA password not supported in this flow yet")
}

// 4. Принятие правил (ToS)
func (q QRAuthWrapper) AcceptTermsOfService(ctx context.Context, tos tg.HelpTermsOfService) error {
	return nil
}

// 5. Заглушка для регистрации
func (q QRAuthWrapper) SignUp(ctx context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, fmt.Errorf("signup not supported")
}

// 6. Реализация CodeAuthenticator (метод Code)
func (q QRAuthWrapper) Code(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
	return "", fmt.Errorf("SMS code not used in QR flow")
}

func (w *Worker) Start() {
	go func() {
		// Гарантируем закрытие канала при выходе из горутины
		defer close(w.MessageChan)
		defer func() {
			_ = recover()
		}()
		defer close(w.QRCodeChan)

		// 1. Создаем стратегию QR
		rawQR := qrlogin.NewQR(w.client.API(), w.apiID, w.apiHash, qrlogin.Options{})

		// 2. Оборачиваем её в наш хелпер (который мы написали ранее с методом AcceptTermsOfService)
		qrStrategy := QRAuthWrapper{
			QR: &rawQR,
			OnCode: func(ctx context.Context, token qrlogin.Token) error {
				// Отправляем URL (tg://login?...) в канал для gRPC
				select {
				case w.QRCodeChan <- token.URL():
					w.logger.Info().Str("url", token.URL()).Msg("QR-код сгенерирован")
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Second * 5):
					w.logger.Warn().Msg("Канал QR-кода переполнен или не читается")
				}
				return nil
			},
		}

		w.logger.Info().Msg("Запуск соединения с Telegram...")

		err := w.client.Run(w.ctx, func(ctx context.Context) error {
			// 1. Проверяем статус: если уже авторизованы (через файл), просто идем дальше
			status, _ := w.client.Auth().Status(ctx)
			if status.Authorized {
				w.logger.Info().Msg("Сессия уже активна, пропускаем вход")
				// Закрываем канал, чтобы менеджер не ждал QR
				select {
				case <-w.QRCodeChan:
				default:
				}
				close(w.QRCodeChan)
			} else {
				w.logger.Info().Msg("Запуск прямой QR-авторизации...")

				// 2. ВЫЗЫВАЕМ НАПРЯМУЮ: Твой метод, который делает q.QR.Auth(...)
				// Это не даст библиотеке уйти в запрос номера телефона
				_, err := qrStrategy.Authenticate(ctx)
				if err != nil {
					return fmt.Errorf("QR-авторизация не удалась: %w", err)
				}
			}

			w.logger.Info().Msg("Вход выполнен! Ожидание обновлений...")

			// Блокируем до вызова Stop()
			<-ctx.Done()
			return ctx.Err()
		})
		// Логируем причину остановки
		if err != nil && err != context.Canceled {
			w.logger.Error().Err(err).Msg("Критическая ошибка в работе воркера")
		} else {
			w.logger.Info().Msg("Воркер успешно остановлен")
		}
	}()
}

func (w *Worker) SendText(ctx context.Context, peerStr string, text string) (int64, error) {
	api := w.client.API()

	var peer tg.InputPeerClass
	if peerStr == "me" || peerStr == "" {
		self, err := w.client.Self(ctx)
		if err != nil {
			return 0, fmt.Errorf("failed to get self: %w", err)
		}
		peer = self.AsInputPeer()
	} else {
		username := strings.TrimPrefix(peerStr, "@")
		res, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
			Username: username,
		})
		if err != nil {
			return 0, fmt.Errorf("failed to resolve username %s: %w", username, err)
		}
		if len(res.Users) == 0 {
			return 0, fmt.Errorf("user %s not found", username)
		}

		user, ok := res.Users[0].(*tg.User)
		if !ok {
			return 0, fmt.Errorf("resolved peer is not a user")
		}
		peer = user.AsInputPeer()
	}
	// отправка
	sent, err := api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  text,
		RandomID: time.Now().UnixNano(),
	})
	if err != nil {
		return 0, err
	}

	// Получение id сообщения из ответа Telegram
	switch u := sent.(type) {
	case *tg.UpdateShortSentMessage:
		return int64(u.ID), nil // Приводим к int64
	case *tg.Updates:
		for _, upd := range u.Updates {
			if m, ok := upd.(*tg.UpdateNewMessage); ok {
				if msg, ok := m.Message.(*tg.Message); ok {
					return int64(msg.ID), nil
				}
			}
		}
	}

	return 0, nil
}

func (w *Worker) Stop() {
	w.logger.Warn().Msg("Остановка воркера")
	w.cancel()
	// Канал закроем чуть позже, чтобы не было паники при записи в закрытый канал
}

func (w *Worker) LogoutAndStop(ctx context.Context) error {
	w.logger.Warn().Msg("Запрос выйти из аккаунта")
	_, err := w.client.API().AuthLogOut(ctx)
	if err != nil {
		w.logger.Err(err).Msg("Ошибка при вызову AuthLogOut")
	}
	w.Stop()
	return err
}
