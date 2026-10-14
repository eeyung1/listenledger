package api

import (
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestULIDFormatAndPrefix(t *testing.T) {
	id, err := newID("txn")
	if err != nil {
		t.Fatalf("newID failed: %v", err)
	}
	if !strings.HasPrefix(id, "txn_") {
		t.Fatalf("expected txn_ prefix, got %s", id)
	}

	ulidPart := strings.TrimPrefix(id, "txn_")
	if len(ulidPart) != 26 {
		t.Fatalf("expected 26-char ULID part, got %d (%s)", len(ulidPart), ulidPart)
	}

	for _, ch := range ulidPart {
		if !strings.ContainsRune(crockford32, ch) {
			t.Fatalf("invalid Crockford base32 character in ULID: %c", ch)
		}
	}
}

func TestULIDChronologicalSortability(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 1, 10, 0, 1, 0, time.UTC)
	t3 := time.Date(2026, 1, 1, 10, 0, 2, 0, time.UTC)

	id1, _ := generateULID(t1)
	id2, _ := generateULID(t2)
	id3, _ := generateULID(t3)

	if !(id1 < id2 && id2 < id3) {
		t.Fatalf("expected chronological sortability: id1=%s id2=%s id3=%s", id1, id2, id3)
	}

	list := []string{id3, id1, id2}
	sort.Strings(list)
	if list[0] != id1 || list[1] != id2 || list[2] != id3 {
		t.Fatalf("sort order mismatch: %+v", list)
	}
}

func TestULIDMonotonicWithinSameMillisecond(t *testing.T) {
	fixedTime := time.Date(2026, 5, 10, 12, 0, 0, 123000000, time.UTC)

	id1, err := generateULID(fixedTime)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := generateULID(fixedTime)
	if err != nil {
		t.Fatal(err)
	}

	// In the exact same millisecond, monotonic entropy guarantees id1 < id2
	if !(id1 < id2) {
		t.Fatalf("expected monotonic ordering within same ms: id1=%s id2=%s", id1, id2)
	}
}

func TestULIDConcurrentUniqueness(t *testing.T) {
	const count = 1000
	var wg sync.WaitGroup
	ids := make([]string, count)

	wg.Add(count)
	for i := 0; i < count; i++ {
		go func(idx int) {
			defer wg.Done()
			id, err := newID("cap")
			if err != nil {
				t.Errorf("generate id: %v", err)
				return
			}
			ids[idx] = id
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool, count)
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate ID detected: %s", id)
		}
		seen[id] = true
	}
	if len(seen) != count {
		t.Fatalf("expected %d unique IDs, got %d", count, len(seen))
	}
}
