package websocket

import (
	"testing"
	"time"
)

func TestMarkTerminalOccupiedStoresPendingUntilCleared(t *testing.T) {
	manager := NewConnectionManager(nil, nil)
	payload := TerminalOccupiedPayload{
		TerminalSessionID:  "session-1",
		TerminalTicketHash: "abcd",
		ExpiresAt:          time.Now().Add(5 * time.Minute),
	}
	manager.MarkTerminalOccupied("node-1", payload)

	manager.occupiedMu.Lock()
	pending := manager.pendingOccupied["node-1"]
	manager.occupiedMu.Unlock()
	if pending == nil || pending.TerminalSessionID != "session-1" {
		t.Fatalf("expected pending occupy for node-1, got %#v", pending)
	}

	manager.ClearTerminalOccupied("node-1")
	manager.occupiedMu.Lock()
	_, exists := manager.pendingOccupied["node-1"]
	manager.occupiedMu.Unlock()
	if exists {
		t.Fatal("pending occupy should be cleared")
	}
}

func TestReplayTerminalOccupiedKeepsPendingWithoutConnection(t *testing.T) {
	manager := NewConnectionManager(nil, nil)
	payload := TerminalOccupiedPayload{
		TerminalSessionID:  "session-2",
		TerminalTicketHash: "ef01",
		ExpiresAt:          time.Now().Add(time.Minute),
	}
	manager.ReplayTerminalOccupiedIfNeeded("node-2", payload, false)
	// No connection: dispatch is deferred; pending must remain for reconnect.
	time.Sleep(20 * time.Millisecond)
	manager.occupiedMu.Lock()
	pending := manager.pendingOccupied["node-2"]
	manager.occupiedMu.Unlock()
	if pending == nil || pending.TerminalSessionID != "session-2" {
		t.Fatalf("expected pending occupy after replay without connection, got %#v", pending)
	}
}

func TestReplayTerminalOccupiedSkipsWhenEdgeAlreadyHasTicket(t *testing.T) {
	manager := NewConnectionManager(nil, nil)
	payload := TerminalOccupiedPayload{
		TerminalSessionID:  "session-3",
		TerminalTicketHash: "aa11",
		ExpiresAt:          time.Now().Add(time.Minute),
	}
	manager.ReplayTerminalOccupiedIfNeeded("node-3", payload, true)
	manager.occupiedMu.Lock()
	_, exists := manager.pendingOccupied["node-3"]
	manager.occupiedMu.Unlock()
	if exists {
		t.Fatal("should not re-arm occupy when Edge already reported the ticket hash")
	}
}

func TestReplayTerminalOccupiedDoesNotResendWhilePending(t *testing.T) {
	manager := NewConnectionManager(nil, nil)
	first := TerminalOccupiedPayload{
		TerminalSessionID:  "session-4",
		TerminalTicketHash: "bb22",
		ExpiresAt:          time.Now().Add(time.Minute),
	}
	manager.MarkTerminalOccupied("node-4", first)
	manager.ReplayTerminalOccupiedIfNeeded("node-4", TerminalOccupiedPayload{
		TerminalSessionID:  "session-4",
		TerminalTicketHash: "bb22",
		ExpiresAt:          time.Now().Add(2 * time.Minute),
	}, true)
	manager.occupiedMu.Lock()
	pending := manager.pendingOccupied["node-4"]
	manager.occupiedMu.Unlock()
	if pending == nil || pending.TerminalSessionID != "session-4" {
		t.Fatalf("pending occupy must remain while ACK outstanding, got %#v", pending)
	}
}

func TestReplayTerminalOccupiedResendsAfterReconnect(t *testing.T) {
	manager := NewConnectionManager(nil, nil)
	connection := &Connection{
		NodeID:      "node-5",
		Send:        make(chan []byte, 1),
		done:        make(chan struct{}),
		pendingAcks: make(map[string]chan string),
	}
	manager.mutex.Lock()
	manager.connections["node-5"] = connection
	manager.mutex.Unlock()
	payload := TerminalOccupiedPayload{
		TerminalSessionID:  "session-5",
		TerminalTicketHash: "cc33",
		ExpiresAt:          time.Now().Add(time.Minute),
	}
	manager.occupiedMu.Lock()
	manager.pendingOccupied["node-5"] = &payload
	manager.occupiedMu.Unlock()

	manager.ReplayTerminalOccupiedIfNeeded("node-5", payload, false)
	select {
	case <-connection.Send:
		connection.ackMutex.Lock()
		var msgID string
		for id := range connection.pendingAcks {
			msgID = id
		}
		connection.ackMutex.Unlock()
		if msgID == "" {
			t.Fatal("expected replayed command to wait for ACK")
		}
		connection.handleAckDirect(&CommandAck{MsgID: msgID, Status: "accepted"})
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected terminal_occupied to be replayed after reconnect")
	}
}
