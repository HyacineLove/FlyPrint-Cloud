package websocket

import (
	"encoding/json"
	"testing"
)

func TestJobUpdateDataDeserializesReportedPrintUsage(t *testing.T) {
	payload := []byte(`{
		"event_id":"11111111-1111-1111-1111-111111111111",
		"job_id":"22222222-2222-2222-2222-222222222222",
		"status":"failed",
		"impressions_completed":4,
		"sheets_completed":3,
		"quota_consumed":6
	}`)
	var update JobUpdateData
	if err := json.Unmarshal(payload, &update); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if update.ImpressionsCompleted != 4 || update.SheetsCompleted != 3 || update.QuotaConsumed != 6 {
		t.Fatalf("reported usage = %#v", update)
	}
}

func TestTerminalJobUpdatePayloadHashIncludesReportedPrintUsage(t *testing.T) {
	base := JobUpdateData{JobID: "job-1", Status: "failed", ImpressionsCompleted: 4, SheetsCompleted: 3, QuotaConsumed: 6}
	changed := base
	changed.QuotaConsumed = 5
	if terminalJobUpdatePayloadHash(base) == terminalJobUpdatePayloadHash(changed) {
		t.Fatal("payload hash must change when reported quota changes")
	}
}
