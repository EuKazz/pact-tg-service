package telegram

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/pact-tg-service/internal/interfaces"

	"github.com/rs/zerolog"
)

// структура менеджера
type Manager struct {
	mu        sync.RWMutex                 // защита мапы от паники при параллельных запросах
	workers   map[string]interfaces.Worker // хранилище всех активных подключений
	logger    zerolog.Logger
	apiID     int                                                                            // учетные данные ТГ
	apiHash   string                                                                         // учетные данные ТГ
	newWorker func(id string, apiID int, apiHash string, l zerolog.Logger) interfaces.Worker // ф-я для создания воркера
}

// конструктор
func NewManager(apiID int, apiHash string, logger zerolog.Logger) *Manager {
	return &Manager{
		workers: make(map[string]interfaces.Worker),
		logger:  logger,
		apiID:   apiID,
		apiHash: apiHash,
		newWorker: func(id string, apiID int, apiHash string, l zerolog.Logger) interfaces.Worker {
			return NewWorker(id, apiID, apiHash, l)
		},
	}
}

// метод для получения worker из мапы по ID
func (m *Manager) GetWorker(sessionID string) (interfaces.MessageSender, bool) {
	// блокировка менеджера для защиты от конкуретной работы с мапой
	m.mu.Lock()
	defer m.mu.Unlock()

	// поиск по мапе
	worker, ok := m.workers[sessionID]
	return worker, ok
}

// метод для создания новой сессии
func (m *Manager) CreateSession(ctx context.Context, sessionID string) (interfaces.MessageSender, string, error) {
	// защита от дубликатов подключений
	m.mu.Lock()
	if _, exists := m.workers[sessionID]; exists {
		m.mu.Unlock()
		return nil, "", fmt.Errorf("сессия %s уже активна", sessionID)
	}
	// логгер, который будет помечать логи уникальным ID сессии
	workerLogger := m.logger.With().Str("session_id", sessionID).Logger()
	// создаем воркера
	worker := m.newWorker(sessionID, m.apiID, m.apiHash, workerLogger)
	// создание канала для куар-кода
	worker.SetQRCodeChan(make(chan string))
	// записываем воркера в карту
	m.workers[sessionID] = worker

	m.mu.Unlock()
	// запуск воркера - он идет в ТГ и требует куар
	worker.Start()

	// ожидание QR
	select {
	// успех
	case qrURL, ok := <-worker.GetQRCodeChan():
		if !ok {
			return worker, "", nil
		}
		return worker, qrURL, nil
		// таумаут
	case <-time.After(time.Second * 15):
		m.DeleteSession(ctx, sessionID)
		return nil, "", fmt.Errorf("превышено время ожидания")
		// отмена клиента
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
	// удаляем ссылку на воркера из мапы
	delete(m.workers, sessionID)
	m.mu.Unlock()

	// logout and stop - деактивация сеанса в ТГ и закрытие соед
	err := worker.LogoutAndStop(ctx)

	// удаление сессии
	sessionPath := fmt.Sprintf("sessions/%s.json", sessionID)
	if _, err := os.Stat(sessionPath); err == nil {
		if errRem := os.Remove(sessionPath); errRem != nil {
			m.logger.Error().Err(errRem).Str("path", sessionPath).Msg("Error while deleting file")
		} else {
			m.logger.Info().Str("path", sessionPath).Msg("Файл сессии успешно удалена")
		}
	}
	return err
}
