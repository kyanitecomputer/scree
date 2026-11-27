package scree

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentUse(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{JournalBlocks: 12})
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	ns := createNamespace(t, store, "test")
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Go(func() {
			for i := range 20 {
				if _, err := store.Append(ns, &Entry{Subject: fmt.Sprint(w)}); err != nil {
					t.Errorf("Append() error = %v", err)
					return
				}
				if err := store.PutMeta(ns, fmt.Sprint(w), []byte{byte(i)}); err != nil {
					t.Errorf("PutMeta() error = %v", err)
					return
				}
			}
		})
		wg.Go(func() {
			for range 20 {
				store.FirstSequence(ns)
				store.LoadEntryBySubject(ns, fmt.Sprint(w), 1)
				store.MetaKeys(ns)
				store.ListNamespaces()
			}
		})
	}
	wg.Wait()
	if last, err := store.LastSequence(ns); err != nil || last != 80 {
		t.Fatalf("LastSequence() = %d, %v, want 80, nil", last, err)
	}
	reopened, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() reopened error = %v", err)
	}
	if last, err := reopened.LastSequence(ns); err != nil || last != 80 {
		t.Fatalf("LastSequence() after remount = %d, %v, want 80, nil", last, err)
	}
}
