package gatesentry2storage

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func withTempStore(t *testing.T, encrypt bool) (*MapStore, func()) {
	t.Helper()
	dir := t.TempDir()
	orig := GSBASEDIR
	SetBaseDir(dir + string(os.PathSeparator))
	store := NewMapStore("testset", encrypt)
	return store, func() { SetBaseDir(orig) }
}

func TestUpdateGetRoundTrip(t *testing.T) {
	s, cleanup := withTempStore(t, false)
	defer cleanup()

	s.Update("dns_resolver", "8.8.8.8:53")
	if got := s.Get("dns_resolver"); got != "8.8.8.8:53" {
		t.Fatalf("Get = %q, want 8.8.8.8:53", got)
	}
}

func TestSetDefaultDoesNotOverwrite(t *testing.T) {
	s, cleanup := withTempStore(t, false)
	defer cleanup()

	s.SetDefault("k", "first")
	s.SetDefault("k", "second")
	if got := s.Get("k"); got != "first" {
		t.Fatalf("SetDefault overwrote: got %q", got)
	}
}

func TestUpdateDoesNotDeadlockOnCorruptDisk(t *testing.T) {
	s, cleanup := withTempStore(t, false)
	defer cleanup()

	s.Update("ok", "1")

	// Corrupt the on-disk file, then Reload — in-memory Update must still unlock.
	path := filepath.Join(GSBASEDIR, "testset")
	if err := os.WriteFile(path, []byte("not-json{{{"), 0644); err != nil {
		t.Fatal(err)
	}
	s.Reload()

	done := make(chan struct{})
	go func() {
		s.Update("after", "2")
		close(done)
	}()

	select {
	case <-done:
	default:
	}
	// Second Update must not block if the first left the mutex held.
	s.Update("after2", "3")
	if s.Get("after2") != "3" {
		t.Fatalf("expected after2=3 after corrupt reload, got %q", s.Get("after2"))
	}
}

func TestConcurrentGetUpdate(t *testing.T) {
	s, cleanup := withTempStore(t, false)
	defer cleanup()
	s.Update("n", "0")

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			s.Update("n", strconv.Itoa(i))
		}(i)
		go func() {
			defer wg.Done()
			_ = s.Get("n")
			_ = s.GetInt("n")
		}()
	}
	wg.Wait()
}

func TestReloadKeepsSamePointer(t *testing.T) {
	s, cleanup := withTempStore(t, false)
	defer cleanup()
	s.Update("a", "1")

	ptr := s
	s.Reload()
	if ptr != s {
		t.Fatal("Reload must not replace the MapStore pointer")
	}
	if s.Get("a") != "1" {
		t.Fatalf("Reload lost value, got %q", s.Get("a"))
	}
}
