package node

import (
	"fmt"
	"io"
	"net"
	"time"

	"github.com/hashicorp/raft"
)

// NewDNSTransport returns a Raft TCP transport that listens on bind and
// advertises the host:port string advertise, which may be a DNS name.
//
// raft.NewTCPTransport only accepts an IP as the advertised address. In
// Kubernetes that would put the pod IP into the Raft configuration, and pod IPs
// change on every restart. Advertising the stable StatefulSet DNS name and
// resolving it on every dial keeps the member's address valid across restarts.
func NewDNSTransport(bind, advertise string, logOut io.Writer) (*raft.NetworkTransport, error) {
	if _, _, err := net.SplitHostPort(advertise); err != nil {
		return nil, fmt.Errorf("advertise address %q: %w", advertise, err)
	}
	l, err := net.Listen("tcp", bind)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", bind, err)
	}
	layer := &dnsStreamLayer{Listener: l, advertise: dnsAddr(advertise)}
	return raft.NewNetworkTransport(layer, 3, 10*time.Second, logOut), nil
}

type dnsAddr string

func (a dnsAddr) Network() string { return "tcp" }
func (a dnsAddr) String() string  { return string(a) }

type dnsStreamLayer struct {
	net.Listener
	advertise dnsAddr
}

// Addr is what the transport reports as LocalAddr: the advertised name.
func (s *dnsStreamLayer) Addr() net.Addr { return s.advertise }

// Dial resolves the address on every call, so a peer whose IP changed is found.
func (s *dnsStreamLayer) Dial(addr raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("tcp", string(addr), timeout)
}
