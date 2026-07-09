package paircode

import (
	"crypto/rand"
	"strings"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

func testPeerID(t *testing.T) peer.ID {
	t.Helper()
	_, pub, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRoundTrip(t *testing.T) {
	id := testPeerID(t)
	addrs := []multiaddr.Multiaddr{
		multiaddr.StringCast("/ip4/192.168.1.7/tcp/4001"),
		multiaddr.StringCast("/ip4/203.0.113.9/udp/4001/quic-v1"),
		multiaddr.StringCast("/ip6/fe80::1/tcp/4001"),
	}
	code, err := Encode(peer.AddrInfo{ID: id, Addrs: addrs})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.HasPrefix(code, Prefix) {
		t.Fatalf("code %q lacks prefix %q", code, Prefix)
	}

	got, err := Decode(code)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.ID != id {
		t.Errorf("ID = %s, want %s", got.ID, id)
	}
	if len(got.Addrs) != len(addrs) {
		t.Fatalf("got %d addrs, want %d", len(got.Addrs), len(addrs))
	}
	for i := range addrs {
		if !got.Addrs[i].Equal(addrs[i]) {
			t.Errorf("addr[%d] = %s, want %s", i, got.Addrs[i], addrs[i])
		}
	}
}

func TestRoundTripNoAddrs(t *testing.T) {
	id := testPeerID(t)
	code, err := Encode(peer.AddrInfo{ID: id})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(code)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.ID != id || len(got.Addrs) != 0 {
		t.Errorf("got %v, want bare ID %s", got, id)
	}
}

func TestDecodeSurvivesWhitespace(t *testing.T) {
	id := testPeerID(t)
	code, _ := Encode(peer.AddrInfo{ID: id, Addrs: []multiaddr.Multiaddr{multiaddr.StringCast("/ip4/10.0.0.2/tcp/9")}})
	if _, err := Decode("  " + code + "\n"); err != nil {
		t.Fatalf("Decode with surrounding whitespace: %v", err)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	cases := []string{
		"",
		"rca1.",                  // empty payload
		"nope.abcdef",            // wrong prefix
		"/ip4/1.2.3.4/tcp/4001",  // raw multiaddr, not a code
		Prefix + "!!!not-b64!!!", // bad base64
		Prefix + "AA",            // truncated payload
	}
	for _, c := range cases {
		if _, err := Decode(c); err == nil {
			t.Errorf("Decode(%q) succeeded, want error", c)
		}
	}
}

func TestEncodeRejectsEmptyID(t *testing.T) {
	if _, err := Encode(peer.AddrInfo{}); err == nil {
		t.Error("Encode with empty ID succeeded, want error")
	}
}
