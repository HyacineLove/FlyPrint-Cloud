package handlers

import "testing"

func TestValidateSitePortalProviderRequestRequiresLocalSecretReference(t *testing.T) {
	valid := sitePortalProviderRequest{
		ProviderID: "invoice", DisplayName: "发票", Enabled: boolPtr(true), SortOrder: 1,
		FileBaseURL: "https://files.example.test", SignSecretRef: "INVOICE",
	}
	if err := validateSitePortalProviderRequest(valid); err != nil {
		t.Fatalf("valid Provider request rejected: %v", err)
	}
	for _, ref := range []string{"invoice", "A-ENV"} {
		invalid := valid
		invalid.SignSecretRef = ref
		if err := validateSitePortalProviderRequest(invalid); err == nil {
			t.Fatalf("invalid secret reference %q was accepted", ref)
		}
	}
	invalid := valid
	invalid.FileBaseURL = "https://files.example.test/?token=secret"
	if err := validateSitePortalProviderRequest(invalid); err == nil {
		t.Fatal("Provider URL with query parameters was accepted")
	}
}

func boolPtr(value bool) *bool { return &value }
