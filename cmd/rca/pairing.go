package main

import (
	"fmt"
	"log"
	"os"

	"github.com/hoveychen/remote-cc-adapter/internal/paircode"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// printPairingCode prints the copy-pasteable code the local side passes as
// --code. The code packs the host's PeerID plus its dialable listen addrs
// (loopback is dropped unless it is all we have, so co-located testing still
// works). Falls back to raw --peer multiaddrs if encoding ever fails.
func printPairingCode(h host.Host, logger *log.Logger) {
	addrs := h.Addrs()
	var dialable []multiaddr.Multiaddr
	for _, a := range addrs {
		if !manet.IsIPLoopback(a) {
			dialable = append(dialable, a)
		}
	}
	if len(dialable) == 0 {
		dialable = addrs
	}
	code, err := paircode.Encode(peer.AddrInfo{ID: h.ID(), Addrs: dialable})
	if err != nil {
		logger.Printf("pairing code: %v; dial with --peer instead:", err)
		suffix := "/p2p/" + h.ID().String()
		for _, a := range addrs {
			logger.Printf("  --peer %s%s", a, suffix)
		}
		return
	}
	fmt.Fprintf(os.Stdout, "pairing code:\n\n  %s\n\nrun on the local machine:\n\n  rca <command> --code %s\n\n", code, code)
}
