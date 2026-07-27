package security

import "testing"

func TestNormalizeEmail(t *testing.T) {
	if got := NormalizeEmail("  Alice@Example.COM "); got != "alice@example.com" {
		t.Fatalf("NormalizeEmail() = %q, want alice@example.com", got)
	}
}

func TestInternalUsernameForEmailIsStableAndLegacyCompatible(t *testing.T) {
	first := InternalUsernameForEmail("Alice@Example.COM")
	second := InternalUsernameForEmail(" alice@example.com ")
	if first != second {
		t.Fatalf("internal username is not stable: %q != %q", first, second)
	}
	if len(first) != 50 || first[:2] != "u_" {
		t.Fatalf("internal username = %q, want a 50-character u_ identifier", first)
	}
	if err := ValidateUsername(first); err != nil {
		t.Fatalf("internal username failed legacy validation: %v", err)
	}
}
