package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type prpTokenConfig struct {
	Secret         string
	Issuer         string
	Audience       string
	SitePortalCode string
	Scopes         []string
}

func (c prpTokenConfig) validate() error {
	if len(c.Secret) < 32 ||
		strings.TrimSpace(c.Issuer) == "" ||
		strings.TrimSpace(c.Audience) == "" ||
		strings.TrimSpace(c.SitePortalCode) == "" ||
		len(c.Scopes) == 0 {
		return fmt.Errorf("PRP token configuration is incomplete")
	}
	for _, scope := range c.Scopes {
		if strings.TrimSpace(scope) == "" || strings.ContainsAny(scope, " \t\r\n") {
			return fmt.Errorf("PRP token scope is invalid")
		}
	}
	return nil
}

type prpTokenHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}

type prpTokenClaims struct {
	Issuer         string `json:"iss"`
	Audience       string `json:"aud"`
	SitePortalCode string `json:"site_portal_code"`
	Subject        string `json:"sub"`
	Scope          string `json:"scope"`
	IssuedAt       int64  `json:"iat"`
	ExpiresAt      int64  `json:"exp"`
	TokenID        string `json:"jti"`
}

func signPRPToken(
	config prpTokenConfig,
	subject string,
	issuedAt time.Time,
	expiresAt time.Time,
) (string, error) {
	if err := config.validate(); err != nil {
		return "", err
	}
	subject = strings.TrimSpace(subject)
	if subject == "" || !expiresAt.After(issuedAt) {
		return "", fmt.Errorf("PRP token subject or lifetime is invalid")
	}
	randomID := make([]byte, 16)
	if _, err := rand.Read(randomID); err != nil {
		return "", fmt.Errorf("generate PRP token id: %w", err)
	}
	header, err := json.Marshal(prpTokenHeader{Algorithm: "HS256", Type: "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(prpTokenClaims{
		Issuer:         strings.TrimSpace(config.Issuer),
		Audience:       strings.TrimSpace(config.Audience),
		SitePortalCode: strings.TrimSpace(config.SitePortalCode),
		Subject:        subject,
		Scope:          strings.Join(config.Scopes, " "),
		IssuedAt:       issuedAt.Unix(),
		ExpiresAt:      expiresAt.Unix(),
		TokenID:        hex.EncodeToString(randomID),
	})
	if err != nil {
		return "", err
	}
	encode := base64.RawURLEncoding.EncodeToString
	signingInput := encode(header) + "." + encode(claims)
	mac := hmac.New(sha256.New, []byte(config.Secret))
	_, _ = mac.Write([]byte(signingInput))
	return signingInput + "." + encode(mac.Sum(nil)), nil
}
