package middleware

import "testing"

func TestValidateAnyScopeAcceptsEitherAdminRole(t *testing.T) {
	if !validateAnyScope([]string{"fly-print-operator"}, []string{"fly-print-admin", "fly-print-operator"}) {
		t.Fatal("operator role should satisfy admin/operator any-scope policy")
	}
	if validateAnyScope([]string{"fly-print-viewer"}, []string{"fly-print-admin", "fly-print-operator"}) {
		t.Fatal("viewer role must not satisfy admin/operator any-scope policy")
	}
}

func TestValidateScopesRetainsAllScopeSemantics(t *testing.T) {
	if validateScopes([]string{"fly-print-operator"}, []string{"fly-print-admin", "fly-print-operator"}) {
		t.Fatal("the existing scope validator must retain AND semantics")
	}
}
