// Package transport abstracts the brain<->executor link so the wire protocol
// (internal/protocol) is decoupled from how bytes actually travel.
//
// The adapter (brain) uses a Dialer to open RPC streams to the executor; the
// executor uses a Listener to accept them. Today the only implemented transport
// is a local Unix-domain socket (UnixDialer/UnixListener), which lets the whole
// pipeline run end-to-end on one host. The design target is go-libp2p (DCUtR
// hole-punching + circuit-relay fallback, Noise/TLS, PeerID == public key); it
// is present as a compile-time stub (Libp2pDialer/Libp2pListener) so callers can
// already program against the interface. See design doc §3.3.
package transport

import (
	"context"
	"io"
)

// Stream is a bidirectional byte pipe carrying one or more protocol frames.
type Stream = io.ReadWriteCloser

// Dialer opens streams to the executor (brain side).
type Dialer interface {
	// Dial opens a new stream to the executor.
	Dial(ctx context.Context) (Stream, error)
	// Close releases dialer resources.
	Close() error
}

// Listener accepts streams from the adapter (executor side).
type Listener interface {
	// Accept blocks until the next inbound stream arrives.
	Accept() (Stream, error)
	// Addr returns a human-readable address for logs.
	Addr() string
	// Close stops accepting and releases resources.
	Close() error
}
