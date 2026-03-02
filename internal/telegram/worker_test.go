package telegram

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gotd/td/bin"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"github.com/pact-tg-service/internal/interfaces"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockAuth struct {
	mock.Mock
}

func (m *MockAuth) Status(ctx context.Context) (any, error) {
	args := m.Called(ctx)
	return args.Get(0), args.Error(1)
}

type MockTelegramClient struct {
	mock.Mock
	MockAuth *MockAuth
}

func (m *MockTelegramClient) Run(ctx context.Context, f func(ctx context.Context) error) error {
	args := m.Called(ctx, f)
	if rf, ok := args.Get(0).(func(context.Context, func(context.Context) error) error); ok {
		return rf(ctx, f)
	}
	return args.Error(0)
}

func (m *MockTelegramClient) API() *tg.Client {
	return tg.NewClient(m)
}

func (m *MockTelegramClient) Auth() interfaces.AuthClient {
	args := m.Called()
	return args.Get(0).(interfaces.AuthClient)
}

func (m *MockTelegramClient) Self(ctx context.Context) (*tg.User, error) {
	args := m.Called(ctx)
	var user *tg.User
	if g0 := args.Get(0); g0 != nil {
		user = g0.(*tg.User)
	}
	return user, args.Error(1)
}

func (m *MockTelegramClient) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	args := m.Called(ctx, input, output)

	if rf, ok := args.Get(0).(func(context.Context, bin.Encoder, bin.Decoder) error); ok {
		return rf(ctx, input, output)
	}

	return args.Error(0)
}

func TestWorker_Start_AlreadyAuthorized(t *testing.T) {
	mockAuth := new(MockAuth)
	mockClient := &MockTelegramClient{MockAuth: mockAuth}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := &Worker{
		SessionID:   "test_sess",
		client:      mockClient,
		ctx:         ctx,
		cancel:      cancel,
		QRCodeChan:  make(chan string, 1),
		MessageChan: make(chan *tg.UpdateNewMessage, 1),
		logger:      zerolog.Nop(),
	}

	type authStatus struct{ Authorized bool }
	mockAuth.On("Status", mock.Anything).Return(&authStatus{Authorized: true}, nil)

	mockClient.On("Run", mock.Anything, mock.Anything).Return(nil)

	w.Start()

	select {
	case _, ok := <-w.QRCodeChan:
		assert.False(t, ok, "Канал QR должен быть закрыт, так как мы уже вошли")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Тест завис: канал QR не закрылся вовремя")
	}
}

func TestWorker_Start_AuthStatusError(t *testing.T) {
	mockClient := &MockTelegramClient{}
	mockAuth := &MockAuth{}
	mockClient.MockAuth = mockAuth

	mockClient.On("Auth").Return(mockAuth)
	mockClient.On("Run", mock.Anything, mock.Anything).Return(func(ctx context.Context, f func(context.Context) error) error {
		return f(ctx)
	})
	mockAuth.On("Status", mock.Anything).Return((*auth.Status)(nil), errors.New("api error"))

	ctx, cancel := context.WithCancel(context.Background())
	w := &Worker{
		client:      mockClient,
		ctx:         ctx,
		cancel:      cancel,
		logger:      zerolog.Nop(),
		QRCodeChan:  make(chan string, 1),
		MessageChan: make(chan *tg.UpdateNewMessage, 1),
	}

	w.Start()

	select {
	case _, ok := <-w.QRCodeChan:
		assert.False(t, ok, "Канал QRCodeChan должен быть закрыт")
	case <-time.After(time.Second * 2):
		t.Fatal("Тайм-аут: воркер не завершил работу при ошибке Auth")
	}

	mockClient.AssertExpectations(t)
	mockAuth.AssertExpectations(t)
}

func TestWorker_Stop(t *testing.T) {
	mockClient := &MockTelegramClient{}

	mockClient.On("Run", mock.Anything, mock.Anything).Return(func(ctx context.Context, f func(context.Context) error) error {
		_ = f(ctx)
		return nil
	})

	mockAuth := &MockAuth{}
	mockClient.On("Auth").Return(mockAuth)
	mockAuth.On("Status", mock.Anything).Return(&auth.Status{Authorized: true}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	w := &Worker{
		client:      mockClient,
		ctx:         ctx,
		cancel:      cancel,
		logger:      zerolog.Nop(),
		QRCodeChan:  make(chan string, 1),
		MessageChan: make(chan *tg.UpdateNewMessage, 1),
	}

	w.Start()

	w.Stop()

	timeout := time.Second * 1

	select {
	case _, ok := <-w.QRCodeChan:
		assert.False(t, ok, "QRCodeChan должен быть закрыт")
	case <-time.After(timeout):
		t.Fatal("QRCodeChan не закрылся после вызова Stop()")
	}

	select {
	case _, ok := <-w.MessageChan:
		assert.False(t, ok, "MessageChan должен быть закрыт")
	case <-time.After(timeout):
		t.Fatal("MessageChan не закрылся после вызова Stop()")
	}
}

func TestWorker_SendText_ToSelf(t *testing.T) {
	mockClient := &MockTelegramClient{}

	selfUser := &tg.User{
		ID:         12345,
		AccessHash: 999,
		Username:   "me",
	}
	selfUser.Flags.Set(0)
	selfUser.Flags.Set(3)
	selfUser.Flags.Set(6)

	mockClient.On("Self", mock.Anything).Return(selfUser, nil)

	expectedMsgID := 777

	mockClient.On("Invoke", mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
			if _, ok := input.(*tg.MessagesSendMessageRequest); !ok {
				return fmt.Errorf("unexpected request type: %T", input)
			}

			res, ok := output.(*tg.UpdatesBox)
			if !ok {
				return fmt.Errorf("unexpected output type: %T", output)
			}

			res.Updates = &tg.Updates{
				Updates: []tg.UpdateClass{
					&tg.UpdateNewMessage{
						Message: &tg.Message{
							ID: expectedMsgID,
						},
					},
				},
			}
			return nil
		},
	)

	w := &Worker{client: mockClient}
	id, err := w.SendText(context.Background(), "me", "hello self")

	assert.NoError(t, err)
	assert.Equal(t, int64(expectedMsgID), id)
	mockClient.AssertExpectations(t)
}

func TestWorker_SendText_ByUsername(t *testing.T) {
	mockClient := &MockTelegramClient{}
	targetUser := "pact_user"
	expectedMsgID := 888
	mockClient.On("Invoke", mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
			switch req := input.(type) {

			case *tg.ContactsResolveUsernameRequest:
				if req.Username != targetUser {
					return fmt.Errorf("expected username %s, got %s", targetUser, req.Username)
				}
				res := output.(*tg.ContactsResolvedPeer)
				res.Users = []tg.UserClass{
					&tg.User{ID: 999, AccessHash: 123, Username: targetUser},
				}
				return nil

			case *tg.MessagesSendMessageRequest:
				res := output.(*tg.UpdatesBox)
				res.Updates = &tg.Updates{
					Updates: []tg.UpdateClass{
						&tg.UpdateNewMessage{
							Message: &tg.Message{ID: expectedMsgID},
						},
					},
				}
				return nil
			default:
				return fmt.Errorf("unexpected request type: %T", input)
			}
		},
	).Times(2)

	w := &Worker{client: mockClient}

	id, err := w.SendText(context.Background(), "@"+targetUser, "hi")

	assert.NoError(t, err)
	assert.Equal(t, int64(expectedMsgID), id)
	mockClient.AssertExpectations(t)
}

func TestWorker_LogoutAndStop(t *testing.T) {
	mockClient := &MockTelegramClient{}
	mockAuth := &MockAuth{}
	mockClient.MockAuth = mockAuth

	mockClient.On("Auth").Return(mockAuth)
	mockAuth.On("Status", mock.Anything).Return(&auth.Status{Authorized: true}, nil)

	mockClient.On("Run", mock.Anything, mock.Anything).Return(func(ctx context.Context, f func(context.Context) error) error {
		return f(ctx)
	})

	mockClient.On("Invoke", mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
			switch req := input.(type) {
			case *tg.AuthLogOutRequest:

				res, ok := output.(*tg.AuthLoggedOut)
				if !ok {
					return fmt.Errorf("unexpected output type for AuthLogOut: %T", output)
				}

				_ = res
				return nil
			default:
				return fmt.Errorf("unexpected request type in Invoke: %T", req)
			}
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	w := &Worker{
		client:      mockClient,
		ctx:         ctx,
		cancel:      cancel,
		logger:      zerolog.Nop(),
		QRCodeChan:  make(chan string, 1),
		MessageChan: make(chan *tg.UpdateNewMessage, 1),
	}

	w.Start()

	err := w.LogoutAndStop(context.Background())

	assert.NoError(t, err)

	select {
	case _, ok := <-w.QRCodeChan:
		assert.False(t, ok, "QRCodeChan должен быть закрыт")
	case <-time.After(time.Second * 1):
		t.Fatal("Тайм-аут: каналы не закрылись после LogoutAndStop")
	}

	select {
	case _, ok := <-w.MessageChan:
		assert.False(t, ok, "MessageChan должен быть закрыт")
	case <-time.After(time.Millisecond * 100):
		t.Fatal("MessageChan не закрылся")
	}

	mockClient.AssertExpectations(t)
	mockAuth.AssertExpectations(t)
}
