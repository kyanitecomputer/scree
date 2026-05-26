package testflash

import (
	"errors"
	"fmt"
	"sync"

	"src.kyanite.computer/scree/driver"
)

var (
	// ErrOutOfRange reports access outside the simulated flash.
	ErrOutOfRange = errors.New("testflash: out of range")

	// ErrUnaligned reports an operation that violates simulated flash alignment.
	ErrUnaligned = errors.New("testflash: unaligned operation")

	// ErrNotErased reports a write that would set a flash bit from 0 to 1.
	ErrNotErased = errors.New("testflash: target is not erased")

	// ErrInjected reports an injected write failure.
	ErrInjected = errors.New("testflash: injected failure")
)

// NOR is an in-memory NOR flash simulator.
type NOR struct {
	mu          sync.Mutex
	geometry    driver.Geometry
	data        []byte
	eraseCounts []uint64
	failWrites  int
}

// NewNOR returns an erased in-memory NOR flash simulator.
func NewNOR(geometry driver.Geometry) (*NOR, error) {
	if err := geometry.Validate(); err != nil {
		return nil, fmt.Errorf("validate geometry: %w", err)
	}
	data := make([]byte, geometry.TotalSize)
	for i := range data {
		data[i] = geometry.EraseValue
	}
	return &NOR{
		geometry:    geometry,
		data:        data,
		eraseCounts: make([]uint64, int(geometry.TotalSize)/geometry.EraseBlockSize),
		failWrites:  -1,
	}, nil
}

// Geometry returns the simulated flash geometry.
func (n *NOR) Geometry() driver.Geometry {
	return n.geometry
}

// ReadAt reads bytes from the simulated flash.
func (n *NOR) ReadAt(dst []byte, addr int64) (int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if addr < 0 || addr+int64(len(dst)) > int64(len(n.data)) {
		return 0, ErrOutOfRange
	}
	copy(dst, n.data[addr:addr+int64(len(dst))])
	return len(dst), nil
}

// WriteAt programs bytes to the simulated flash.
func (n *NOR) WriteAt(src []byte, addr int64) (int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.failWrites == 0 {
		return 0, ErrInjected
	}
	if n.failWrites > 0 {
		n.failWrites--
	}

	if addr < 0 || addr+int64(len(src)) > int64(len(n.data)) {
		return 0, ErrOutOfRange
	}
	if addr%int64(n.geometry.PageSize) != 0 || len(src)%n.geometry.PageSize != 0 {
		return 0, ErrUnaligned
	}
	start := int(addr)
	for i, b := range src {
		old := n.data[start+i]
		if old&b != b {
			return 0, ErrNotErased
		}
	}
	for i, b := range src {
		n.data[start+i] &= b
	}
	return len(src), nil
}

// FailAfterWrites injects a failure after allowed successful writes.
func (n *NOR) FailAfterWrites(allowed int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.failWrites = allowed
}

// ClearFailures disables injected failures.
func (n *NOR) ClearFailures() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.failWrites = -1
}

// CorruptByte clears bits at addr without enforcing alignment.
func (n *NOR) CorruptByte(addr int64, mask byte) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if addr < 0 || addr >= int64(len(n.data)) {
		return ErrOutOfRange
	}
	n.data[addr] &= mask
	return nil
}

// EraseBlock erases the block containing addr.
func (n *NOR) EraseBlock(addr int64) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if addr < 0 || addr >= int64(len(n.data)) {
		return ErrOutOfRange
	}
	if addr%int64(n.geometry.EraseBlockSize) != 0 {
		return ErrUnaligned
	}
	block := int(addr) / n.geometry.EraseBlockSize
	start := block * n.geometry.EraseBlockSize
	end := start + n.geometry.EraseBlockSize
	for i := start; i < end; i++ {
		n.data[i] = n.geometry.EraseValue
	}
	n.eraseCounts[block]++
	return nil
}

// EraseCount returns the number of erases for block.
func (n *NOR) EraseCount(block int) (uint64, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if block < 0 || block >= len(n.eraseCounts) {
		return 0, ErrOutOfRange
	}
	return n.eraseCounts[block], nil
}
