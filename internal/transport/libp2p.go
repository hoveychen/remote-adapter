package transport

import (
	"context"
	"errors"
)

// ErrNotImplemented is returned by the libp2p transport stub. The go-libp2p
// implementation (DCUtR hole-punching, circuit-relay fallback, Noise/TLS,
// PeerID == public key) is the design target for crossing NATs between the
// brain host and the remote sandbox; see design doc §3.3. It is intentionally
// not yet wired so the first cut can be validated over a local Unix socket
// without pulling in the libp2p dependency tree.
var ErrNotImplemented = errors.New("transport: libp2p transport not yet implemented (design doc §3.3); use the unix-socket transport for local end-to-end runs")

// Libp2pConfig holds the parameters a future go-libp2p transport will need.
type Libp2pConfig struct {
	// PeerAddr is the multiaddr (or PeerID) of the remote executor.
	PeerAddr string
	// RelayAddrs are optional circuit-relay multiaddrs used when hole-punching
	// fails.
	RelayAddrs []string
}

// Libp2pDialer is a placeholder Dialer. Every Dial returns ErrNotImplemented.
type Libp2pDialer struct{ Config Libp2pConfig }

// NewLibp2pDialer constructs the stub dialer.
func NewLibp2pDialer(cfg Libp2pConfig) *Libp2pDialer { return &Libp2pDialer{Config: cfg} }

// Dial always returns ErrNotImplemented until go-libp2p is wired in.
func (l *Libp2pDialer) Dial(context.Context) (Stream, error) { return nil, ErrNotImplemented }

// Close is a no-op.
func (l *Libp2pDialer) Close() error { return nil }

// Libp2pListener is a placeholder Listener. Accept returns ErrNotImplemented.
type Libp2pListener struct{ Config Libp2pConfig }

// NewLibp2pListener constructs the stub listener.
func NewLibp2pListener(cfg Libp2pConfig) *Libp2pListener { return &Libp2pListener{Config: cfg} }

// Accept always returns ErrNotImplemented until go-libp2p is wired in.
func (l *Libp2pListener) Accept() (Stream, error) { return nil, ErrNotImplemented }

// Addr returns a descriptive placeholder address.
func (l *Libp2pListener) Addr() string { return "libp2p:" + l.Config.PeerAddr + " (stub)" }

// Close is a no-op.
func (l *Libp2pListener) Close() error { return nil }
