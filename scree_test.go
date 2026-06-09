package scree

import (
	"errors"
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

func TestMountAuthStrictRequiresKey(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{EnableAuth: true})
	_, err := Mount(dev, MountOptions{AuthFailureMode: AuthStrict})
	if !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("Mount() error = %v, want %v", err, ErrAuthenticationFailed)
	}
	if _, err := Mount(dev, MountOptions{AuthFailureMode: AuthReportOnly}); err != nil {
		t.Fatalf("Mount() report-only error = %v", err)
	}
}

func TestNilDevice(t *testing.T) {
	if err := Format(nil, FormatOptions{}); err != ErrUnsupportedFormat {
		t.Fatalf("Format(nil) error = %v, want %v", err, ErrUnsupportedFormat)
	}
	if _, err := Mount(nil, MountOptions{}); err != ErrUnsupportedFormat {
		t.Fatalf("Mount(nil) error = %v, want %v", err, ErrUnsupportedFormat)
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
