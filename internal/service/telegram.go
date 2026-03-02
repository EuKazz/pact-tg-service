package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/gotd/td/tg"
	tg_pb "github.com/pact-tg-service/internal/gen/telegram"
	"github.com/pact-tg-service/internal/interfaces"
	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// структура ядра gRPC сервера, связ сетевые запросы с бизнес-логикой и логированием
type TelegramGRPCService struct {
	tg_pb.UnimplementedTelegramServiceServer // заглушка для зищаиты от падений при обновлении интерфейсов
	manager                                  interfaces.SessionManager
	logger                                   zerolog.Logger
}

// конструктор
func NewTelegramGRPCService(manager interfaces.SessionManager, logger zerolog.Logger) *TelegramGRPCService {
	return &TelegramGRPCService{
		manager: manager,
		logger:  logger,
	}
}

// CreateSession - gRPC - хэндлер. Создает сессию и возвращает QR-код
func (s *TelegramGRPCService) CreateSession(ctx context.Context, req *tg_pb.CreateSessionRequest) (*tg_pb.CreateSessionResponse, error) {
	// Ген-я уникального ID для сессии
	sessionID := "sess_" + uuid.New().String()
	// Передает зачдау менеджеру создать подкл к ТГ
	_, qrURL, err := s.manager.CreateSession(ctx, sessionID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "ошибка создания сессии: %v", err)
	}
	// менеджер запускает воркер, тот свяжется с ТГ и получит ссылку для входа - результат упакован и сген protobuf структуру
	return &tg_pb.CreateSessionResponse{
		SessionId: sessionID,
		QrCode:    qrURL,
	}, nil
}

// SendMessage - Отправляет сообщение
func (s *TelegramGRPCService) SendMessage(ctx context.Context, req *tg_pb.SendMessageRequest) (*tg_pb.SendMessageResponse, error) {
	// поиск воркера по ID
	worker, ok := s.manager.GetWorker(req.SessionId)
	if !ok {
		return nil, status.Error(codes.NotFound, "сессия не найдена")
	}
	// воркер превращает username в ID и ждет ответа от серверов ТГ
	msgID, err := worker.SendText(ctx, req.Peer, req.Text)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "ошибка отправки: %v", err)
	}
	// возвращается ID сообщения, которое было присвоено ТГ
	return &tg_pb.SendMessageResponse{MessageId: msgID}, nil
}

// удаление сессии- безопасное завершение работы аккаунта
func (s *TelegramGRPCService) DeleteSession(ctx context.Context, req *tg_pb.DeleteSessionRequest) (*tg_pb.DeleteSessionResponse, error) {
	if req.SessionId == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id обязателен")
	}
	// задача удаления уходит менеджеру - от найдет воркера и отдаст команду разлогиниться и разорвать соед
	err := s.manager.DeleteSession(ctx, req.SessionId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "не удалось удалить эту сессию")
	}
	// по требованию protobuc возвращается пустой ответ
	return &tg_pb.DeleteSessionResponse{}, nil
}

// подписка на входящие сообщения через живой канал, по которому сервер может бесконечно слать данные
func (s *TelegramGRPCService) SubscribeMessages(req *tg_pb.SubscribeMessagesRequest, stream tg_pb.TelegramService_SubscribeMessagesServer) error {
	// поиска активного воркера
	worker, ok := s.manager.GetWorker(req.SessionId)
	if !ok {
		return status.Errorf(codes.NotFound, "сессия %s не найдена", req.SessionId)
	}

	s.logger.Info().Str("session_id", req.SessionId).Msg("Клиент подписался на сообщения")

	// прослушивание событий в бесконечном цикле
	for {
		select {
		// если соединение закрылось или выключился инт-т
		case <-stream.Context().Done():
			s.logger.Debug().Msg("Стрим закрыт клиентом")
			return nil

		// если в канал воркера пришло новое сообщение
		case update, ok := <-worker.GetMessageChan():
			if !ok {
				s.logger.Warn().Msg("Канал воркера закрыт (воркер остановлен)")
				return status.Error(codes.Aborted, "соединение с Telegram разорвано")
			}

			// проверка является ли обновление текстовым сообщением
			if m, ok := update.Message.(*tg.Message); ok {

				// получение ID отправителя (Peer)
				var fromID string
				if p, ok := m.PeerID.(*tg.PeerUser); ok {
					fromID = fmt.Sprintf("%d", p.UserID)
				}
				// создание gRPC ответа из структур tg.Message в структуру proto файла
				resp := &tg_pb.MessageUpdate{
					MessageId: int64(m.ID),
					From:      fromID,
					Text:      m.Message,
					Timestamp: int64(m.Date),
				}

				//  отправка байтов клиенту
				if err := stream.Send(resp); err != nil {
					s.logger.Error().Err(err).Msg("Ошибка отправки в стрим")
					return err
				}
			}
		}
	}
}
