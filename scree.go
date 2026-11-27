package scree

import "src.kyanite.computer/scree/blkdev"

// Store is a mounted Scree volume.
type Store struct {
	dev        blkdev.BlockDevice
	super      superblock
	master     masterNode
	journal    *journal
	namespaces map[NamespaceID]string
	nsByName   map[string]NamespaceID
	nextNS     NamespaceID
	logs       map[NamespaceID]*entryLog
	meta       map[NamespaceID]map[string][]byte
	readOnly   bool
}

// Format initializes a Scree volume on a block device.
func Format(dev blkdev.BlockDevice, opts FormatOptions) error {
	if err := blkdev.Validate(dev); err != nil {
		return err
	}
	sb, err := newSuperblock(dev, opts)
	if err != nil {
		return err
	}
	if err := writeSuperblock(dev, 0, sb); err != nil {
		return err
	}
	if err := writeSuperblock(dev, 1, sb); err != nil {
		return err
	}
	master := newMasterNode(sb)
	if err := writeMaster(dev, 2, master); err != nil {
		return err
	}
	if err := writeMaster(dev, 3, master); err != nil {
		return err
	}
	for block := int(sb.JournalStart); block < int(sb.JournalStart+sb.JournalBlocks); block++ {
		if err := dev.EraseBlock(block); err != nil {
			return err
		}
	}
	if err := dev.Sync(); err != nil {
		return err
	}
	return nil
}

// Mount opens an existing Scree volume on a block device.
func Mount(dev blkdev.BlockDevice, opts MountOptions) (*Store, error) {
	if err := blkdev.Validate(dev); err != nil {
		return nil, err
	}
	if err := checkUnitSize(dev); err != nil {
		return nil, err
	}
	sb, err := mountSuperblock(dev)
	if err != nil {
		return nil, err
	}
	master, err := mountMaster(dev)
	if err != nil {
		return nil, err
	}
	if err := validateMaster(sb, master); err != nil {
		return nil, err
	}
	if sb.Flags&superFlagAuth != 0 && len(opts.AuthKey) == 0 && opts.AuthFailureMode == AuthStrict {
		return nil, ErrAuthenticationFailed
	}
	store := newStore(dev, sb, master)
	store.readOnly = opts.ReadOnly
	if err := store.replayEntries(); err != nil {
		return nil, err
	}
	return store, nil
}

func newStore(dev blkdev.BlockDevice, sb superblock, master masterNode) *Store {
	return &Store{
		dev:        dev,
		super:      sb,
		master:     master,
		journal:    openJournal(dev, sb),
		namespaces: make(map[NamespaceID]string),
		nsByName:   make(map[string]NamespaceID),
		nextNS:     1,
		logs:       make(map[NamespaceID]*entryLog),
		meta:       make(map[NamespaceID]map[string][]byte),
	}
}

// Stats returns storage statistics.
func (s *Store) Stats() StoreStats {
	return StoreStats{TotalBlocks: int(s.super.BlockCount), FreeBlocks: int(s.master.FreeBlocks)}
}

// StoreStats describes storage usage.
type StoreStats struct {
	TotalBlocks int
	FreeBlocks  int
}

// Close releases resources associated with the store.
func (s *Store) Close() error {
	return nil
}
