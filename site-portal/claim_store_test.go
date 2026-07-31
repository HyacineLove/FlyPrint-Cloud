package main

import (
	"errors"
	"testing"
	"time"
)

func TestClaimCanBeRedeemedOnlyOnce(t *testing.T) {
	store := newClaimStore()
	code := store.create(claim{
		SitePortalCode: "official", NodeID: "edge-1", TerminalSessionID: "session-1",
		ExternalUserID: "user-1", DisplayName: "用户一", AccessToken: "access-token",
		AccessTokenExpiresAt: time.Now().Add(time.Minute), ExpiresAt: time.Now().Add(time.Minute),
	})
	input := redeemClaimInput{
		Code: code, SitePortalCode: "official", NodeID: "edge-1", TerminalSessionID: "session-1",
	}

	first, err := store.redeem(input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if first.AccessToken != "access-token" || first.ExternalUserID != "user-1" {
		t.Fatalf("unexpected claim: %#v", first)
	}
	if _, err := store.redeem(input, time.Now()); !errors.Is(err, errClaimUnavailable) {
		t.Fatalf("second redemption error=%v", err)
	}
}

func TestClaimRejectsWrongTerminalBindingWithoutConsuming(t *testing.T) {
	store := newClaimStore()
	code := store.create(claim{
		SitePortalCode: "official", NodeID: "edge-1", TerminalSessionID: "session-1",
		ExternalUserID: "user-1", AccessToken: "access-token",
		AccessTokenExpiresAt: time.Now().Add(time.Minute), ExpiresAt: time.Now().Add(time.Minute),
	})

	_, err := store.redeem(redeemClaimInput{
		Code: code, SitePortalCode: "official", NodeID: "edge-1", TerminalSessionID: "session-2",
	}, time.Now())
	if !errors.Is(err, errClaimBindingMismatch) {
		t.Fatalf("wrong binding error=%v", err)
	}
	if _, err := store.redeem(redeemClaimInput{
		Code: code, SitePortalCode: "official", NodeID: "edge-1", TerminalSessionID: "session-1",
	}, time.Now()); err != nil {
		t.Fatalf("correct binding should remain redeemable: %v", err)
	}
}

func TestExpiredClaimIsRejectedAndRemoved(t *testing.T) {
	store := newClaimStore()
	code := store.create(claim{
		SitePortalCode: "official", NodeID: "edge-1", TerminalSessionID: "session-1",
		ExternalUserID: "user-1", AccessToken: "access-token",
		AccessTokenExpiresAt: time.Now().Add(-time.Second), ExpiresAt: time.Now().Add(-time.Second),
	})
	_, err := store.redeem(redeemClaimInput{
		Code: code, SitePortalCode: "official", NodeID: "edge-1", TerminalSessionID: "session-1",
	}, time.Now())
	if !errors.Is(err, errClaimUnavailable) {
		t.Fatalf("expired claim error=%v", err)
	}
	if store.count() != 0 {
		t.Fatalf("expired claim remains stored: count=%d", store.count())
	}
}
