//go:build linux

package main

// rca _fuse — mounts the Linux lazy-slice FUSE filesystem (design doc §4.1.3 /
// §4.3). Reads are served one slice at a time from the adapter.
//
// With -root, the mount IS the remote directory: pass -mount and -root the same
// absolute path and every syscall the target makes — openat, stat, statx,
// getdents64, getcwd — resolves through one filesystem. Without it, the mount
// exposes a flat namespace of hex(path) entries for a caller that resolves
// routed absolute paths by hand.

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/hoveychen/remote-cc-adapter/internal/linuxfuse"
	"github.com/hoveychen/remote-cc-adapter/internal/transport"
)

func cmdFuse(args []string) int {
	fs := flag.NewFlagSet("rca _fuse", flag.ExitOnError)
	mount := fs.String("mount", "", "FUSE mount point (required)")
	root := fs.String("root", "", "remote directory this mount exposes; empty means hex-entry mode")
	direct := fs.Bool("direct-mount", false, "mount(2) directly instead of via fusermount3 (needs CAP_SYS_ADMIN)")
	adapterSock := fs.String("adapter-sock", "", "adapter fs IO-RPC unix socket (required)")
	_ = fs.Parse(args)

	if *mount == "" || *adapterSock == "" {
		log.Print("rca _fuse: -mount and -adapter-sock are required")
		return 2
	}
	if err := os.MkdirAll(*mount, 0o755); err != nil {
		log.Printf("rca _fuse: mkdir mount: %v", err)
		return 1
	}

	client := linuxfuse.NewClient(transport.NewUnixDialer(*adapterSock))
	var (
		server *fuse.Server
		err    error
	)
	if *root != "" {
		server, err = linuxfuse.MountRouted(*mount, *root, client, *direct)
	} else {
		server, err = linuxfuse.Mount(*mount, client)
	}
	if err != nil {
		log.Printf("rca _fuse: mount: %v", err)
		return 1
	}
	log.Printf("rca _fuse: mounted at %s (root %q, adapter %s)", *mount, *root, *adapterSock)

	// Unmount cleanly on signal.
	sigc := make(chan os.Signal, 2)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigc
		_ = server.Unmount()
	}()
	server.Wait()
	return 0
}
