#Telegram Service (gRPC)
    Микросервис на Go, который устанавливает и поддерживает несколько независимых соединений с Telegram через библиотеку gotd.

#1. Требования
    • Тулчейн: Go 1.26 (последняя версия)
    • Telegram клиент: github.com/gotd/td
    • API: gRPC, Protocol Buffers (.proto)
    • Хранение состояния в памяти 
    • Конфигурация: через переменные окружения
    • Логирование - zerolog

#2. Настройка окружения
    Создайте .env в корне проекта:
    TG_API_ID=ваш_id
    TG_API_HASH=ваш_hash
    PORT=8088

#3. Запуск проекта:
    #Скачивание инструментов
        make install
    #Генерация gRPC кода
        make proto
    #Запуск сервера
        make run

#4. Пример вызова API 
    • Создание сессии и получение ссылки для QR, генерация кода по ссылке и сканирование в приложении Telegram
    grpcurl -plaintext -d '{}' localhost:8088 pact.telegram.TelegramService/CreateSession
    • Отправка сообщения
    grpcurl -plaintext -d '{
    "session_id": "sess_1772302101",
    "peer": "me",
    "text": "Привет из Telegram Service!"
    }' localhost:8088 pact.telegram.TelegramService/SendMessage
    • Подписка для получения входящих сообщений / Stream
    grpcurl -plaintext -d '{"session_id": "sess_1772302101"}' localhost:8088 pact.telegram.TelegramService/SubscribeMessages

#5. Структура проекта
    • proto/ — описание gRPC сервиса.
    • internal/gen/telegram/ - сгенерированный код.
    • internal/telegram/ — логика воркеров и менеджера сессий.
    • internal/service/ — реализация методов gRPC сервера.
    • internal/interfaces - интерфейсы.
    • sessions/ — локальное хранилище авторизационных данных.
 
#6. Архитектурные решения
    • Паттерн Manager + Worker(есть центральный узел управления manager, и для каждой сессии создаются изолированные сущности workers;
    Каждый worker работает в отдельной горутине и не влияет на работу остальных. Жизненный цикл управляется контекстом)
    • Инъекция зависимостей осуществляется с использованием интерфейсов, что исключает круговые зависимости.
    • Архитектура разделена на слои - service(преобразует прото в бизнес-логику с помощью gRPC обработчиков)  и telegram (ядро системы)
    • Переменные окружения вынесены в .env и скрыты .gitignore
    • Сессии сохраняются в папке sessions (.json)
    • Для тестирования используется библиотека testify/mock.
    • QR передается через каналы, это обеспечивает буструю реакцию стрима без блокировок основного потока.