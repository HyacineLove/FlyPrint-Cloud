package auth

import "testing"

func TestViewerRoleReceivesOnlyFileReadScope(t *testing.T) {
	if got := mapUserRoleToScopes("viewer"); got != "file:read" {
		t.Fatalf("viewer scopes = %q, want file:read", got)
	}
}
