package client

import (
	"net/http"
	"testing"
)

func TestDiscover(t *testing.T) {
	c, err := Discover("http://a:1,http://b:2", "", 0, http.DefaultClient)
	if err != nil || len(c.static) != 2 || c.dnsHost != "" {
		t.Fatalf("static: %+v %v", c, err)
	}
	c, err = Discover("", "kv.raftlab.svc", 8080, http.DefaultClient)
	if err != nil || c.dnsHost != "kv.raftlab.svc" || c.dnsPort != 8080 {
		t.Fatalf("dns: %+v %v", c, err)
	}
	if _, err := Discover("", "", 0, http.DefaultClient); err == nil {
		t.Fatal("neither --nodes nor --service: want error")
	}
	if _, err := Discover("http://a:1", "kv", 8080, http.DefaultClient); err == nil {
		t.Fatal("both --nodes and --service: want error")
	}
}
