package websocket

import (
	"encoding/json"
	"testing"
)

func TestUploadTokenErrorPreservesRequestID(t *testing.T) {
	connection := &Connection{Send: make(chan []byte, 1)}
	connection.sendRequestError(
		"printer_out_of_paper",
		"Printer cannot accept a new task",
		"printer-1",
		"request-1",
	)

	var message struct {
		Type string                 `json:"type"`
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(<-connection.Send, &message); err != nil {
		t.Fatal(err)
	}
	if message.Type != CmdTypeError {
		t.Fatalf("unexpected message type %q", message.Type)
	}
	if message.Data["request_id"] != "request-1" {
		t.Fatalf("request id was not preserved: %#v", message.Data)
	}
}

func TestUploadTokenResponsePayloadUsesHeaderTokenAndFragmentWebURL(t *testing.T) {
	payload := UploadTokenResponsePayload{
		RequestID: "request-2",
		UploadURL: "/api/v1/files",
		WebURL:    "/upload#token=opaque&node_id=node-1&printer_id=printer-1",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["request_id"] != "request-2" {
		t.Fatalf("request id was not encoded: %#v", decoded)
	}
	if decoded["upload_url"] != "/api/v1/files" {
		t.Fatalf("upload URL = %#v, want header-authenticated endpoint", decoded["upload_url"])
	}
	if decoded["web_url"] != "/upload#token=opaque&node_id=node-1&printer_id=printer-1" {
		t.Fatalf("web URL = %#v, want fragment credential", decoded["web_url"])
	}
}

func TestUploadTokenRequestIDExtractsOnlyUploadTokenRequests(t *testing.T) {
	connection := &Connection{}
	message := &Message{
		Type: MsgTypeRequestUploadToken,
		Data: map[string]interface{}{
			"request_id": "request-3",
			"printer_id": "printer-1",
		},
	}
	if got := connection.uploadTokenRequestID(message); got != "request-3" {
		t.Fatalf("upload token request id = %q, want request-3", got)
	}
	if got := connection.uploadTokenRequestID(&Message{Type: MsgTypeHeartbeat, Data: message.Data}); got != "" {
		t.Fatalf("non-upload-token message request id = %q, want empty", got)
	}
}
