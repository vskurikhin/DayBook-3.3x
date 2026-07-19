package config

import (
	"net"
	"os"
	"testing"
)

func TestParsePeerAddress(t *testing.T) {
	// with rpc:// prefix
	addr := parsePeerAddress("rpc://127.0.0.1:9990")
	if addr.Network() != "tcp" {
		t.Errorf("parsePeerAddress('rpc://...') network = %s, want tcp", addr.Network())
	}
	if addr.String() != "127.0.0.1:9990" {
		t.Errorf("parsePeerAddress('rpc://...') addr = %s, want 127.0.0.1:9990", addr.String())
	}

	// without prefix (backward compatibility)
	addr2 := parsePeerAddress("127.0.0.1:9991")
	if addr2.Network() != "tcp" {
		t.Errorf("parsePeerAddress('host:port') network = %s, want tcp", addr2.Network())
	}
	if addr2.String() != "127.0.0.1:9991" {
		t.Errorf("parsePeerAddress('host:port') addr = %s, want 127.0.0.1:9991", addr2.String())
	}
}

func TestAddrAppendWithPrefix(t *testing.T) {
	peers := make(map[int]net.Addr)
	peers = addrAppend(peers, 0, "rpc://127.0.0.1:9990")
	if len(peers) != 1 {
		t.Fatalf("len = %d, want 1", len(peers))
	}
	if peers[0].String() != "127.0.0.1:9990" {
		t.Errorf("peers[0] = %s, want 127.0.0.1:9990", peers[0].String())
	}
}

func TestAddrAppendWithoutPrefix(t *testing.T) {
	peers := make(map[int]net.Addr)
	peers = addrAppend(peers, 1, "127.0.0.1:9991")
	if len(peers) != 1 {
		t.Fatalf("len = %d, want 1", len(peers))
	}
	if peers[1].String() != "127.0.0.1:9991" {
		t.Errorf("peers[1] = %s, want 127.0.0.1:9991", peers[1].String())
	}
}

func TestAddrAppendTrimmed(t *testing.T) {
	peers := make(map[int]net.Addr)
	peers = addrAppend(peers, 0, "  rpc://127.0.0.1:9990  ")
	if len(peers) != 1 {
		t.Fatalf("len = %d, want 1", len(peers))
	}
	if peers[0].String() != "127.0.0.1:9990" {
		t.Errorf("peers[0] = %s, want 127.0.0.1:9990", peers[0].String())
	}
}

func TestParsePeers(t *testing.T) {
	peers := make(map[int]net.Addr)
	peers = parsePeers(peers, "0=127.0.0.1:9990,1=127.0.0.1:9991")
	if len(peers) != 2 {
		t.Fatalf("len = %d, want 2", len(peers))
	}
	if peers[0].String() != "127.0.0.1:9990" {
		t.Errorf("peers[0] = %s, want 127.0.0.1:9990", peers[0].String())
	}
	if peers[1].String() != "127.0.0.1:9991" {
		t.Errorf("peers[1] = %s, want 127.0.0.1:9991", peers[1].String())
	}
}

func TestParsePeersSingle(t *testing.T) {
	peers := make(map[int]net.Addr)
	peers = parsePeers(peers, "0=127.0.0.1:9990")
	if len(peers) != 1 {
		t.Fatalf("len = %d, want 1", len(peers))
	}
	if peers[0].String() != "127.0.0.1:9990" {
		t.Errorf("peers[0] = %s, want 127.0.0.1:9990", peers[0].String())
	}
}

func TestParseFlags(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{"raft", "-rpc-addr", ":9999", "-number", "0", "-peers", "1=127.0.0.1:9991"}

	v := ParseFlags()
	if v.Number != 0 {
		t.Errorf("Number = %d, want 0", v.Number)
	}
	if v.RPCAddress.String() != ":9999" &&
		v.RPCAddress.String() != "0.0.0.0:9999" &&
		v.RPCAddress.String() != "[::]:9999" {
		t.Errorf("Address = %s, want :9999, 0.0.0.0:9999, or [::]:9999", v.RPCAddress.String())
	}
	if len(v.Peers) != 1 {
		t.Errorf("len(Peers) = %d, want 1", len(v.Peers))
	}
	if v.Peers[1].String() != "127.0.0.1:9991" {
		t.Errorf("Peers[1] = %s, want 127.0.0.1:9991", v.Peers[1].String())
	}
}

func TestParseFlagsNoPeers(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{"raft", "-rpc-addr", ":9990", "-number", "1"}

	v := ParseFlags()
	if v.Number != 1 {
		t.Errorf("Number = %d, want 1", v.Number)
	}
	if v.RPCAddress.String() != ":9990" &&
		v.RPCAddress.String() != "0.0.0.0:9990" &&
		v.RPCAddress.String() != "[::]:9990" {
		t.Errorf("Address = %s, want :9990, 0.0.0.0:9990, or [::]:9990", v.RPCAddress.String())
	}
	if len(v.Peers) != 0 {
		t.Errorf("len(Peers) = %d, want 0", len(v.Peers))
	}
}

func TestParseFlagsDefaultAddr(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{"raft", "-number", "2"}

	v := ParseFlags()
	if v.Number != 2 {
		t.Errorf("Number = %d, want 2", v.Number)
	}
	if v.RPCAddress.String() != ":9990" &&
		v.RPCAddress.String() != "0.0.0.0:9990" &&
		v.RPCAddress.String() != "[::]:9990" {
		t.Errorf("Address = %s, want :9990", v.RPCAddress.String())
	}
}

func TestValuesImplements(t *testing.T) {
	v := Values{
		RPCAddress: mustParseAddr("127.0.0.1:9990"),
		Number:     0,
		Peers:      map[int]net.Addr{1: mustParseAddr("127.0.0.1:9991")},
	}
	if v.Number != 0 {
		t.Errorf("Number = %d, want 0", v.Number)
	}
	if v.RPCAddress.String() != "127.0.0.1:9990" {
		t.Errorf("Address = %s, want 127.0.0.1:9990", v.RPCAddress.String())
	}
	if len(v.Peers) != 1 {
		t.Errorf("len(Peers) = %d, want 1", len(v.Peers))
	}
}

func mustParseAddr(s string) net.Addr {
	addr, err := net.ResolveTCPAddr("tcp", s)
	if err != nil {
		panic(err)
	}
	return addr
}
