package middleware

import "testing"

func TestHasRequiredScopeAcceptsClientRealmAndScopeRoles(t *testing.T) {
	token := &OAuth2TokenInfo{
		Scope: "edge:heartbeat",
		ResourceAccess: map[string]struct {
			Roles []string `json:"roles"`
		}{"edge-client": {Roles: []string{"edge:printer"}}},
	}
	token.RealmAccess.Roles = []string{"edge:register"}
	for _, scope := range []string{"edge:heartbeat", "edge:printer", "edge:register"} {
		if !HasRequiredScope(token, scope) {
			t.Fatalf("scope %q should be accepted from normalized token roles", scope)
		}
	}
}
