package telegram

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/pact-tg-service/internal/interfaces"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockWorker struct {
	mock.Mock
}

func TestGetWorker(t *testing.T) {
	m := &Manager{
		workers: make(map[string]interfaces.Worker),
	}

	sessionID := "test_session_234"
	testWorker := &Worker{SessionID: sessionID}

	t.Run("Worker exists", func(t *testing.T) {
		m.mu.Lock()
		m.workers[sessionID] = testWorker
		m.mu.Unlock()

		worker, ok := m.GetWorker(sessionID)

		assert.True(t, ok)
		assert.NotNil(t, worker)
		assert.Equal(t, sessionID, worker.(*Worker).SessionID)
	})

	t.Run("Worker not found", func(t *testing.T) {
		worker, ok := m.GetWorker("non_exist_id")

		assert.False(t, ok)
		assert.Nil(t, worker)
	})
}

func TestCreateSession(t *testing.T) {
	logger := zerolog.Nop()

	setupManager := func() *Manager {
		m := &Manager{
			workers: make(map[string]interfaces.Worker),
			apiID:   123,
			apiHash: "hash",
			logger:  logger,
		}

		m.newWorker = func(id string, apiID int, apiHash string, l zerolog.Logger) interfaces.Worker {
			ctx, cancel := context.WithCancel(context.Background())
			return &Worker{
				SessionID:   id,
				QRCodeChan:  make(chan string, 1),
				MessageChan: make(chan *tg.UpdateNewMessage, 1),
				ctx:         ctx,
				cancel:      cancel,
				logger:      l,
			}
		}
		return m
	}

	t.Run("Successfully got QR", func(t *testing.T) {
		m := setupManager()
		sessionID := "new_sess"

		type result struct {
			w   interfaces.MessageSender
			qr  string
			err error
		}
		resChan := make(chan result)

		go func() {
			w, qr, err := m.CreateSession(context.Background(), sessionID)
			resChan <- result{w, qr, err}
		}()

		var worker interfaces.MessageSender
		var ok bool
		for i := 0; i < 50; i++ {
			worker, ok = m.GetWorker(sessionID)
			if ok {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		assert.True(t, ok, "Worker should be in map")
		if ok {
			worker.(interfaces.Worker).GetQRCodeChan() <- "https://test-qr-link.com"
		}

		select {
		case res := <-resChan:
			assert.NoError(t, res.err)
			assert.Equal(t, "https://test-qr-link.com", res.qr)
			assert.NotNil(t, res.w)
		case <-time.After(1 * time.Second):
			t.Fatal("Didnt get response from CreateSession")
		}
	})

	t.Run("This ID already exists", func(t *testing.T) {
		m := setupManager()
		id := "same_id"

		m.mu.Lock()
		m.workers[id] = &Worker{SessionID: id}
		m.mu.Unlock()

		_, _, err := m.CreateSession(context.Background(), id)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "уже активна")
	})

	t.Run("Timeout waiting for QR", func(t *testing.T) {
		m := setupManager()
		id := "timeout"

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		_, qr, err := m.CreateSession(ctx, id)

		assert.Error(t, err)
		assert.Empty(t, qr)

		m.mu.Lock()
		_, exists := m.workers[id]
		m.mu.Unlock()
		assert.False(t, exists, "Worker should be deleted, timeout")
	})
}

func (m *MockWorker) LogoutAndStop(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockWorker) GetMessageChan() <-chan *tg.UpdateNewMessage {
	args := m.Called()

	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(<-chan *tg.UpdateNewMessage)
}

func (m *MockWorker) Start() {
	m.Called()
}

func (m *MockWorker) GetQRCodeChan() chan string {
	return nil
}

func (m *MockWorker) SetQRCodeChan(ch chan string) {}

func (m *MockWorker) GetSessionID() string {
	return ""
}

func (m *MockWorker) SendText(ctx context.Context, p, t string) (int64, error) {
	return 0, nil
}

func TestDeleteSession(t *testing.T) {
	logger := zerolog.Nop()
	ctx := context.Background()

	setupManager := func() *Manager {
		return &Manager{
			workers: make(map[string]interfaces.Worker),
			logger:  logger,
		}
	}
	t.Run("Successfully deleted", func(t *testing.T) {
		m := setupManager()
		sessionID := "to_delete"

		mockWrk := new(MockWorker)
		mockWrk.On("LogoutAndStop", ctx).Return(nil).Once()

		m.mu.Lock()
		m.workers[sessionID] = mockWrk
		m.mu.Unlock()

		sessionPath := fmt.Sprintf("sessions/%s.json", sessionID)
		_ = os.MkdirAll("sessions", 0755)
		_ = os.WriteFile(sessionPath, []byte("test"), 0644)

		err := m.DeleteSession(ctx, sessionID)

		assert.NoError(t, err)

		_, exists := m.GetWorker(sessionID)
		assert.False(t, exists, "Worker should be deleted")

		_, statErr := os.Stat(sessionPath)
		assert.True(t, os.IsNotExist(statErr), "Session file deleted")

		mockWrk.AssertExpectations(t)
	})

	t.Run("Session not found", func(t *testing.T) {
		m := setupManager()
		err := m.DeleteSession(ctx, "unknown_id")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "сессия не найдена")
	})
}
