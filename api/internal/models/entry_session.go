package models

import "time"

// EntrySession is the durable parent of the QR (T1), browser (T2), handoff
// (T3), and Claim capabilities.  Raw credential values are deliberately not
// represented here because only their SHA-256 hashes are persisted.
type EntrySession struct {
	ID                   string
	T1Hash               string
	AcquireHash          string
	T2Hash               string
	NodeID               string
	PrinterID            string
	TerminalSessionID    string
	QRGeneration         int64
	Status               string
	MaskCommandID        string
	MaskConfirmedAt      *time.Time
	PortalAttemptVersion int
	IssuedAt             time.Time
	ExpiresAt            time.Time
}

type EntryPortalAttempt struct {
	ID             string
	EntrySessionID string
	Version        int
	SitePortalCode string
	T3Hash         string
	Status         string
	ExpiresAt      time.Time
}
