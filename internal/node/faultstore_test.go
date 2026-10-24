package node

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

func TestFaultLogStoreDelays(t *testing.T) {
	f := NewFaultLogStore(raft.NewInmemStore())
	f.SetFault(50*time.Millisecond, 0)
	start := time.Now()
	if err := f.StoreLog(&raft.Log{Index: 1}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 50*time.Millisecond {
		t.Fatalf("StoreLog took %v, want >= 50ms", d)
	}
	start = time.Now()
	if err := f.StoreLogs([]*raft.Log{{Index: 2}, {Index: 3}}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 50*time.Millisecond || d > 95*time.Millisecond {
		t.Fatalf("StoreLogs took %v, want one delay per call (~50ms)", d)
	}
}

func TestFaultLogStoreFails(t *testing.T) {
	inner := raft.NewInmemStore()
	f := NewFaultLogStore(inner)
	f.Rand = func() float64 { return 0 }

	f.SetFault(0, 1)
	if err := f.StoreLog(&raft.Log{Index: 1}); !errors.Is(err, ErrInjectedIO) {
		t.Fatalf("err = %v, want ErrInjectedIO", err)
	}
	if err := f.StoreLogs([]*raft.Log{{Index: 1}}); !errors.Is(err, ErrInjectedIO) {
		t.Fatalf("StoreLogs err = %v, want ErrInjectedIO", err)
	}
	if last, _ := inner.LastIndex(); last != 0 {
		t.Fatalf("failed append reached the inner store (last index %d)", last)
	}

	f.SetFault(0, 0)
	if err := f.StoreLog(&raft.Log{Index: 1}); err != nil {
		t.Fatalf("passthrough err = %v", err)
	}
	if last, _ := inner.LastIndex(); last != 1 {
		t.Fatalf("last index %d, want 1", last)
	}
}

func TestFaultLogStoreSetFaultIsRaceFree(t *testing.T) {
	f := NewFaultLogStore(raft.NewInmemStore())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 200 {
			f.SetFault(time.Duration(i%2)*time.Microsecond, float64(i%2)/10)
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 200 {
			f.StoreLog(&raft.Log{Index: uint64(i + 1)})
		}
	}()
	wg.Wait()
}
