package websocket

import "errors"

var (
	ErrNodeNotConnected     = errors.New("edge node not connected")
	ErrConnectionClosed     = errors.New("connection closed")
	ErrConnectionQueueFull  = errors.New("connection send queue is full")
	ErrAckTimeout           = errors.New("command acknowledgement timeout")
	ErrAckRejected          = errors.New("command acknowledgement rejected by edge")
	ErrInvalidMessage       = errors.New("invalid message format")
	ErrAuthenticationFailed = errors.New("authentication failed")
)
