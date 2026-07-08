package transport

import (
	"context"
	"net"
	"os"
)

// UnixDialer dials a Unix-domain socket. Each Dial is an independent stream
// (connection), which the executor accepts and serves concurrently.
type UnixDialer struct {
	Path string
	d    net.Dialer
}

// NewUnixDialer returns a dialer for the socket at path.
func NewUnixDialer(path string) *UnixDialer { return &UnixDialer{Path: path} }

// Dial opens a new connection to the socket.
func (u *UnixDialer) Dial(ctx context.Context) (Stream, error) {
	return u.d.DialContext(ctx, "unix", u.Path)
}

// Close is a no-op; per-stream connections are closed by the caller.
func (u *UnixDialer) Close() error { return nil }

// UnixListener listens on a Unix-domain socket.
type UnixListener struct {
	path string
	ln   net.Listener
}

// ListenUnix removes any stale socket at path and starts listening.
func ListenUnix(path string) (*UnixListener, error) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	return &UnixListener{path: path, ln: ln}, nil
}

// Accept returns the next inbound connection as a Stream.
func (u *UnixListener) Accept() (Stream, error) { return u.ln.Accept() }

// Addr returns the socket path.
func (u *UnixListener) Addr() string { return "unix:" + u.path }

// Close stops listening and unlinks the socket file.
func (u *UnixListener) Close() error {
	err := u.ln.Close()
	_ = os.Remove(u.path)
	return err
}
