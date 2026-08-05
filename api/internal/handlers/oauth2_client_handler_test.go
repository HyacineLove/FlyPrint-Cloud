package handlers

import "testing"

func TestValidateOAuth2ClientTypeOnlyAllowsEdgeAndSitePortal(t *testing.T) {
	for _, clientType := range []string{"edge_node", "site_portal"} {
		if err := validateOAuth2ClientType(clientType); err != nil {
			t.Fatalf("client type %q should be accepted: %v", clientType, err)
		}
	}
	if err := validateOAuth2ClientType("third_party"); err == nil {
		t.Fatal("third_party OAuth clients must be rejected")
	}
}
