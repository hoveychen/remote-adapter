// Command rcc-executor is the remote sidecar. It runs inside the sandbox and
// executes filesystem and subprocess operations forwarded by the brain-side
// adapter. See design doc §3.1 component 3.
//
// The first cut listens on a local Unix socket (the transport the brain and
// sidecar share when co-located). The go-libp2p transport (design doc §3.3) is
// a placeholder; see internal/transport.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/hoveychen/remote-cc-adapter/internal/executor"
	"github.com/hoveychen/remote-cc-adapter/internal/transport"
)

func main() {
	sock := flag.String("sock", "", "unix socket path to listen on (required)")
	flag.Parse()

	if *sock == "" {
		log.Fatal("rcc-executor: -sock is required")
	}

	logger := log.New(os.Stderr, "rcc-executor ", log.LstdFlags|log.Lmsgprefix)

	ln, err := transport.ListenUnix(*sock)
	if err != nil {
		log.Fatalf("rcc-executor: listen: %v", err)
	}
	defer ln.Close()

	exe := executor.New(ln, logger)
	logger.Printf("serving on %s", ln.Addr())
	if err := exe.Serve(); err != nil {
		log.Fatalf("rcc-executor: serve: %v", err)
	}
}
