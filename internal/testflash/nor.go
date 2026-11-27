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
	tearKeep    int
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

	if !n.inRange(addr, len(dst)) {
		return 0, ErrOutOfRange
	}
	copy(dst, n.data[addr:addr+int64(len(dst))])
	return len(dst), nil
}

// WriteAt programs bytes to the simulated flash.
func (n *NOR) WriteAt(src []byte, addr int64) (int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.failWrites == 0 && n.tearKeep == 0 {
		return 0, ErrInjected
	}

	if !n.inRange(addr, len(src)) {
		return 0, ErrOutOfRange
	}
	if addr%int64(n.geometry.PageSize) != 0 || len(src)%n.geometry.PageSize != 0 {
		return 0, ErrUnaligned
	}
	torn := n.failWrites == 0
	if torn {
		src = src[:min(n.tearKeep, len(src))]
	} else if n.failWrites > 0 {
		n.failWrites--
	}
	// Programming moves bits away from the erase value; a bit already
	// programmed cannot return to it.
	start := int(addr)
	erased := n.geometry.EraseValue
	for i, b := range src {
		old := n.data[start+i]
		if (old^erased)&^(b^erased) != 0 {
			return 0, ErrNotErased
		}
	}
	for i, b := range src {
		n.data[start+i] = erased ^ (n.data[start+i] ^ erased | b ^ erased)
	}
	if torn {
		return 0, ErrInjected
	}
	return len(src), nil
}

// FailAfterWrites injects a failure after allowed successful writes.
func (n *NOR) FailAfterWrites(allowed int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.failWrites = allowed
}

// TearAfterWrites injects a failure after allowed successful writes, like
// FailAfterWrites, but the failing write programs its first keep bytes, as
// when power is lost while programming.
func (n *NOR) TearAfterWrites(allowed int, keep int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.failWrites = allowed
	n.tearKeep = keep
}

// ClearFailures disables injected failures.
func (n *NOR) ClearFailures() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.failWrites = -1
	n.tearKeep = 0
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

func (n *NOR) inRange(addr int64, size int) bool {
	return addr >= 0 && addr <= int64(len(n.data)) && int64(size) <= int64(len(n.data))-addr
}
