package scree

import (
	"errors"
	"math"
	"testing"

	"src.kyanite.computer/scree/blkdev"
	"src.kyanite.computer/scree/driver"
	"src.kyanite.computer/scree/internal/testflash"
)

type noopDevice struct{}

func (noopDevice) BlockSize() int                 { return 4096 }
func (noopDevice) BlockCount() int                { return 16 }
func (noopDevice) WriteSize() int                 { return 256 }
func (noopDevice) ReadAt(int, int, []byte) error  { return nil }
func (noopDevice) WriteAt(int, int, []byte) error { return nil }
func (noopDevice) EraseBlock(int) error           { return nil }
func (noopDevice) Sync() error                    { return nil }
func (noopDevice) Close() error                   { return nil }

func TestMountReadOnly(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{})
	store, err := Mount(dev, MountOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	if !store.readOnly {
		t.Fatalf("Mount() readOnly = false, want true")
	}
}

func TestFormatMount(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{JournalBlocks: 2})
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	if store.super.JournalBlocks != 2 {
		t.Fatalf("Mount() journal blocks = %d, want 2", store.super.JournalBlocks)
	}
	stats := store.Stats()
	if stats.TotalBlocks != 16 || stats.FreeBlocks != 10 {
		t.Fatalf("Stats() = %+v, want TotalBlocks 16 FreeBlocks 10", stats)
	}
}

func TestMountFallsBackToBackupMaster(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{})
	if err := dev.EraseBlock(2); err != nil {
		t.Fatalf("EraseBlock(2) error = %v", err)
	}
	if _, err := Mount(dev, MountOptions{}); err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
}

func TestMountUnformatted(t *testing.T) {
	dev := newDevice(t)
	_, err := Mount(dev, MountOptions{})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Mount() error = %v, want %v", err, ErrCorrupt)
	}
}

func TestAuthenticationIsRefused(t *testing.T) {
	if err := Format(newDevice(t), FormatOptions{EnableAuth: true}); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("Format(EnableAuth) error = %v, want %v", err, ErrUnsupportedFormat)
	}
	dev := newFormattedDevice(t, FormatOptions{})
	sb, err := readSuperblock(dev, 0)
	if err != nil {
		t.Fatalf("readSuperblock() error = %v", err)
	}
	sb.Flags |= superFlagAuth
	for block := range 2 {
		if err := writeSuperblock(dev, block, sb); err != nil {
			t.Fatalf("writeSuperblock(%d) error = %v", block, err)
		}
	}
	for _, opts := range []MountOptions{
		{AuthFailureMode: AuthStrict, AuthKey: []byte("key")},
		{AuthFailureMode: AuthReportOnly},
	} {
		if _, err := Mount(dev, opts); !errors.Is(err, ErrUnsupportedFormat) {
			t.Fatalf("Mount(%+v) error = %v, want %v", opts, err, ErrUnsupportedFormat)
		}
	}
}

func TestNilDevice(t *testing.T) {
	if err := Format(nil, FormatOptions{}); !errors.Is(err, blkdev.ErrInvalidGeometry) {
		t.Fatalf("Format(nil) error = %v, want %v", err, blkdev.ErrInvalidGeometry)
	}
	if _, err := Mount(nil, MountOptions{}); !errors.Is(err, blkdev.ErrInvalidGeometry) {
		t.Fatalf("Mount(nil) error = %v, want %v", err, blkdev.ErrInvalidGeometry)
	}
}

func newDevice(t *testing.T) blkdev.BlockDevice {
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

func newFormattedDevice(t *testing.T, opts FormatOptions) blkdev.BlockDevice {
	t.Helper()
	dev := newDevice(t)
	if err := Format(dev, opts); err != nil {
		t.Fatalf("Format() error = %v", err)
	}
	return dev
}

func TestSmallWriteSize(t *testing.T) {
	for _, pageSize := range []int{1, 8, 32} {
		flash, err := testflash.NewNOR(driver.Geometry{TotalSize: 16 * 4096, EraseBlockSize: 4096, PageSize: pageSize, EraseValue: 0xff})
		if err != nil {
			t.Fatalf("testflash.NewNOR() error = %v", err)
		}
		dev, err := blkdev.NewNOR(flash)
		if err != nil {
			t.Fatalf("blkdev.NewNOR() error = %v", err)
		}
		if err := Format(dev, FormatOptions{}); err != nil {
			t.Fatalf("Format(page %d) error = %v", pageSize, err)
		}
		store, err := Mount(dev, MountOptions{})
		if err != nil {
			t.Fatalf("Mount(page %d) error = %v", pageSize, err)
		}
		id, err := store.CreateNamespace("a")
		if err != nil {
			t.Fatalf("CreateNamespace(page %d) error = %v", pageSize, err)
		}
		reopened, err := Mount(dev, MountOptions{})
		if err != nil {
			t.Fatalf("Mount(page %d) reopened error = %v", pageSize, err)
		}
		if got, ok, _ := reopened.LookupNamespace("a"); !ok || got != id {
			t.Fatalf("LookupNamespace(page %d) = %d, %v, want %d, true", pageSize, got, ok, id)
		}
	}
}

func TestBlockSmallerThanUnit(t *testing.T) {
	flash, err := testflash.NewNOR(driver.Geometry{TotalSize: 16 * 32, EraseBlockSize: 32, PageSize: 32, EraseValue: 0xff})
	if err != nil {
		t.Fatalf("testflash.NewNOR() error = %v", err)
	}
	dev, err := blkdev.NewNOR(flash)
	if err != nil {
		t.Fatalf("blkdev.NewNOR() error = %v", err)
	}
	if err := Format(dev, FormatOptions{}); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("Format() error = %v, want %v", err, ErrUnsupportedFormat)
	}
	if _, err := Mount(dev, MountOptions{}); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("Mount() error = %v, want %v", err, ErrUnsupportedFormat)
	}
}

func TestFormatRejectsJournalLargerThanDevice(t *testing.T) {
	for _, blocks := range []int{-1, 13, math.MaxInt} {
		if err := Format(newDevice(t), FormatOptions{JournalBlocks: blocks}); !errors.Is(err, ErrUnsupportedFormat) {
			t.Fatalf("Format(JournalBlocks %d) error = %v, want %v", blocks, err, ErrUnsupportedFormat)
		}
	}
	if err := Format(newDevice(t), FormatOptions{JournalBlocks: 12}); err != nil {
		t.Fatalf("Format(JournalBlocks 12) error = %v", err)
	}
}

func TestMountRejectsJournalOutsideDevice(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{})
	sb, err := readSuperblock(dev, 0)
	if err != nil {
		t.Fatalf("readSuperblock() error = %v", err)
	}
	sb.JournalBlocks = math.MaxUint32
	for block := range 2 {
		if err := writeSuperblock(dev, block, sb); err != nil {
			t.Fatalf("writeSuperblock(%d) error = %v", block, err)
		}
	}
	if _, err := Mount(dev, MountOptions{}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Mount() error = %v, want %v", err, ErrCorrupt)
	}
}

var errTestIO = errors.New("test I/O error")

// failingReadDevice fails reads of the blocks in fail.
type failingReadDevice struct {
	blkdev.BlockDevice
	fail map[int]bool
}

func (d failingReadDevice) ReadAt(block int, off int, dst []byte) error {
	if d.fail[block] {
		return errTestIO
	}
	return d.BlockDevice.ReadAt(block, off, dst)
}

func TestMountReportsReadErrors(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{})
	if _, err := Mount(failingReadDevice{dev, map[int]bool{0: true, 1: true}}, MountOptions{}); !errors.Is(err, errTestIO) {
		t.Fatalf("Mount() with unreadable superblocks error = %v, want %v", err, errTestIO)
	}
	if _, err := Mount(failingReadDevice{dev, map[int]bool{2: true, 3: true}}, MountOptions{}); !errors.Is(err, errTestIO) {
		t.Fatalf("Mount() with unreadable masters error = %v, want %v", err, errTestIO)
	}
	if _, err := Mount(failingReadDevice{dev, map[int]bool{0: true, 2: true}}, MountOptions{}); err != nil {
		t.Fatalf("Mount() with readable backups error = %v", err)
	}
}

func createNamespace(t *testing.T, store *Store, name string) NamespaceID {
	t.Helper()
	ns, err := store.CreateNamespace(name)
	if err != nil {
		t.Fatalf("CreateNamespace(%q) error = %v", name, err)
	}
	return ns
}

// syncCountingDevice counts the syncs of its device.
type syncCountingDevice struct {
	blkdev.BlockDevice
	syncs *int
}

func (d syncCountingDevice) Sync() error {
	*d.syncs++
	return d.BlockDevice.Sync()
}

func TestClose(t *testing.T) {
	var syncs int
	dev := syncCountingDevice{newFormattedDevice(t, FormatOptions{}), &syncs}
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	ns := createNamespace(t, store, "test")
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if syncs != 1 {
		t.Fatalf("Close() synced %d times, want 1", syncs)
	}
	if err := store.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Close() again error = %v, want %v", err, ErrClosed)
	}
	if _, err := store.Append(ns, &Entry{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Append() after Close error = %v, want %v", err, ErrClosed)
	}
	if _, _, err := store.LookupNamespace("test"); !errors.Is(err, ErrClosed) {
		t.Fatalf("LookupNamespace() after Close error = %v, want %v", err, ErrClosed)
	}
	if _, err := Mount(dev, MountOptions{}); err != nil {
		t.Fatalf("Mount() after Close error = %v", err)
	}
}
