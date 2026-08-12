package websocket

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPortalSessionReadyDoesNotSerializePrivateCredential(t *testing.T) {
	payload := PortalSessionReadyPayload{
		SitePortalCode:        "official",
		SitePortalDisplayName: "\u5b98\u65b9\u6253\u5370\u670d\u52a1",
		ClaimBaseURL:          "https://portal.example.test",
		ClaimCode:             "claim-1",
		TerminalSessionID:     "session-1",
		CloudUserID:           "user-1",
		ExpiresAt:             time.Date(2026, 7, 30, 12, 5, 0, 0, time.UTC),
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, `"claim_code":"claim-1"`) {
		t.Fatalf("missing claim code: %s", text)
	}
	if !strings.Contains(text, "\"site_portal_display_name\":\"\u5b98\u65b9\u6253\u5370\u670d\u52a1\"") {
		t.Fatalf("missing Site Portal display name: %s", text)
	}
	for _, forbidden := range []string{"access_token", "prp_credential", "prp_base_url", "providers", "provider_id", "cookie", "password"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("private field %q leaked: %s", forbidden, text)
		}
	}
}
