package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

var (
	errTokenInvalid = errors.New("token invalid")
	errTokenExpired = errors.New("token expired")
	errTokenScope   = errors.New("token scope unavailable")
)

type accessClaims struct {
	Subject        string
	SitePortalCode string
	Scopes         map[string]struct{}
	ExpiresAt      time.Time
	TokenID        string
}

type tokenVerifier struct {
	secret         string
	issuer         string
	audience       string
	sitePortalCode string
}

type tokenHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}

type serializedAccessClaims struct {
	Issuer         string `json:"iss"`
	Audience       string `json:"aud"`
	SitePortalCode string `json:"site_portal_code"`
	Subject        string `json:"sub"`
	Scope          string `json:"scope"`
	IssuedAt       int64  `json:"iat"`
	ExpiresAt      int64  `json:"exp"`
	TokenID        string `json:"jti"`
}

func decodeTokenJSON(segment string, target any) error {
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return errTokenInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errTokenInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errTokenInvalid
	}
	return nil
}

func (v tokenVerifier) verify(raw, requiredScope string, now time.Time) (accessClaims, error) {
	segments := strings.Split(strings.TrimSpace(raw), ".")
	if len(segments) != 3 {
		return accessClaims{}, errTokenInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(segments[2])
	if err != nil {
		return accessClaims{}, errTokenInvalid
	}
	signingInput := segments[0] + "." + segments[1]
	mac := hmac.New(sha256.New, []byte(v.secret))
	_, _ = mac.Write([]byte(signingInput))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return accessClaims{}, errTokenInvalid
	}

	var header tokenHeader
	if err := decodeTokenJSON(segments[0], &header); err != nil ||
		header.Algorithm != "HS256" || header.Type != "JWT" {
		return accessClaims{}, errTokenInvalid
	}
	var serialized serializedAccessClaims
	if err := decodeTokenJSON(segments[1], &serialized); err != nil {
		return accessClaims{}, err
	}
	if serialized.Issuer != v.issuer ||
		serialized.Audience != v.audience ||
		serialized.SitePortalCode != v.sitePortalCode ||
		strings.TrimSpace(serialized.Subject) == "" ||
		strings.TrimSpace(serialized.TokenID) == "" ||
		serialized.IssuedAt <= 0 ||
		serialized.ExpiresAt <= serialized.IssuedAt {
		return accessClaims{}, errTokenInvalid
	}
	expiresAt := time.Unix(serialized.ExpiresAt, 0).UTC()
	if !expiresAt.After(now) {
		return accessClaims{}, errTokenExpired
	}
	scopes := make(map[string]struct{})
	for _, scope := range strings.Fields(serialized.Scope) {
		scopes[scope] = struct{}{}
	}
	if _, exists := scopes[requiredScope]; !exists {
		return accessClaims{}, errTokenScope
	}
	return accessClaims{
		Subject:        serialized.Subject,
		SitePortalCode: serialized.SitePortalCode,
		Scopes:         scopes,
		ExpiresAt:      expiresAt,
		TokenID:        serialized.TokenID,
	}, nil
}
