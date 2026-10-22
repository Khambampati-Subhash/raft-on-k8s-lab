package node

import (
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/hashicorp/raft"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestDNSTransportAdvertisesHostname(t *testing.T) {
	port := freePort(t)
	advertise := fmt.Sprintf("localhost:%d", port)

	tr, err := NewDNSTransport(fmt.Sprintf("127.0.0.1:%d", port), advertise, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	if got := string(tr.LocalAddr()); got != advertise {
		t.Fatalf("LocalAddr = %q, want %q (must advertise the name, not an IP)", got, advertise)
	}

	// A second transport must reach the first by its hostname.
	other, err := NewDNSTransport("127.0.0.1:0", "localhost:1", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	go func() {
		rpc := <-tr.Consumer()
		rpc.Respond(&raft.AppendEntriesResponse{Success: true}, nil)
	}()
	var resp raft.AppendEntriesResponse
	err = other.AppendEntries("peer", raft.ServerAddress(advertise), &raft.AppendEntriesRequest{}, &resp)
	if err != nil || !resp.Success {
		t.Fatalf("AppendEntries via hostname: err=%v success=%v", err, resp.Success)
	}
}
