package executor

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hoveychen/remote-cc-adapter/internal/execproto"
	"github.com/hoveychen/remote-cc-adapter/internal/transport"
)

// TestExecIncrementalOutput models a run_in_background task: a long-running
// command that emits lines over time. The consumer reads incrementally (with a
// delay before draining), asserting output streams as it is produced rather than
// only at exit — the property BashOutput polling relies on.
func TestExecIncrementalOutput(t *testing.T) {
	stream := startExec(t, &execproto.SpawnRequest{
		// Three lines, ~150ms apart, then exit.
		Argv: []string{"/bin/sh", "-c", "for i in 1 2 3; do echo line$i; sleep 0.15; done"},
	})
	defer stream.Close()

	br := bufio.NewReader(stream)
	// First line should arrive well before the command finishes (~0.45s total).
	first := make(chan string, 1)
	go func() {
		for {
			f, err := execproto.ReadFrame(br)
			if err != nil {
				return
			}
			if f.Tag == execproto.TagStdout && len(f.Data) > 0 {
				first <- string(f.Data)
				return
			}
		}
	}()
	select {
	case got := <-first:
		if got == "" {
			t.Fatal("empty first chunk")
		}
	case <-time.After(400 * time.Millisecond):
		t.Fatal("no output within 400ms — not streaming incrementally")
	}
}

// TestExecSignalTerminates models KillBash: a long-running command is stopped by
// forwarding SIGTERM through the exec stream, and the exit frame reflects it.
func TestExecSignalTerminates(t *testing.T) {
	stream := startExec(t, &execproto.SpawnRequest{
		// Sleeps 30s unless signalled; traps TERM to exit 42 promptly.
		Argv: []string{"/bin/sh", "-c", "trap 'exit 42' TERM; sleep 30"},
	})
	defer stream.Close()

	// Give it a moment to install the trap, then forward SIGTERM (15).
	time.Sleep(200 * time.Millisecond)
	if err := execproto.WriteSignal(stream, 15); err != nil {
		t.Fatal(err)
	}

	code := int32(-999)
	done := make(chan struct{})
	go func() {
		br := bufio.NewReader(stream)
		for {
			f, err := execproto.ReadFrame(br)
			if err != nil {
				close(done)
				return
			}
			if f.Tag == execproto.TagExit {
				code = f.ExitCode()
				close(done)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("command not terminated by forwarded signal within 5s")
	}
	if code != 42 {
		t.Fatalf("exit code = %d, want 42 (TERM trap)", code)
	}
}

// startExec dials a fresh executor over a unix socket and opens an exec stream
// with req already sent.
func startExec(t *testing.T, req *execproto.SpawnRequest) transport.Stream {
	t.Helper()
	sockDir, err := os.MkdirTemp("/tmp", "rccbg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "e.sock")
	ln, err := transport.ListenUnix(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go New(ln, nil).Serve()

	d := transport.NewUnixDialer(sock)
	var stream transport.Stream
	for i := 0; i < 50; i++ {
		if s, e := d.Dial(t.Context()); e == nil {
			stream = s
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if stream == nil {
		t.Fatal("dial executor failed")
	}
	if _, err := stream.Write([]byte{StreamKindExec}); err != nil {
		t.Fatal(err)
	}
	if err := execproto.WriteSpawnRequest(stream, req); err != nil {
		t.Fatal(err)
	}
	return stream
}
