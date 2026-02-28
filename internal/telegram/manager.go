package telegram

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

type Manager struct {
	mu      sync.RWMutex
	workers map[string]*Worker
	logger  zerolog.Logger
	apiID   int
	apiHash string
}

func NewManager(apiID int, apiHash string, logger zerolog.Logger) *Manager {
	return &Manager{
		workers: make(map[string]*Worker),
		logger:  logger,
		apiID:   apiID,
		apiHash: apiHash,
	}
}

func (m *Manager) GetWorker(sessionID string) (*Worker, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	worker, ok := m.workers[sessionID]
	return worker, ok
}

func (m *Manager) CreateSession(ctx context.Context, sessionID string) (*Worker, string, error) {
	m.mu.Lock()

	if _, exists := m.workers[sessionID]; exists {
		return nil, "", fmt.Errorf("сессия %s уже активна", sessionID)
	}

	workerLogger := m.logger.With().Str("session_id", sessionID).Logger()
	worker := NewWorker(sessionID, m.apiID, m.apiHash, workerLogger)

	worker.QRCodeChan = make(chan string)

	m.workers[sessionID] = worker

	defer m.mu.Unlock()

	worker.Start()

	// ожидание QR
	select {
	case qrURL, ok := <-worker.QRCodeChan:
		if !ok {
			return worker, "", nil
		}
		return worker, qrURL, nil
	case <-time.After(time.Second * 15):
		m.DeleteSession(ctx, sessionID)
		return nil, "", fmt.Errorf("превышено время ожидания")
	case <-ctx.Done():
		m.DeleteSession(ctx, sessionID)
		return nil, "", ctx.Err()
	}
}

// метод для очистки ресурсов и удаления воркера из памяти
func (m *Manager) DeleteSession(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	worker, ok := m.workers[sessionID]
	if !ok {
		defer m.mu.Unlock()
		return fmt.Errorf("сессия не найдена %s", sessionID)
	}

	delete(m.workers, sessionID)
	m.mu.Unlock()

	// logout and stop
	err := worker.LogoutAndStop(ctx)

	// удаление сессии
	sessionPath := fmt.Sprintf("sessions/%s.json", sessionID)
	if _, err := os.Stat(sessionPath); err != nil {
		_ = os.Remove(sessionPath)
		m.logger.Info().Str("path", sessionPath).Msg("Файл сессии успешно удалена")
	}
	return err
}
