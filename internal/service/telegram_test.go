package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
	tg_pb "github.com/pact-tg-service/internal/gen/telegram"
	"github.com/pact-tg-service/internal/interfaces"
	"github.com/pact-tg-service/internal/telegram"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type MockSessionManager struct {
	mock.Mock
}

type MockWorker struct {
	mock.Mock
}

type MockStream struct {
	mock.Mock
	tg_pb.TelegramService_SubscribeMessagesServer
}

// метод для проверки, есть ли соед с клиентом
func (m *MockStream) Context() context.Context {
	args := m.Called()
	return args.Get(0).(context.Context)
}

// имитация отправки данных к клиенту от сервера
func (m *MockStream) Send(resp *tg_pb.MessageUpdate) error {
	args := m.Called(resp)
	return args.Error(0)
}

// имитация отправки текста юзеру
func (m *MockWorker) SendText(ctx context.Context, peer, text string) (int64, error) {
	args := m.Called(ctx, peer, text)
	return int64(args.Int(0)), args.Error(1)
}

func (m *MockWorker) GetMessageChan() <-chan *tg.UpdateNewMessage {
	args := m.Called()
	return args.Get(0).(<-chan *tg.UpdateNewMessage)
}

// имитация создания сессии
func (m *MockSessionManager) CreateSession(ctx context.Context, sessionID string) (interfaces.MessageSender, string, error) {
	args := m.Called(ctx, sessionID)

	var w interfaces.MessageSender

	if args.Get(0) != nil {
		w = args.Get(0).(*telegram.Worker) // приведение типа
	}

	return w, args.String(1), args.Error(2)
}

// имитация поиска воркера
func (m *MockSessionManager) GetWorker(sessionID string) (interfaces.MessageSender, bool) {
	args := m.Called(sessionID)

	var w interfaces.MessageSender

	if args.Get(0) != nil {
		w = args.Get(0).(interfaces.MessageSender)
	}

	return w, args.Bool(1)
}

// имитация удаления сессии
func (m *MockSessionManager) DeleteSession(ctx context.Context, sessionID string) error {
	args := m.Called(ctx, sessionID)

	return args.Error(0)
}

func TestCreateSession_Success(t *testing.T) {
	mockMgr := new(MockSessionManager)
	logger := zerolog.Nop()
	svc := NewTelegramGRPCService(mockMgr, logger)

	ctx := context.Background()
	req := &tg_pb.CreateSessionRequest{}

	expectedWorker := &telegram.Worker{SessionID: "generated-uuid-123"}
	expectedQR := "https://t.me"

	mockMgr.On("CreateSession", ctx, mock.MatchedBy(func(id string) bool {
		return strings.HasPrefix(id, "sess_") && len(id) > 5
	})).Return(expectedWorker, expectedQR, nil).Once()

	resp, err := svc.CreateSession(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "generated-uuid-123", resp.SessionId)
	assert.Equal(t, expectedQR, resp.QrCode)

	mockMgr.AssertExpectations(t)
}

func TestCreateSession_Error(t *testing.T) {
	mockMgr := new(MockSessionManager)
	logger := zerolog.Nop()
	svc := NewTelegramGRPCService(mockMgr, logger)

	mockMgr.On("CreateSession", mock.Anything, mock.Anything).
		Return((*telegram.Worker)(nil), "", errors.New("internal telegram error")).Once()

	resp, err := svc.CreateSession(context.Background(), &tg_pb.CreateSessionRequest{})

	assert.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "ошибка создания сессии")
}

func TestDeleteSession(t *testing.T) {
	ctx := context.Background()

	t.Run("Удалено успешно", func(t *testing.T) {
		mockMgr := new(MockSessionManager)
		logger := zerolog.Nop()
		svc := NewTelegramGRPCService(mockMgr, logger)
		sessionId := "sess_123"

		mockMgr.On("DeleteSession", ctx, sessionId).Return(nil).Once()
		resp, err := svc.DeleteSession(ctx, &tg_pb.DeleteSessionRequest{SessionId: sessionId})
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		mockMgr.AssertExpectations(t)
	})

	t.Run("Возникла ошибка при удалении", func(t *testing.T) {
		mockMgr := new(MockSessionManager)
		logger := zerolog.Nop()
		svc := NewTelegramGRPCService(mockMgr, logger)
		sessionId := "sess_error"

		mockMgr.On("DeleteSession", ctx, sessionId).Return(errors.New("db_error")).Once()
		resp, err := svc.DeleteSession(ctx, &tg_pb.DeleteSessionRequest{SessionId: sessionId})

		assert.Error(t, err)
		assert.Nil(t, resp)

		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
		assert.Contains(t, st.Message(), "не удалось удалить")
	})
}

func TestGetWorker(t *testing.T) {
	mockMgr := new(MockSessionManager)
	logger := zerolog.Nop()
	svc := NewTelegramGRPCService(mockMgr, logger)
	sessionId := "sess_123"

	t.Run("Worker found", func(t *testing.T) {
		expectedWorker := &telegram.Worker{
			SessionID: sessionId,
		}
		mockMgr.On("GetWorker", sessionId).Return(expectedWorker, true).Once()
		worker, ok := svc.manager.GetWorker(sessionId)
		assert.True(t, ok)
		assert.NotNil(t, worker)
		realWorker := worker.(*telegram.Worker)
		assert.Equal(t, sessionId, realWorker.SessionID)
		mockMgr.AssertExpectations(t)
	})

	t.Run("Worker not found", func(t *testing.T) {
		mockMgr.On("GetWorker", "unknown").Return((*telegram.Worker)(nil), false).Once()
		worker, ok := svc.manager.GetWorker("unknown")

		assert.False(t, ok)
		assert.Nil(t, worker)
		mockMgr.AssertExpectations(t)
	})
}

func TestSendMessage(t *testing.T) {
	ctx := context.Background()
	req := &tg_pb.SendMessageRequest{
		SessionId: "sess_123",
		Peer:      "@testuser",
		Text:      "Test message",
	}

	t.Run("Успешная отправка", func(t *testing.T) {
		mockMgr := new(MockSessionManager)
		mockWrk := new(MockWorker)
		svc := NewTelegramGRPCService(mockMgr, zerolog.Nop())

		mockMgr.On("GetWorker", "sess_123").Return(mockWrk, true).Once()

		mockWrk.On("SendText", ctx, "@testuser", "Test message").Return(555, nil).Once()

		resp, err := svc.SendMessage(ctx, req)

		assert.NoError(t, err)
		assert.Equal(t, int64(555), resp.MessageId)
		mockMgr.AssertExpectations(t)
		mockWrk.AssertExpectations(t)
	})

	t.Run("Сессия не найден", func(t *testing.T) {
		mockMgr := new(MockSessionManager)
		svc := NewTelegramGRPCService(mockMgr, zerolog.Nop())

		mockMgr.On("GetWorker", "sess_123").Return(nil, false).Once()

		resp, err := svc.SendMessage(ctx, req)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}

func TestSubscribeMessages_Success(t *testing.T) {
	mockMgr := new(MockSessionManager)
	mockWrk := new(MockWorker)
	mockStr := new(MockStream)
	svc := NewTelegramGRPCService(mockMgr, zerolog.Nop())

	sessionID := "stream_test_123"
	msgChan := make(chan *tg.UpdateNewMessage, 1)

	ctx, cancel := context.WithCancel(context.Background())

	mockMgr.On("GetWorker", sessionID).Return(mockWrk, true)
	mockWrk.On("GetMessageChan").Return((<-chan *tg.UpdateNewMessage)(msgChan))
	mockStr.On("Context").Return(ctx)

	mockStr.On("Send", mock.MatchedBy(func(resp *tg_pb.MessageUpdate) bool {
		return resp.Text == "Hello from Telegram"
	})).Return(nil).Run(func(args mock.Arguments) {
		cancel()
	})

	msgChan <- &tg.UpdateNewMessage{
		Message: &tg.Message{Message: "Hello from Telegram"},
	}

	err := svc.SubscribeMessages(&tg_pb.SubscribeMessagesRequest{SessionId: sessionID}, mockStr)

	assert.NoError(t, err)
	mockMgr.AssertExpectations(t)
	mockWrk.AssertExpectations(t)
	mockStr.AssertExpectations(t)
}
