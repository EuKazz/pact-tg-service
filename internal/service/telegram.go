package service

import (
	"context"
	"fmt"
	"time"

	"github.com/gotd/td/tg"
	tg_pb "github.com/pact-tg-service/internal/gen/telegram"
	"github.com/pact-tg-service/internal/telegram"
	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type TelegramGRPCService struct {
	tg_pb.UnimplementedTelegramServiceServer
	manager *telegram.Manager
	logger  zerolog.Logger
}

func NewTelegramGRPCService(manager *telegram.Manager, logger zerolog.Logger) *TelegramGRPCService {
	return &TelegramGRPCService{
		manager: manager,
		logger:  logger,
	}
}

// CreateSession - Создает сессию и возвращает QR-код
func (s *TelegramGRPCService) CreateSession(ctx context.Context, req *tg_pb.CreateSessionRequest) (*tg_pb.CreateSessionResponse, error) {
	// Генерируем уникальный ID для сессии (например, UUID или просто время)
	sessionID := fmt.Sprintf("sess_%d", time.Now().Unix())
	if sessionID == "sess_%!d(MISSING)" {
		sessionID = fmt.Sprintf("sess_%d", 1)
	} // Костыль для теста

	worker, qrURL, err := s.manager.CreateSession(ctx, sessionID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "ошибка создания сессии: %v", err)
	}

	return &tg_pb.CreateSessionResponse{
		SessionId: worker.SessionID,
		QrCode:    qrURL,
	}, nil
}

// SendMessage - Отправляет сообщение
func (s *TelegramGRPCService) SendMessage(ctx context.Context, req *tg_pb.SendMessageRequest) (*tg_pb.SendMessageResponse, error) {
	worker, ok := s.manager.GetWorker(req.SessionId)
	if !ok {
		return nil, status.Error(codes.NotFound, "сессия не найдена")
	}

	msgID, err := worker.SendText(ctx, req.Peer, req.Text)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "ошибка отправки: %v", err)
	}

	return &tg_pb.SendMessageResponse{MessageId: msgID}, nil
}

func (s *TelegramGRPCService) DeleteSession(ctx context.Context, req *tg_pb.DeleteSessionRequest) (*tg_pb.DeleteSessionResponse, error) {
	if req.SessionId == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id обязателен")
	}
	err := s.manager.DeleteSession(ctx, req.SessionId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "не удалось удалить эту сессию")
	}
	return &tg_pb.DeleteSessionResponse{}, nil
}

func (s *TelegramGRPCService) SubscribeMessages(req *tg_pb.SubscribeMessagesRequest, stream tg_pb.TelegramService_SubscribeMessagesServer) error {
	// 1. Ищем нужный воркер
	worker, ok := s.manager.GetWorker(req.SessionId)
	if !ok {
		return status.Errorf(codes.NotFound, "сессия %s не найдена", req.SessionId)
	}

	s.logger.Info().Str("session_id", req.SessionId).Msg("Клиент подписался на сообщения")

	// 2. Слушаем события
	for {
		select {
		// Если клиент gRPC закрыл соединение (нажал Stop или пропал интернет)
		case <-stream.Context().Done():
			s.logger.Debug().Msg("Стрим закрыт клиентом")
			return nil

		// Если в канал воркера пришло новое сообщение от Telegram
		case update, ok := <-worker.MessageChan:
			if !ok {
				s.logger.Warn().Msg("Канал воркера закрыт (воркер остановлен)")
				return status.Error(codes.Aborted, "соединение с Telegram разорвано")
			}

			// 3. Преобразуем внутренний тип tg.Message в наш gRPC MessageUpdate
			if m, ok := update.Message.(*tg.Message); ok {

				// Пытаемся достать ID отправителя (Peer)
				var fromID string
				if p, ok := m.PeerID.(*tg.PeerUser); ok {
					fromID = fmt.Sprintf("%d", p.UserID)
				}

				resp := &tg_pb.MessageUpdate{
					MessageId: int64(m.ID),
					From:      fromID,
					Text:      m.Message,
					Timestamp: int64(m.Date),
				}

				// 4. Отправляем в поток
				if err := stream.Send(resp); err != nil {
					s.logger.Error().Err(err).Msg("Ошибка отправки в стрим")
					return err
				}
			}
		}
	}
}
