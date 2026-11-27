package scree

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"

	"src.kyanite.computer/scree/blkdev"
)

const (
	journalMagic = uint16(0x5c5e)

	journalOffsetMagic  = 0
	journalOffsetType   = 2
	journalOffsetLength = 4
	journalOffsetCRC    = 8
	journalOffsetData   = 12
)

type journal struct {
	dev       blkdev.BlockDevice
	start     int
	blocks    int
	writeUnit int
	nextUnit  int
}

// recordType identifies the kind of a journal record. The values are part of
// the on-flash format.
type recordType uint16

const (
	recordEntry              recordType = 1
	recordNamespaceCreate    recordType = 2
	recordNamespaceDelete    recordType = 3
	recordMetaPut            recordType = 4
	recordMetaDelete         recordType = 5
	recordEntryRemove        recordType = 6
	recordNamespacePurge     recordType = 7
	recordNamespacePurgeUpTo recordType = 8
	recordSetLastSequence    recordType = 9
)

type journalEntry struct {
	Type recordType
	Data []byte
}

// openJournal returns the journal of sb. It appends only after replay found
// the end of the journal.
func openJournal(dev blkdev.BlockDevice, sb superblock) *journal {
	return &journal{
		dev:       dev,
		start:     int(sb.JournalStart),
		blocks:    int(sb.JournalBlocks),
		writeUnit: unitSize(dev.WriteSize()),
	}
}

func (j *journal) append(entry journalEntry) error {
	if len(entry.Data) > j.writeUnit-journalOffsetData {
		return fmt.Errorf("%w: record of %d bytes exceeds the journal unit", ErrInvalidArgument, len(entry.Data))
	}
	if j.nextUnit >= j.capacityUnits() {
		return ErrNoSpace
	}
	buf := make([]byte, j.writeUnit)
	fillErased(buf, j.dev.EraseValue())
	binary.LittleEndian.PutUint16(buf[journalOffsetMagic:], journalMagic)
	binary.LittleEndian.PutUint16(buf[journalOffsetType:], uint16(entry.Type))
	binary.LittleEndian.PutUint32(buf[journalOffsetLength:], uint32(len(entry.Data)))
	binary.LittleEndian.PutUint32(buf[journalOffsetCRC:], 0)
	copy(buf[journalOffsetData:], entry.Data)
	binary.LittleEndian.PutUint32(buf[journalOffsetCRC:], journalCRC(buf, len(entry.Data)))
	block, off := j.location(j.nextUnit)
	// A failed write may have programmed part of the unit, which cannot be
	// programmed again before the block is erased.
	j.nextUnit++
	return j.dev.WriteAt(block, off, buf)
}

// replay reads the journal up to its first erased unit, where the next
// append goes. It returns the records of the units before and the number of
// them that hold no valid record: a write that power loss or an error cut
// short leaves such a unit, which is skipped.
func (j *journal) replay() (entries []journalEntry, bad int, err error) {
	buf := make([]byte, j.writeUnit)
	unit := 0
	for ; unit < j.capacityUnits(); unit++ {
		block, off := j.location(unit)
		if err := j.dev.ReadAt(block, off, buf); err != nil {
			return nil, 0, err
		}
		if isErased(buf, j.dev.EraseValue()) {
			break
		}
		entry, ok := j.decodeUnit(buf)
		if !ok {
			bad++
			continue
		}
		entries = append(entries, entry)
	}
	j.nextUnit = unit
	return entries, bad, nil
}

// checkErased reports ErrCorrupt if a unit after the end of the journal is
// programmed.
func (j *journal) checkErased() error {
	buf := make([]byte, j.writeUnit)
	for unit := j.nextUnit; unit < j.capacityUnits(); unit++ {
		block, off := j.location(unit)
		if err := j.dev.ReadAt(block, off, buf); err != nil {
			return err
		}
		if !isErased(buf, j.dev.EraseValue()) {
			return fmt.Errorf("%w: journal unit %d is programmed after the end of the journal", ErrCorrupt, unit)
		}
	}
	return nil
}

// decodeUnit returns the record of a programmed unit, if it holds a valid one.
func (j *journal) decodeUnit(buf []byte) (journalEntry, bool) {
	if binary.LittleEndian.Uint16(buf[journalOffsetMagic:]) != journalMagic {
		return journalEntry{}, false
	}
	length32 := binary.LittleEndian.Uint32(buf[journalOffsetLength:])
	if uint64(length32) > uint64(j.writeUnit-journalOffsetData) {
		return journalEntry{}, false
	}
	length := int(length32)
	if journalCRC(buf, length) != binary.LittleEndian.Uint32(buf[journalOffsetCRC:]) {
		return journalEntry{}, false
	}
	data := make([]byte, length)
	copy(data, buf[journalOffsetData:journalOffsetData+length])
	return journalEntry{Type: recordType(binary.LittleEndian.Uint16(buf[journalOffsetType:])), Data: data}, true
}

func journalCRC(buf []byte, length int) uint32 {
	crcBuf := make([]byte, journalOffsetData+length-journalOffsetType)
	copy(crcBuf, buf[journalOffsetType:journalOffsetData+length])
	binary.LittleEndian.PutUint32(crcBuf[journalOffsetCRC-journalOffsetType:], 0)
	return crc32.ChecksumIEEE(crcBuf)
}

func (j *journal) capacityUnits() int {
	return j.blocks * (j.dev.BlockSize() / j.writeUnit)
}

func (j *journal) location(unit int) (block int, off int) {
	unitsPerBlock := j.dev.BlockSize() / j.writeUnit
	return j.start + unit/unitsPerBlock, (unit % unitsPerBlock) * j.writeUnit
}

func fillErased(buf []byte, erased byte) {
	for i := range buf {
		buf[i] = erased
	}
}

func isErased(buf []byte, erased byte) bool {
	for _, b := range buf {
		if b != erased {
			return false
		}
	}
	return true
}
