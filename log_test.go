package scree

import (
	"math"
	"slices"
	"testing"
)

func newTestLog(seqs ...uint64) *entryLog {
	l := newEntryLog()
	for _, seq := range seqs {
		l.add(StoredEntry{Sequence: seq})
	}
	return l
}

func logSeqs(l *entryLog) []uint64 {
	var seqs []uint64
	for entry := range l.ascending() {
		seqs = append(seqs, entry.Sequence)
	}
	return seqs
}

func TestEntryLogFirst(t *testing.T) {
	l := newTestLog(1, 2, 3, 4, 5)
	l.remove(2)
	l.remove(1)
	if l.first != 3 {
		t.Fatalf("first after removing 1 and 2 = %d, want 3", l.first)
	}
	if n := l.removeUpTo(4); n != 2 || l.first != 5 {
		t.Fatalf("removeUpTo(4) = %d, first %d, want 2, 5", n, l.first)
	}
	if n := l.removeUpTo(math.MaxUint64); n != 1 || l.first != 0 || l.last != 5 {
		t.Fatalf("removeUpTo(max) = %d, first %d, last %d, want 1, 0, 5", n, l.first, l.last)
	}
}

func TestEntryLogSparse(t *testing.T) {
	const far = uint64(1) << 60
	l := newTestLog(1, far, far+2)
	if got, want := logSeqs(l), []uint64{1, far, far + 2}; !slices.Equal(got, want) {
		t.Fatalf("ascending() = %v, want %v", got, want)
	}
	l.remove(1)
	if l.first != far {
		t.Fatalf("first after removing 1 = %d, want %d", l.first, far)
	}
	if n := l.removeUpTo(far); n != 1 || l.first != far+2 {
		t.Fatalf("removeUpTo(%d) = %d, first %d, want 1, %d", far, n, l.first, far+2)
	}
}

func TestEntryLogNil(t *testing.T) {
	var l *entryLog
	if l.len() != 0 || l.firstSeq() != 0 || l.lastSeq() != 0 || l.removeUpTo(10) != 0 {
		t.Fatal("nil log is not empty")
	}
	if _, ok := l.get(1); ok {
		t.Fatal("nil log has an entry")
	}
	l.remove(1)
	if seqs := logSeqs(l); len(seqs) != 0 {
		t.Fatalf("ascending() of nil log = %v, want none", seqs)
	}
}
