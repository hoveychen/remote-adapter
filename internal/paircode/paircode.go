// Package paircode encodes a libp2p peer identity (PeerID + listen multiaddrs)
// into a single copy-pasteable pairing code and back. `rca serve` prints the
// code; `rca <command> --code <code>` decodes it into the peer.AddrInfo to dial.
//
// The code is self-contained — no rendezvous infrastructure. Format:
//
//	"rca1." + base64url( uvarint(len(peerID)) peerID
//	                     uvarint(n) { uvarint(len(addr)) addr-bytes }*n )
//
// peerID is the binary multihash ([]byte of peer.ID); each addr is a binary
// multiaddr. base64url is unpadded (RFC 4648 §5), so the code survives shells,
// URLs, and double-click selection. The "rca1" prefix versions the format.
package paircode

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// Prefix identifies (and versions) a pairing code.
const Prefix = "rca1."

// Encode packs info into a pairing code. The AddrInfo must carry a valid ID;
// addrs are optional (an empty set still yields a valid code, though dialing
// then relies on other discovery).
func Encode(info peer.AddrInfo) (string, error) {
	if err := info.ID.Validate(); err != nil {
		return "", fmt.Errorf("paircode: invalid peer ID: %w", err)
	}
	var buf []byte
	buf = binary.AppendUvarint(buf, uint64(len(info.ID)))
	buf = append(buf, []byte(info.ID)...)
	buf = binary.AppendUvarint(buf, uint64(len(info.Addrs)))
	for _, a := range info.Addrs {
		ab := a.Bytes()
		buf = binary.AppendUvarint(buf, uint64(len(ab)))
		buf = append(buf, ab...)
	}
	return Prefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// Decode unpacks a pairing code produced by Encode.
func Decode(code string) (peer.AddrInfo, error) {
	var info peer.AddrInfo
	code = strings.TrimSpace(code)
	if !strings.HasPrefix(code, Prefix) {
		return info, fmt.Errorf("paircode: not a pairing code (want %q prefix)", Prefix)
	}
	buf, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(code, Prefix))
	if err != nil {
		return info, fmt.Errorf("paircode: decode base64: %w", err)
	}

	idBytes, buf, err := readChunk(buf)
	if err != nil {
		return info, fmt.Errorf("paircode: peer ID: %w", err)
	}
	id, err := peer.IDFromBytes(idBytes)
	if err != nil {
		return info, fmt.Errorf("paircode: peer ID: %w", err)
	}
	info.ID = id

	n, k := binary.Uvarint(buf)
	if k <= 0 {
		return peer.AddrInfo{}, errors.New("paircode: truncated addr count")
	}
	buf = buf[k:]
	for i := uint64(0); i < n; i++ {
		var ab []byte
		if ab, buf, err = readChunk(buf); err != nil {
			return peer.AddrInfo{}, fmt.Errorf("paircode: addr %d: %w", i, err)
		}
		ma, err := multiaddr.NewMultiaddrBytes(ab)
		if err != nil {
			return peer.AddrInfo{}, fmt.Errorf("paircode: addr %d: %w", i, err)
		}
		info.Addrs = append(info.Addrs, ma)
	}
	if len(buf) != 0 {
		return peer.AddrInfo{}, errors.New("paircode: trailing bytes")
	}
	return info, nil
}

// readChunk pops one uvarint-length-prefixed chunk off buf.
func readChunk(buf []byte) (chunk, rest []byte, err error) {
	n, k := binary.Uvarint(buf)
	if k <= 0 {
		return nil, nil, errors.New("truncated length")
	}
	buf = buf[k:]
	if uint64(len(buf)) < n {
		return nil, nil, errors.New("truncated payload")
	}
	return buf[:n], buf[n:], nil
}
