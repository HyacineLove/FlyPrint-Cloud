package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const testTokenSecret = "12345678901234567890123456789012"

func buildLiteralToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return buildLiteralRawToken(t, header, payload)
}

func buildLiteralRawToken(t *testing.T, header, payload []byte) string {
	t.Helper()
	encode := base64.RawURLEncoding.EncodeToString
	signingInput := encode(header) + "." + encode(payload)
	mac := hmac.New(sha256.New, []byte(testTokenSecret))
	_, _ = mac.Write([]byte(signingInput))
	return signingInput + "." + encode(mac.Sum(nil))
}

func validLiteralClaims() map[string]any {
	return map[string]any{
		"iss":              "flyprint-sso-demo",
		"aud":              "flyprint-prp-demo",
		"site_portal_code": "official",
		"sub":              "user-1",
		"scope":            "files:list files:download upload-context:create",
		"iat":              int64(1000),
		"exp":              int64(1300),
		"jti":              "token-id-1",
	}
}

func testTokenVerifier() tokenVerifier {
	return tokenVerifier{
		secret:         testTokenSecret,
		issuer:         "flyprint-sso-demo",
		audience:       "flyprint-prp-demo",
		sitePortalCode: "official",
	}
}

func TestVerifyPRPTokenAcceptsBoundClaims(t *testing.T) {
	claims, err := testTokenVerifier().verify(
		buildLiteralToken(t, validLiteralClaims()),
		"files:list",
		time.Unix(1100, 0).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "user-1" ||
		claims.SitePortalCode != "official" ||
		claims.TokenID != "token-id-1" ||
		!claims.ExpiresAt.Equal(time.Unix(1300, 0).UTC()) {
		t.Fatalf("claims=%#v", claims)
	}
}

func TestVerifyPRPTokenRejectsTamperedPayload(t *testing.T) {
	token := buildLiteralToken(t, validLiteralClaims())
	segments := strings.Split(token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		t.Fatal(err)
	}
	payload = append([]byte(nil), payload...)
	payload[len(payload)-2] ^= 1
	segments[1] = base64.RawURLEncoding.EncodeToString(payload)
	_, err = testTokenVerifier().verify(strings.Join(segments, "."), "files:list", time.Unix(1100, 0).UTC())
	if !errors.Is(err, errTokenInvalid) {
		t.Fatalf("error=%v", err)
	}
}

func TestVerifyPRPTokenRejectsTrailingPayloadData(t *testing.T) {
	header := []byte(`{"alg":"HS256","typ":"JWT"}`)
	payload := []byte(`{"iss":"flyprint-sso-demo","aud":"flyprint-prp-demo","site_portal_code":"official","sub":"user-1","scope":"files:list","iat":1000,"exp":1300,"jti":"token-1"} trailing`)
	token := buildLiteralRawToken(t, header, payload)

	_, err := testTokenVerifier().verify(token, "files:list", time.Unix(1100, 0).UTC())
	if !errors.Is(err, errTokenInvalid) {
		t.Fatalf("expected invalid token, got %v", err)
	}
}

func TestVerifyPRPTokenRejectsWrongAudience(t *testing.T) {
	claims := validLiteralClaims()
	claims["aud"] = "other-prp"
	_, err := testTokenVerifier().verify(
		buildLiteralToken(t, claims), "files:list", time.Unix(1100, 0).UTC(),
	)
	if !errors.Is(err, errTokenInvalid) {
		t.Fatalf("error=%v", err)
	}
}

func TestVerifyPRPTokenRejectsWrongSitePortal(t *testing.T) {
	claims := validLiteralClaims()
	claims["site_portal_code"] = "private"
	_, err := testTokenVerifier().verify(
		buildLiteralToken(t, claims), "files:list", time.Unix(1100, 0).UTC(),
	)
	if !errors.Is(err, errTokenInvalid) {
		t.Fatalf("error=%v", err)
	}
}

func TestVerifyPRPTokenRejectsExpiredToken(t *testing.T) {
	_, err := testTokenVerifier().verify(
		buildLiteralToken(t, validLiteralClaims()), "files:list", time.Unix(1300, 0).UTC(),
	)
	if !errors.Is(err, errTokenExpired) {
		t.Fatalf("error=%v", err)
	}
}

func TestVerifyPRPTokenRequiresScope(t *testing.T) {
	_, err := testTokenVerifier().verify(
		buildLiteralToken(t, validLiteralClaims()), "files:delete", time.Unix(1100, 0).UTC(),
	)
	if !errors.Is(err, errTokenScope) {
		t.Fatalf("error=%v", err)
	}
}
