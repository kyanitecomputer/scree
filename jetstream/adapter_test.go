package jetstream

import (
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"

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
