package main

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestUploadContextCanBeConsumedOnlyOnce(t *testing.T) {
	store := newUploadContextStore()
	expiresAt := time.Unix(1300, 0).UTC()
	raw, err := store.create("user-1", expiresAt)
	if err != nil {
		t.Fatal(err)
	}

	context, err := store.consume(raw, time.Unix(1100, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if context.Subject != "user-1" || !context.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("context=%#v", context)
	}
	if _, err := store.consume(raw, time.Unix(1101, 0).UTC()); !errors.Is(err, errUploadContextInvalid) {
		t.Fatalf("second consume error=%v", err)
	}
}

func TestUploadContextRejectsExpiredValue(t *testing.T) {
	store := newUploadContextStore()
	raw, err := store.create("user-1", time.Unix(1100, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.consume(raw, time.Unix(1100, 0).UTC()); !errors.Is(err, errUploadContextExpired) {
		t.Fatalf("expired consume error=%v", err)
	}
	if _, err := store.consume(raw, time.Unix(1099, 0).UTC()); !errors.Is(err, errUploadContextInvalid) {
		t.Fatalf("expired context was retained: %v", err)
	}
}

func TestUploadContextCarriesOnlySubjectAndExpiry(t *testing.T) {
	contextType := reflect.TypeOf(uploadContext{})
	if contextType.NumField() != 2 ||
		contextType.Field(0).Name != "Subject" ||
		contextType.Field(1).Name != "ExpiresAt" {
		t.Fatalf("upload context fields=%v", contextType)
	}
}
