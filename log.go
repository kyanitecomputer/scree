package scree

import (
	"iter"
	"maps"
	"slices"
)

// entryLog holds the live entries of a namespace in memory. Its methods that
// only read accept a nil log, which is empty.
type entryLog struct {
	entries map[uint64]StoredEntry
	// first is the lowest live sequence, or 0 if there is none.
	first uint64
	// last is the highest sequence assigned.
	last uint64
}

func newEntryLog() *entryLog {
	return &entryLog{entries: make(map[uint64]StoredEntry)}
}

func (l *entryLog) len() int {
	if l == nil {
		return 0
	}
	return len(l.entries)
}

func (l *entryLog) get(seq uint64) (StoredEntry, bool) {
	if l == nil {
		return StoredEntry{}, false
	}
	entry, ok := l.entries[seq]
	return entry, ok
}

func (l *entryLog) firstSeq() uint64 {
	if l == nil {
		return 0
	}
	return l.first
}

func (l *entryLog) lastSeq() uint64 {
	if l == nil {
		return 0
	}
	return l.last
}

func (l *entryLog) add(entry StoredEntry) {
	l.entries[entry.Sequence] = entry
	l.last = max(l.last, entry.Sequence)
	if l.first == 0 || entry.Sequence < l.first {
		l.first = entry.Sequence
	}
}

func (l *entryLog) remove(seq uint64) {
	if l == nil {
		return
	}
	if _, ok := l.entries[seq]; !ok {
		return
	}
	delete(l.entries, seq)
	if seq == l.first {
		l.findFirst(seq + 1)
	}
}

// removeUpTo removes the entries up to seq and returns how many it removed.
func (l *entryLog) removeUpTo(seq uint64) uint64 {
	if l == nil || l.first == 0 || seq < l.first {
		return 0
	}
	var n uint64
	for cur := range l.entries {
		if cur <= seq {
			delete(l.entries, cur)
			n++
		}
	}
	if seq < l.last {
		l.findFirst(seq + 1)
	} else {
		l.first = 0
	}
	return n
}

// setLast removes the entries after seq, makes seq the last sequence and
// returns how many entries it removed.
func (l *entryLog) setLast(seq uint64) uint64 {
	var n uint64
	for cur := range l.entries {
		if cur > seq {
			delete(l.entries, cur)
			n++
		}
	}
	l.last = seq
	if l.first > seq {
		l.first = 0
	}
	return n
}

// findFirst sets first to the lowest live sequence from seq on. It walks the
// sequences while they are about as many as the entries, and else the entries.
func (l *entryLog) findFirst(seq uint64) {
	l.first = 0
	if len(l.entries) == 0 || seq > l.last {
		return
	}
	if l.last-seq <= 2*uint64(len(l.entries)) {
		for cur := seq; cur <= l.last; cur++ {
			if _, ok := l.entries[cur]; ok {
				l.first = cur
				return
			}
		}
		return
	}
	for cur := range l.entries {
		if cur >= seq && (l.first == 0 || cur < l.first) {
			l.first = cur
		}
	}
}

// ascending yields the live entries in sequence order.
func (l *entryLog) ascending() iter.Seq[StoredEntry] {
	return func(yield func(StoredEntry) bool) {
		if l.len() == 0 {
			return
		}
		if l.last-l.first <= 2*uint64(len(l.entries)) {
			for cur := l.first; cur <= l.last; cur++ {
				if entry, ok := l.entries[cur]; ok && !yield(entry) {
					return
				}
			}
			return
		}
		for _, cur := range slices.Sorted(maps.Keys(l.entries)) {
			if !yield(l.entries[cur]) {
				return
			}
		}
	}
}
