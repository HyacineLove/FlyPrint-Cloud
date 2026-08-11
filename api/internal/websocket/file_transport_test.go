package websocket

import (
	"encoding/json"
	"testing"

	"fly-print-cloud/api/internal/models"
)

const testContentHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestBuildProxyFileURL(t *testing.T) {
	t.Parallel()

	got := buildProxyFileURL("file-123")
	if got != "/api/v1/files/file-123" {
		t.Fatalf("buildProxyFileURL() = %q, want %q", got, "/api/v1/files/file-123")
	}
}

func TestExtractProxyFileID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fileURL string
		want    string
	}{
		{name: "proxy path", fileURL: "/api/v1/files/file-123", want: "file-123"},
		{name: "proxy path with query credential rejected", fileURL: "/api/v1/files/file-123?token=abc", want: ""},
		{name: "absolute URL ignored in phase one", fileURL: "https://minio.local/bucket/object?X-Amz-Signature=abc", want: ""},
		{name: "empty", fileURL: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := extractProxyFileID(tt.fileURL); got != tt.want {
				t.Fatalf("extractProxyFileID(%q) = %q, want %q", tt.fileURL, got, tt.want)
			}
		})
	}
}

func TestDispatchPreviewFileIncludesContentHash(t *testing.T) {
	t.Parallel()

	conn := &Connection{NodeID: "node-1", Send: make(chan []byte, 1)}
	manager := &ConnectionManager{
		connections: map[string]*Connection{"node-1": conn},
	}

	err := manager.DispatchPreviewFile(
		"node-1",
		"file-1",
		"/api/v1/files/file-1",
		"sample.pdf",
		123,
		"application/pdf",
		testContentHash,
	)
	if err != nil {
		t.Fatalf("DispatchPreviewFile() error = %v", err)
	}

	var msg Message
	if err := json.Unmarshal(<-conn.Send, &msg); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	data, ok := msg.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("message data type = %T, want map", msg.Data)
	}
	if data["content_hash"] != testContentHash {
		t.Fatalf(
			"content_hash = %v, want %s", data["content_hash"], testContentHash,
		)
	}
}

func TestDispatchOfficialPreviewIncludesTerminalProofAndDefaults(t *testing.T) {
	conn := &Connection{NodeID: "node-1", Send: make(chan []byte, 1)}
	manager := &ConnectionManager{connections: map[string]*Connection{"node-1": conn}}
	payload := PreviewFilePayload{FileID: "file-1", FileURL: "/api/v1/files/file-1", FileName: "sample.pdf", ContentHash: testContentHash,
		TerminalSessionID: "session-1", TerminalTicketHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		PrintOptions: map[string]interface{}{"copies": float64(2)}}
	if err := manager.DispatchOfficialPreview("node-1", payload); err != nil {
		t.Fatalf("dispatch official preview: %v", err)
	}
	var msg Message
	if err := json.Unmarshal(<-conn.Send, &msg); err != nil {
		t.Fatal(err)
	}
	data := msg.Data.(map[string]interface{})
	if data["terminal_session_id"] != "session-1" || data["terminal_ticket_hash"] == "" {
		t.Fatalf("terminal context missing: %#v", data)
	}
	options := data["print_options"].(map[string]interface{})
	if options["copies"] != float64(2) {
		t.Fatalf("print defaults missing: %#v", options)
	}
}

func TestDispatchPrintJobIncludesContentHash(t *testing.T) {
	t.Parallel()

	conn := &Connection{
		NodeID:      "node-1",
		Send:        make(chan []byte, 1),
		pendingAcks: make(map[string]chan string),
	}
	manager := &ConnectionManager{
		connections: map[string]*Connection{"node-1": conn},
	}
	job := &models.PrintJob{
		ID:          "job-1",
		Name:        "sample.pdf",
		PrinterID:   "printer-1",
		FileURL:     "/api/v1/files/file-1",
		ContentHash: testContentHash,
		Copies:      1,
		MaxRetries:  3,
	}

	received := make(chan Command, 1)
	go func() {
		var cmd Command
		if err := json.Unmarshal(<-conn.Send, &cmd); err == nil {
			received <- cmd
			conn.handleAckDirect(
				&CommandAck{MsgID: cmd.MsgID, CommandID: cmd.CommandID, Status: "accepted"},
			)
		}
	}()

	if err := manager.DispatchPrintJob("node-1", job); err != nil {
		t.Fatalf("DispatchPrintJob() error = %v", err)
	}

	cmd := <-received
	data, ok := cmd.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("command data type = %T, want map", cmd.Data)
	}
	if data["content_hash"] != testContentHash {
		t.Fatalf(
			"content_hash = %v, want %s", data["content_hash"], testContentHash,
		)
	}
	if _, present := data["printer_name"]; present {
		t.Fatal("print_job must identify its target only by printer_id")
	}
}
