package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	qrlogin "github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/pact-tg-service/internal/interfaces"
	"github.com/rs/zerolog"
	"go.uber.org/zap"
)

// структура, кот управляет циклом одного соединения
type Worker struct {
	SessionID   string
	apiID       int
	apiHash     string
	client      interfaces.TelegramClient // интерфейс над библ gotd
	ctx         context.Context
	cancel      context.CancelFunc
	logger      zerolog.Logger
	MessageChan chan *tg.UpdateNewMessage // входящий поток сообщений из ТГ
	QRCodeChan  chan string               // канал для авторизации и получения куар кода
}

// конструктор
func NewWorker(sessionID string, apiID int, apiHash string, logger zerolog.Logger) *Worker {
	ctx, cancel := context.WithCancel(context.Background())
	msgChan := make(chan *tg.UpdateNewMessage, 100)
	qrChan := make(chan string, 1)
	z, _ := zap.NewDevelopment()
	sessionPath := fmt.Sprintf("sessions/%s.json", sessionID) // память воркера

	//  Сначала создаем воркер, чтобы использовать его внутри диспетчера
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

	//  Создаем диспетчер обновлений - ловит сообщения в стриме
	dispatcher := tg.NewUpdateDispatcher() // сортирует сообщ и берет только важные
	// ф-я, которая реагирует только на новое текстовое сообщение
	dispatcher.OnNewMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewMessage) error {
		w.logger.Debug().Msg("New message received")
		// Используем неблокирующую отправку, если канал переполнен - сработает дефолт
		select {
		case w.MessageChan <- u:
		default:
			w.logger.Warn().Msg("MessageChan is full, dropping message")
		}
		return nil
	})

	//  Настраиваем опции клиента (включая диспетчер)
	opts := telegram.Options{
		UpdateHandler: dispatcher, // клиенту уходит диспетчер для событий
		Logger:        z,
		SessionStorage: &session.FileStorage{ // сохранение на диск
			Path: sessionPath,
		},
	}

	//  Инициализ. настоящего клиента и сразу обернули его в структуру, в соотв с интерфейсом TelegramClient
	realClient := telegram.NewClient(apiID, apiHash, opts)
	w.client = clientWrapper{Client: realClient}

	return w
}

// обертка, чтобы встроить клиент из gotd
type clientWrapper struct {
	*telegram.Client
}

// Auth клиент библиотеки заворачивается в наш интерфейс
func (w clientWrapper) Auth() interfaces.AuthClient {
	return authWrapper{w.Client.Auth()}
}

// обертка для клиента авторизации
type authWrapper struct {
	*auth.Client
}

// вызов метода библиотеки для получения статуса
func (a authWrapper) Status(ctx context.Context) (any, error) {
	return a.Client.Status(ctx)
}

// структура-обертка для объекдинения авторизации через куар с механизмом обратного вызова - получаем токен и отдаем
type QRAuthWrapper struct {
	*qrlogin.QR
	OnCode func(ctx context.Context, token qrlogin.Token) error
}

// метод для запуска QR (запрос токена, вызов генерации и ожидание пока токен будет отсканирован)
func (q QRAuthWrapper) Authenticate(ctx context.Context) (auth.UserInfo, error) {
	// Аргументы: контекст, loggedIn (nil), show (наш колбэк)
	_, err := q.QR.Auth(ctx, nil, q.OnCode)
	return auth.UserInfo{}, err
}

// заглушка для телефона (требуется интерфейсом)
func (q QRAuthWrapper) Phone(ctx context.Context) (string, error) {
	return "", fmt.Errorf("QR-flow does not use phone")
}

// заглушка для пароля (2FA)
func (q QRAuthWrapper) Password(ctx context.Context) (string, error) {
	return "", fmt.Errorf("2FA password not supported in this flow yet")
}

// принятие правил (ToS)
func (q QRAuthWrapper) AcceptTermsOfService(ctx context.Context, tos tg.HelpTermsOfService) error {
	return nil
}

// заглушка для регистрации
func (q QRAuthWrapper) SignUp(ctx context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, fmt.Errorf("signup not supported")
}

// реализация CodeAuthenticator (метод Code)
func (q QRAuthWrapper) Code(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
	return "", fmt.Errorf("SMS code not used in QR flow")
}

// метод для получения ссобщений из канала
func (w *Worker) GetMessageChan() <-chan *tg.UpdateNewMessage {
	return w.MessageChan
}

// запуск конкретного воркера для поддержания связи с ТГ в фоновом режиме
func (w *Worker) Start() {
	// проверка для тестов, не пустой ли воркер, есть ли сетевое соед
	if w.client == nil {
		w.logger.Debug().Msg("Skipping worker start in tests")
		w.cancel()
		return
	}
	// воркер запускается в отдельной горутине
	go func() {
		// защита от паники
		defer func() {
			_ = recover()
		}()
		// гарантия закрытия каналов при выходе из горутины
		defer close(w.MessageChan)
		defer close(w.QRCodeChan)

		// создаем объект из библиотеки, который делает запросы на вход
		rawQR := qrlogin.NewQR(w.client.API(), w.apiID, w.apiHash, qrlogin.Options{})

		// Ловит куар из ТГ
		qrStrategy := QRAuthWrapper{
			QR: &rawQR,
			OnCode: func(ctx context.Context, token qrlogin.Token) error {
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

		// Открывается соед
		err := w.client.Run(w.ctx, func(ctx context.Context) error {
			// Проверка статуса авторизации
			statusAny, err := w.client.Auth().Status(ctx)
			if err != nil {
				w.logger.Error().Err(err).Msg("Не удалось проверить статус авторизации")
				return err
			}

			status, ok := statusAny.(*auth.Status)
			if !ok {
				return fmt.Errorf("неизвестный формат статуса авторизации")
			}

			if status.Authorized {
				w.logger.Info().Msg("Сессия уже активна, пропускаем вход")
			} else {
				w.logger.Info().Msg("Запуск прямой QR-авторизации...")
				_, err := qrStrategy.Authenticate(ctx)
				if err != nil {
					return fmt.Errorf("QR-авторизация не удалась: %w", err)
				}
			}

			w.logger.Info().Msg("Вход выполнен! Ожидание обновлений...")

			// Блокировка до отмены контекста (вызова Stop)
			<-ctx.Done()
			return ctx.Err()
		})

		// Логирование причины остановки
		if err != nil && err != context.Canceled {
			w.logger.Error().Err(err).Msg("Критическая ошибка в работе воркера")
		} else {
			w.logger.Info().Msg("Воркер успешно остановлен")
		}
	}()
}

// метод для отправки текста контакте или в чат через API Telegram
func (w *Worker) SendText(ctx context.Context, peerStr string, text string) (int64, error) {
	api := w.client.API()
	// адресат
	var peer tg.InputPeerClass
	// отправка себе
	if peerStr == "me" || peerStr == "" {
		self, err := w.client.Self(ctx)
		if err != nil {
			return 0, fmt.Errorf("failed to get self: %w", err)
		}
		peer = self.AsInputPeer()
	} else {
		username := strings.TrimPrefix(peerStr, "@")
		// поиск пользователя в сети
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

	// получение id сообщения из ответа Telegram
	switch u := sent.(type) {
	// короткое сообщение
	case *tg.UpdateShortSentMessage:
		return int64(u.ID), nil
		// массив обновлений
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

// остановка воркера
func (w *Worker) Stop() {
	w.logger.Warn().Msg("Остановка воркера")
	w.cancel()
}

// метод для прекращения сессии и выхода из аккаунта Telegram
func (w *Worker) LogoutAndStop(ctx context.Context) error {
	// проверка на пустой клиент
	if w.client == nil {
		w.logger.Debug().Msg("Skipping worker start in tests")
		if w.cancel != nil {
			w.cancel()
		}
		return nil
	}
	w.logger.Warn().Msg("Запрос выйти из аккаунта")
	// сетевой запрос в ТГ
	_, err := w.client.API().AuthLogOut(ctx)
	if err != nil {
		w.logger.Err(err).Msg("Ошибка при вызову AuthLogOut")
	}
	// завершение внутренних горутин воркера, закрываются сокеты и каналы
	w.Stop()
	return err
}

// получение доступа к каналу, куда воркер отправит куар
func (w *Worker) GetQRCodeChan() chan string {
	return w.QRCodeChan
}

// получение ссылки на канал
func (w *Worker) SetQRCodeChan(ch chan string) {
	w.QRCodeChan = ch
}
