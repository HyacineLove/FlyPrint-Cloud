package websocket

import (
	"errors"
	"testing"
	"time"

	"fly-print-cloud/api/internal/database"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestSendCommandWithAckRejectsNonAcceptedStatus(t *testing.T) {
	for _, status := range []string{"rejected", ""} {
		connection := &Connection{
			NodeID:      "node-1",
			Send:        make(chan []byte, 1),
			done:        make(chan struct{}),
			pendingAcks: make(map[string]chan string),
		}

		result := make(chan error, 1)
		go func() {
			result <- connection.SendCommandWithAck(&Command{Type: "terminal_occupied"}, time.Second)
		}()

		select {
		case <-connection.Send:
			connection.ackMutex.Lock()
			var msgID string
			for id := range connection.pendingAcks {
				msgID = id
			}
			connection.ackMutex.Unlock()
			if msgID == "" {
				t.Fatal("expected pending ACK message")
			}
			connection.handleAckDirect(&CommandAck{MsgID: msgID, Status: status})
		case <-time.After(time.Second):
			t.Fatal("command was not queued")
		}

		if err := <-result; !errors.Is(err, ErrAckRejected) {
			t.Fatalf("SendCommandWithAck(status=%q) error = %v, want ErrAckRejected", status, err)
		}
	}
}

func TestTransitionIntegrationStatusReturnsCallbackRepositoryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	wantErr := errors.New("callback database unavailable")
	mock.ExpectBegin().WillReturnError(wantErr)
	repo := database.NewIntegrationCallbackRepository(&database.DB{DB: db})
	if err := transitionIntegrationStatus(repo, "job-1", "failed", "dispatch_failed", "dispatch failed"); !errors.Is(err, wantErr) {
		t.Fatalf("transitionIntegrationStatus() error = %v, want %v", err, wantErr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
