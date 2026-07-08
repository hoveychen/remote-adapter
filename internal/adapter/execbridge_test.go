package adapter

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/hoveychen/remote-cc-adapter/internal/execproto"
	"github.com/hoveychen/remote-cc-adapter/internal/executor"
	"github.com/hoveychen/remote-cc-adapter/internal/transport"
)

// TestExecBridgeOverLibp2p runs a subprocess on a libp2p-remote executor through
// the adapter's exec bridge: a proxy-like client connects to the bridge's local
// unix socket, and the command executes on the executor over libp2p — proving
// cross-machine Bash/ripgrep works without the proxy speaking libp2p.
func TestExecBridgeOverLibp2p(t *testing.T) {
	// Executor on a libp2p host.
	execHost, err := transport.NewLibp2pHost(transport.HostConfig{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}})
	if err != nil {
		t.Fatal(err)
	}
	execLn := transport.ListenLibp2p(execHost)
	defer execLn.Close()
	go executor.New(execLn, testLogger{t}).Serve()

	// Adapter-side dialer to the executor peer.
	brainHost, err := transport.NewLibp2pHost(transport.HostConfig{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}})
	if err != nil {
		t.Fatal(err)
	}
	dialer := transport.NewLibp2pDialer(brainHost, peer.AddrInfo{ID: execHost.ID(), Addrs: execHost.Addrs()})
	defer dialer.Close()

	// Exec bridge on a local unix socket (short path for sun_path limit).
	sockDir, err := os.MkdirTemp("/tmp", "rccbr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	bridgeSock := filepath.Join(sockDir, "b.sock")
	bridgeLn, err := transport.ListenUnix(bridgeSock)
	if err != nil {
		t.Fatal(err)
	}
	defer bridgeLn.Close()
	go NewExecBridge(bridgeLn, dialer, testLogger{t}).Serve()

	// A proxy-like client: connect to the bridge, speak execproto.
	var conn transport.Stream
	for i := 0; i < 50; i++ {
		if c, e := transport.NewUnixDialer(bridgeSock).Dial(t.Context()); e == nil {
			conn = c
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if conn == nil {
		t.Fatal("could not dial bridge")
	}
	defer conn.Close()

	if _, err := conn.Write([]byte{executor.StreamKindExec}); err != nil {
		t.Fatal(err)
	}
	req := &execproto.SpawnRequest{Argv: []string{"/bin/sh", "-c", "echo bridged-over-libp2p"}, Cwd: sockDir}
	if err := execproto.WriteSpawnRequest(conn, req); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	code := int32(-1)
	br := bufio.NewReader(conn)
	for {
		f, err := execproto.ReadFrame(br)
		if err != nil {
			break
		}
		if f.Tag == execproto.TagStdout {
			out.Write(f.Data)
		}
		if f.Tag == execproto.TagExit {
			code = f.ExitCode()
			break
		}
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if got := out.String(); got != "bridged-over-libp2p\n" {
		t.Errorf("stdout = %q, want %q", got, "bridged-over-libp2p\n")
	}
}
