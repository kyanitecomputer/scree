package jetstream

import (
	"errors"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats-server/v2/server/gsl"

	"src.kyanite.computer/scree"
	"src.kyanite.computer/scree/blkdev"
	"src.kyanite.computer/scree/driver"
	"src.kyanite.computer/scree/internal/testflash"
)

func TestStreamStoreStoreLoad(t *testing.T) {
	store := newTestStreamStore(t)
	seq, ts, err := store.StoreMsg("foo", []byte("h"), []byte("hello"), 0)
	if err != nil {
		t.Fatalf("StoreMsg() error = %v", err)
	}
	if seq != 1 || ts == 0 {
		t.Fatalf("StoreMsg() = %d, %d, want seq 1 and timestamp", seq, ts)
	}

	msg, err := store.LoadMsg(seq, nil)
	if err != nil {
		t.Fatalf("LoadMsg() error = %v", err)
	}
	if msg.Subject() != "foo" || string(msg.Header()) != "h" || string(msg.Message()) != "hello" || msg.Sequence() != 1 {
		t.Fatalf("LoadMsg() = subject %q header %q message %q seq %d", msg.Subject(), msg.Header(), msg.Message(), msg.Sequence())
	}
}

func TestStreamStoreStateAfterRemove(t *testing.T) {
	store := newTestStreamStore(t)
	if _, _, err := store.StoreMsg("foo", nil, []byte("one"), 0); err != nil {
		t.Fatalf("StoreMsg(one) error = %v", err)
	}
	if _, _, err := store.StoreMsg("foo", nil, []byte("two"), 0); err != nil {
		t.Fatalf("StoreMsg(two) error = %v", err)
	}
	if ok, err := store.RemoveMsg(1); err != nil || !ok {
		t.Fatalf("RemoveMsg() = %v, %v, want true, nil", ok, err)
	}
	state := store.State()
	if state.Msgs != 1 || state.FirstSeq != 2 || state.LastSeq != 2 {
		t.Fatalf("State() = %+v, want one live message at seq 2", state)
	}
}

func TestStreamStoreConcurrentUse(t *testing.T) {
	store := newTestStreamStore(t)
	cs, err := store.ConsumerStore("c", time.Now(), &server.ConsumerConfig{})
	if err != nil {
		t.Fatalf("ConsumerStore() error = %v", err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for i := range 10 {
				if _, _, err := store.StoreMsg("foo", nil, []byte("x"), 0); err != nil {
					t.Errorf("StoreMsg() error = %v", err)
					return
				}
				cs.UpdateDelivered(uint64(i), uint64(i), 1, 0)
				cs.State()
			}
		})
		wg.Go(func() {
			for range 10 {
				store.RegisterStorageUpdates(func(int64, int64, uint64, string) {})
				store.State()
			}
		})
	}
	wg.Wait()
	if state := store.State(); state.Msgs != 40 {
		t.Fatalf("State().Msgs = %d, want 40", state.Msgs)
	}
}

func newTestStreamStore(t *testing.T) *streamStore {
	t.Helper()
	dev := newTestDevice(t)
	if err := scree.Format(dev, scree.FormatOptions{}); err != nil {
		t.Fatalf("scree.Format() error = %v", err)
	}
	store, err := scree.Mount(dev, scree.MountOptions{})
	if err != nil {
		t.Fatalf("scree.Mount() error = %v", err)
	}
	ns, err := store.CreateNamespace("test")
	if err != nil {
		t.Fatalf("CreateNamespace() error = %v", err)
	}
	return &streamStore{storage: server.StorageType(1000), store: store, ns: ns}
}

func newTestDevice(t *testing.T) blkdev.BlockDevice {
	t.Helper()
	flash, err := testflash.NewNOR(driver.Geometry{TotalSize: 16 * 4096, EraseBlockSize: 4096, PageSize: 256, EraseValue: 0xff})
	if err != nil {
		t.Fatalf("testflash.NewNOR() error = %v", err)
	}
	dev, err := blkdev.NewNOR(flash)
	if err != nil {
		t.Fatalf("blkdev.NewNOR() error = %v", err)
	}
	return dev
}

func TestStreamStoreDelete(t *testing.T) {
	dev := newTestDevice(t)
	if err := scree.Format(dev, scree.FormatOptions{}); err != nil {
		t.Fatalf("scree.Format() error = %v", err)
	}
	store, err := scree.Mount(dev, scree.MountOptions{})
	if err != nil {
		t.Fatalf("scree.Mount() error = %v", err)
	}
	ns, err := streamNamespace(store, &server.StreamConfig{Name: "TEST"})
	if err != nil {
		t.Fatalf("streamNamespace() error = %v", err)
	}
	ss := &streamStore{storage: server.StorageType(1000), store: store, ns: ns}
	if _, _, err := ss.StoreMsg("foo", nil, []byte("x"), 0); err != nil {
		t.Fatalf("StoreMsg() error = %v", err)
	}
	if err := ss.Delete(true); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := ss.Stop(); err != nil {
		t.Fatalf("Stop() after Delete error = %v", err)
	}
	reopened, err := scree.Mount(dev, scree.MountOptions{})
	if err != nil {
		t.Fatalf("scree.Mount() reopened error = %v", err)
	}
	if _, ok, _ := reopened.LookupNamespace("TEST"); ok {
		t.Fatal("namespace TEST still exists after Delete")
	}
}

func TestStreamStoreRawAndSkip(t *testing.T) {
	store := newTestStreamStore(t)
	if err := store.StoreRawMsg("foo", nil, []byte("one"), 2, 1, 0, false); !errors.Is(err, server.ErrSequenceMismatch) {
		t.Fatalf("StoreRawMsg(2) error = %v, want %v", err, server.ErrSequenceMismatch)
	}
	if state := store.State(); state.LastSeq != 0 {
		t.Fatalf("StoreRawMsg(2) stored a message: %+v", state)
	}
	if err := store.StoreRawMsg("foo", nil, []byte("one"), 1, 1, 0, false); err != nil {
		t.Fatalf("StoreRawMsg(1) error = %v", err)
	}
	if seq, err := store.SkipMsg(2); err != nil || seq != 2 {
		t.Fatalf("SkipMsg(2) = %d, %v, want 2, nil", seq, err)
	}
	if _, err := store.SkipMsg(5); !errors.Is(err, server.ErrSequenceMismatch) {
		t.Fatalf("SkipMsg(5) error = %v, want %v", err, server.ErrSequenceMismatch)
	}
	if err := store.SkipMsgs(3, 3); err != nil {
		t.Fatalf("SkipMsgs(3, 3) error = %v", err)
	}
	if seq, err := store.SkipMsg(0); err != nil || seq != 6 {
		t.Fatalf("SkipMsg(0) = %d, %v, want 6, nil", seq, err)
	}
	seq, _, err := store.StoreMsg("foo", nil, []byte("two"), 0)
	if err != nil || seq != 7 {
		t.Fatalf("StoreMsg() = %d, %v, want 7, nil", seq, err)
	}
	if state := store.State(); state.Msgs != 2 || state.FirstSeq != 1 || state.LastSeq != 7 {
		t.Fatalf("State() = %+v, want 2 messages from 1 to 7", state)
	}
}

// storeTestMsgs stores a message on each subject, in order.
func storeTestMsgs(t *testing.T, store *streamStore, subjects ...string) {
	t.Helper()
	for _, subject := range subjects {
		if _, _, err := store.StoreMsg(subject, nil, []byte("x"), 0); err != nil {
			t.Fatalf("StoreMsg(%q) error = %v", subject, err)
		}
	}
}

func TestStreamStorePurgeCompactTruncate(t *testing.T) {
	store := newTestStreamStore(t)
	var msgs, bytes int64
	store.RegisterStorageUpdates(func(m, b int64, _ uint64, _ string) { msgs += m; bytes += b })
	storeTestMsgs(t, store, "a", "b", "a", "b", "a")

	if n, err := store.Compact(3); err != nil || n != 2 {
		t.Fatalf("Compact(3) = %d, %v, want 2, nil", n, err)
	}
	if err := store.Truncate(4); err != nil {
		t.Fatalf("Truncate(4) error = %v", err)
	}
	if state := store.State(); state.Msgs != 2 || state.FirstSeq != 3 || state.LastSeq != 4 {
		t.Fatalf("State() after Compact(3) and Truncate(4) = %+v, want 3 to 4", state)
	}
	if msgs != 2 || bytes != 2 {
		t.Fatalf("updates = %d messages, %d bytes, want 2, 2", msgs, bytes)
	}
	if n, err := store.Purge(); err != nil || n != 2 {
		t.Fatalf("Purge() = %d, %v, want 2, nil", n, err)
	}
	if n, err := store.Compact(10); err != nil || n != 0 {
		t.Fatalf("Compact(10) = %d, %v, want 0, nil", n, err)
	}
	if seq, _, err := store.StoreMsg("a", nil, nil, 0); err != nil || seq != 10 {
		t.Fatalf("StoreMsg() after Compact(10) = %d, %v, want 10, nil", seq, err)
	}
	if msgs != 1 {
		t.Fatalf("updates = %d messages, want 1", msgs)
	}
}

func TestStreamStorePurgeEx(t *testing.T) {
	store := newTestStreamStore(t)
	storeTestMsgs(t, store, "a.1", "b", "a.2", "a.1", "b", "a.3")

	if n, err := store.PurgeEx("a.*", 0, 1); err != nil || n != 3 {
		t.Fatalf("PurgeEx(a.*, keep 1) = %d, %v, want 3, nil", n, err)
	}
	if n, err := store.PurgeEx("b", 5, 0); err != nil || n != 1 {
		t.Fatalf("PurgeEx(b, below 5) = %d, %v, want 1, nil", n, err)
	}
	var seqs []uint64
	for entry := range store.entries(0, math.MaxUint64) {
		seqs = append(seqs, entry.Sequence)
	}
	if !slices.Equal(seqs, []uint64{5, 6}) {
		t.Fatalf("sequences after PurgeEx = %v, want [5 6]", seqs)
	}
}

func TestStreamStoreLoadByFilter(t *testing.T) {
	store := newTestStreamStore(t)
	storeTestMsgs(t, store, "a.1", "b", "a.2", "c")

	sm, seq, err := store.LoadNextMsg("a.*", true, 2, nil)
	if err != nil || seq != 3 || sm.Subject() != "a.2" {
		t.Fatalf("LoadNextMsg(a.*, 2) = %q, %d, %v, want a.2, 3, nil", sm.Subject(), seq, err)
	}
	if _, seq, err := store.LoadNextMsg("a.*", true, 4, nil); !errors.Is(err, server.ErrStoreEOF) || seq != 4 {
		t.Fatalf("LoadNextMsg(a.*, 4) = %d, %v, want 4, %v", seq, err, server.ErrStoreEOF)
	}
	sm, seq, err = store.LoadPrevMsg("a.*", true, 2, nil)
	if err != nil || seq != 1 || sm.Subject() != "a.1" {
		t.Fatalf("LoadPrevMsg(a.*, 2) = %q, %d, %v, want a.1, 1, nil", sm.Subject(), seq, err)
	}
	if sm, err := store.LoadLastMsg("a.>", nil); err != nil || sm.Sequence() != 3 {
		t.Fatalf("LoadLastMsg(a.>) = %v, %v, want sequence 3", sm, err)
	}

	sl := gsl.NewSimpleSublist()
	if err := sl.Insert("c", struct{}{}); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	if err := sl.Insert("b", struct{}{}); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	if _, seq, err := store.LoadNextMsgMulti(sl, 3, nil); err != nil || seq != 4 {
		t.Fatalf("LoadNextMsgMulti(3) = %d, %v, want 4, nil", seq, err)
	}
	if _, seq, err := store.LoadPrevMsgMulti(sl, 3, nil); err != nil || seq != 2 {
		t.Fatalf("LoadPrevMsgMulti(3) = %d, %v, want 2, nil", seq, err)
	}
}
