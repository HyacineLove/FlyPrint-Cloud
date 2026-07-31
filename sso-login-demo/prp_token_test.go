package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testPRPTokenConfig() prpTokenConfig {
	return prpTokenConfig{
		Secret:         "12345678901234567890123456789012",
		Issuer:         "flyprint-sso-demo",
		Audience:       "flyprint-prp-demo",
		SitePortalCode: "official",
		Scopes:         []string{"files:list", "files:download", "upload-context:create"},
	}
}

func decodePRPTokenSegment(t *testing.T, segment string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSignPRPTokenProducesPublicClaims(t *testing.T) {
	token, err := signPRPToken(
		testPRPTokenConfig(),
		"user-1",
		time.Unix(1000, 0).UTC(),
		time.Unix(1300, 0).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	segments := strings.Split(token, ".")
	if len(segments) != 3 {
		t.Fatalf("segments=%d token=%q", len(segments), token)
	}
	header := decodePRPTokenSegment(t, segments[0])
	if header["alg"] != "HS256" || header["typ"] != "JWT" {
		t.Fatalf("header=%#v", header)
	}
	claims := decodePRPTokenSegment(t, segments[1])
	if claims["iss"] != "flyprint-sso-demo" ||
		claims["aud"] != "flyprint-prp-demo" ||
		claims["site_portal_code"] != "official" ||
		claims["sub"] != "user-1" ||
		claims["scope"] != "files:list files:download upload-context:create" ||
		claims["iat"] != float64(1000) ||
		claims["exp"] != float64(1300) {
		t.Fatalf("claims=%#v", claims)
	}
	if strings.TrimSpace(claims["jti"].(string)) == "" {
		t.Fatal("jti is empty")
	}
}

func TestSignPRPTokenDoesNotSerializeSecret(t *testing.T) {
	config := testPRPTokenConfig()
	issuedAt := time.Unix(1000, 0).UTC()
	expiresAt := time.Unix(1300, 0).UTC()
	token, err := signPRPToken(config, "user-1", issuedAt, expiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(token, config.Secret) {
		t.Fatal("signing secret entered serialized token")
	}
}
