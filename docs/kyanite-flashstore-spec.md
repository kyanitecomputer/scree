# Kyanite FlashStore: Specification & Implementation Plan

**Version**: 0.1-draft
**Date**: 2026-05-18
**Target**: Bare-metal Go (TamaGo) on NOR flash (primary), NAND flash via Go Dhara port (secondary), eMMC (future)

---

## 1. Overview

Kyanite FlashStore is a log-structured, authenticated flash storage engine written in pure Go for bare-metal systems without an operating system. It is purpose-built to back NATS JetStream persistence (streams, KV store, object store) on raw flash hardware, with a design that can later be extended into a general-purpose filesystem.

### 1.1 Design Goals

| Priority | Goal | Rationale |
|----------|------|-----------|
| P0 | Power-loss atomicity | Single-node JetStream with no replication must not lose acknowledged data |
| P0 | Offline tamper detection | Merkle-authenticated index with HMAC root, keyed from platform RoT |
| P0 | Low write amplification | Flash lifetime on NOR (100K cycles) and NAND (3K–100K cycles) is finite |
| P0 | Bare-metal, pure Go | TamaGo on ARM/RISC-V, no cgo, no OS syscalls |
| P1 | Bounded, configurable RAM | Target 64–256 KB working set for M-class cores; scale up on A-class |
| P1 | Wear leveling | Built-in for NOR; delegated to Dhara FTL for NAND |
| P2 | Optional compression | LZ4 per data node, negotiated at format time |
| P2 | eMMC block device support | Future: thin adapter over the same logical block interface |

### 1.2 Non-Goals (Approach B scope)

- POSIX filesystem semantics (permissions, xattrs, symlinks, hardlinks)
- Directory hierarchy (flat namespace of named streams/buckets)
- Multi-process/multi-user access control
- Encryption at rest (may be added later; authentication is in scope)
- Network filesystem / remote mount

### 1.3 Prior Art & References

| System | What we borrow | What we avoid |
|--------|---------------|---------------|
| **UBIFS** (Linux, sigma-star authentication whitepaper) | Wandering B+ tree as Merkle tree; HMAC on master node; journal-based writes; LEB-level GC | UBI dependency; Linux kernel coupling; POSIX overhead |
| **littlefs** (ARM) | Pluggable block device interface; bounded RAM via configurable cache; COW metadata updates | Per-node COW chain rewriting (high write amp); no authentication |
| **Dhara** (dlbeer) | Perfect wear leveling for NAND; O(log n) map operations; atomic sector writes with sync points | C-only implementation (we port to Go) |
| **NATS JetStream filestore** (nats-io/nats-server `server/filestore.go`) | Block-group message storage; index.db structure; CRC32 per record; block-level GC on retention | `os.File` dependency; non-atomic index.db + blk sync; no authentication |
| **YAFFS2** (Aleph One) | Log-structured design for NAND; low write amplification; bare-metal Direct Interface | GPL licensing; C-only; no authentication; RAM-heavy metadata |

---

## 2. Architecture

### 2.1 Layer Diagram

```
┌──────────────────────────────────────────────────────┐
│              NATS JetStream Server                   │
│  StreamStore / ConsumerStore / KV / ObjectStore       │
├──────────────────────────────────────────────────────┤
│                                                      │
│            Kyanite FlashStore API                     │
│  AppendLog · AuthIndex · MetaStore · GC              │
│                                                      │
├───────────────────────┬──────────────────────────────┤
│   Authenticated       │   Log-Structured             │
│   B+ Tree Index       │   Block Manager              │
│   (Merkle nodes,      │   (Journal, Block Groups,    │
│    HMAC root)         │    Free list, GC)            │
├───────────────────────┴──────────────────────────────┤
│                                                      │
│         Logical Block Layer (LBL)                    │
│   Uniform read/write/erase over logical blocks       │
│                                                      │
├──────────────────────────────────────────────────────┤
│                                                      │
│      Flash Translation Layer (conditional)           │
│   NOR: identity (LBL = physical)                     │
│   NAND: Go Dhara port (wear leveling + bad block)    │
│   eMMC: future thin adapter                          │
│                                                      │
├──────────────────────────────────────────────────────┤
│                                                      │
│         Hardware Driver Interface                    │
│   SPI NOR · QSPI NOR · SPI NAND · Parallel NAND     │
│   (pure Go, user-provided, pluggable)                │
│                                                      │
└──────────────────────────────────────────────────────┘
```

### 2.2 On-Flash Layout

The flash is divided into Logical Erase Blocks (LEBs). LEB size equals the physical erase block size for NOR, or the Dhara sector size for NAND. All multi-byte integers are little-endian.

```
LEB 0            Superblock (primary copy)
LEB 1            Superblock (backup copy)
LEB 2            Master Node A
LEB 3            Master Node B
LEB 4..J-1       Journal area (configurable, minimum 4 LEBs)
LEB J..N-1       Main area (data + index nodes)
```

#### 2.2.1 Superblock (LEB 0, 1)

Written once at format time. Updated only on feature flag changes.

```
Offset  Size    Field
0       8       Magic: "KFSTORE\x00" (0x4B4653544F524500)
8       4       Format version (uint32)
12      4       LEB size in bytes (uint32)
16      4       Total LEB count (uint32)
20      4       Journal start LEB (uint32)
24      4       Journal LEB count (uint32)
28      4       Main area start LEB (uint32)
32      4       Flags (uint32):
                  bit 0: compression enabled
                  bit 1: NAND mode (FTL active)
                  bit 2: authentication enabled
                  bits 3-7: hash algorithm (0=SHA-256, 1=BLAKE2s-256)
                  bits 8-15: compression algorithm (0=none, 1=LZ4)
36      4       Min I/O size (write granularity, uint32)
40      4       Max inline data size (uint32)
44      32      Format UUID (128-bit, for identifying the volume)
76      32      HMAC of bytes [0..75] using platform key (SHA-256 HMAC)
108     ..      Padding to min I/O alignment
```

*Reference: UBIFS superblock node with HMAC (ubifs-authentication.rst §Superblock Authentication).*

#### 2.2.2 Master Node (LEB 2, 3)

Double-buffered: the node with the higher sequence number is current. Written on every commit (sync). This is the trust anchor for the entire authenticated tree.

```
Offset  Size    Field
0       8       Magic: "KFMSTR\x01\x00"
8       8       Commit sequence number (uint64)
16      4       Index root LEB (uint32)
20      4       Index root offset within LEB (uint32)
24      32      Index root hash (SHA-256 of root index node)
56      4       Free LEB bitmap LEB (uint32)
60      4       Free LEB bitmap offset (uint32)
64      32      Free LEB bitmap hash (SHA-256)
96      8       Total data bytes stored (uint64)
104     8       Total data entries (uint64)
112     8       Highest sequence number (uint64, global across all streams)
120     4       Journal head LEB (uint32)
124     4       Journal head offset (uint32)
128     4       GC LEB (uint32, current GC target)
132     32      HMAC of bytes [0..131] (SHA-256 HMAC, platform key)
164     ..      Padding to min I/O alignment
```

*Reference: UBIFS master node stores HMAC over its contents including root node location. The index root hash + HMAC chain is the Merkle trust anchor (ubifs-authentication whitepaper §Index Authentication).*

#### 2.2.3 Journal Entries

The journal is a circular log of pending mutations. Each entry is self-describing and CRC-protected. Entries accumulate until a commit promotes them into the main area.

```
Entry header (common):
Offset  Size    Field
0       2       Entry type (uint16):
                  0x01 = DataWrite (message body)
                  0x02 = IndexUpdate (B+ tree node)
                  0x03 = Delete (entry removal)
                  0x04 = MetaUpdate (consumer state, stream config)
                  0x05 = CommitBarrier
2       2       Flags (uint16)
4       4       Entry length including header (uint32)
8       4       CRC32C of [type..payload] (uint32)
12      ..      Payload (type-specific)
```

**DataWrite payload**:
```
0       8       Namespace ID (uint64, identifies stream/bucket)
8       8       Sequence number (uint64)
16      4       Subject length (uint16) + subject bytes
..      4       Payload length (uint32)
..      ..      Payload bytes (optionally LZ4 compressed)
..      8       Timestamp (unix nanos, uint64)
..      32      SHA-256 hash of uncompressed payload
```

**CommitBarrier payload**:
```
0       8       Commit sequence number (uint64)
8       32      Hash of new index root after applying all entries since last barrier
```

*Reference: UBIFS journal contains data nodes and index updates; commit writes master node. littlefs uses a similar commit-log model in its metadata pairs. JetStream filestore writes CRC32 per message record.*

#### 2.2.4 Main Area: Index Nodes

The index is a B+ tree stored in the main area. Each node is a single write unit (fits within min I/O size, padded if needed, max one page). Internal nodes contain keys and child pointers. Leaf nodes contain keys and data references.

**Internal index node**:
```
0       2       Node type: 0x10 = internal
2       2       Key count (uint16)
4       4       Node size (uint32)
8       ..      Keys: [namespace_id(8) | sequence(8)] × key_count
..      ..      Children: [child_leb(4) | child_offset(4) | child_hash(32)] × (key_count + 1)
..      4       CRC32C of node content
```

Each child pointer carries `child_hash = SHA-256(child_node_bytes)`. This makes every internal node a Merkle tree node. The root node's hash is stored in the master node and sealed by HMAC.

*Reference: UBIFS augments B+ tree index nodes with hash(child) to create a Merkle tree. Verification path: leaf → parent → ... → root → HMAC in master node. See ubifs-authentication whitepaper §Index Authentication.*

**Leaf index node**:
```
0       2       Node type: 0x20 = leaf
2       2       Entry count (uint16)
4       4       Node size (uint32)
8       ..      Entries: [
                  namespace_id(8) |
                  sequence(8) |
                  data_leb(4) | data_offset(4) | data_length(4) |
                  data_hash(32) |  -- SHA-256 of the stored data
                  subject_hash(8)  -- truncated hash for subject index lookups
                ] × entry_count
..      4       CRC32C of node content
```

Leaf nodes reference data in the main area. The `data_hash` field allows verifying individual message integrity without reading the full tree path — but the full Merkle verification (leaf → root → HMAC) proves the reference itself hasn't been tampered with.

#### 2.2.5 Main Area: Data Nodes

Raw message/object/KV data stored contiguously within LEBs. Data nodes are write-once, append-only within an LEB. An LEB containing data nodes is only erased after GC confirms all live references have been relocated.

```
0       2       Node type: 0x30 = data
2       2       Flags (compression, etc.)
4       4       Total node size (uint32)
8       8       Namespace ID (uint64)
16      8       Sequence number (uint64)
24      4       Uncompressed length (uint32)
28      4       Compressed length (uint32), equals uncompressed if no compression
32      ..      Data bytes
..      4       CRC32C of [type..data]
```

### 2.3 Authentication Model

The authentication scheme is modeled directly on UBIFS authentication (sigma-star whitepaper, Linux kernel documentation `filesystems/ubifs-authentication.rst`), adapted for the simpler FlashStore structure.

#### 2.3.1 Threat Model

- **In scope**: offline modification of flash contents (cold boot attack, flash chip desolder + reprogram, JTAG/SWD flash write). Detection of any bit-level tampering.
- **Out of scope**: side-channel attacks on the running system, key extraction from RoT, online runtime attacks with code execution.

#### 2.3.2 Hash Chain

```
Platform RoT Key (DICE CDI / OTP / TPM-sealed HMAC key)
        │
        ▼
  HMAC(master_node_bytes) ──── stored in master node
        │
        ▼
  master_node.index_root_hash = SHA-256(root_index_node)
        │
        ▼
  root_index_node.child_hashes[i] = SHA-256(child_node_i)
        │
        ▼
        ... (intermediate index nodes)
        │
        ▼
  leaf_node.entries[j].data_hash = SHA-256(data_node_j)
```

**Verification on read**: to verify a data entry at sequence S, walk the B+ tree from root to the leaf containing S. At each level, recompute `SHA-256(child_node)` and compare against the parent's stored hash. At the root, compare the root node hash against `master_node.index_root_hash`. Finally, verify `HMAC(master_node)` against the stored HMAC using the platform key.

**Cost**: O(tree_depth) hash computations per verified read. For a tree with fanout ~100 and 1M entries, depth ≈ 3. Three SHA-256 computations (~3 µs on Cortex-A7 with hardware acceleration, ~30 µs in software) plus one HMAC verification.

**Update on write**: when a new entry is inserted, the leaf node and all ancestor nodes up to the root are rewritten (wandering tree / COW). Each rewritten node recomputes its hash. The new root hash is stored in the master node, and a new HMAC is computed over the master node. This piggybacks on the COW update that's already necessary for crash safety — the authentication adds hash computation but no additional I/O.

*Reference: UBIFS wandering tree already updates leaf-to-root on every commit; authentication hooks into this path to recompute hashes (ubifs-authentication.rst §Index Authentication). The HMAC seals the root, protecting the entire tree.*

#### 2.3.3 Free Space Map Authentication

The free LEB bitmap is hashed and the hash stored in the master node (which is HMAC-sealed). An attacker cannot mark free LEBs as used (denial of service) or used LEBs as free (data destruction) without detection.

*Reference: UBIFS LPT authentication — the LPT is hashed as a whole, hash stored in the authenticated master node (ubifs-authentication.rst §LPT Authentication).*

#### 2.3.4 Journal Authentication

Each journal CommitBarrier includes the hash of the new index root that results from applying all journal entries since the last barrier. On recovery, after replaying the journal, the resulting index root hash is compared against the barrier's recorded hash. If they don't match, the journal is truncated to the last valid barrier.

*Reference: UBIFS journal authentication uses hash chains across journal entries anchored in the master node (ubifs-authentication.rst §Journal Authentication).*

#### 2.3.5 Key Provisioning

FlashStore does not manage key storage. The HMAC key is provided by the caller at mount time via the `MountOptions.AuthKey` field. On platforms with DICE/Caliptra, this would be the CDI-derived key. On simpler platforms, it could be an OTP-fused key read by the bootloader.

If no key is provided and authentication is enabled in the superblock flags, mount fails. If authentication is disabled, hash fields are still computed (for integrity/corruption detection) but HMAC fields are zeroed and not verified.

---

## 3. Go Package Design

### 3.1 Package Structure

```
kyanite.computer/flashstore/
├── flashstore.go          // top-level Store type, Mount/Format/Close
├── config.go              // FormatOptions, MountOptions
├── superblock.go          // superblock read/write/verify
├── master.go              // master node double-buffer logic
├── journal.go             // journal append, replay, commit
├── index.go               // B+ tree operations (insert, lookup, delete, split, merge)
├── merkle.go              // hash computation, verification path, HMAC
├── data.go                // data node read/write, optional compression
├── gc.go                  // garbage collection, LEB consolidation
├── freemap.go             // free LEB bitmap management
├── errors.go              // sentinel errors
├── store.go               // StreamStore/ConsumerStore/KVStore interfaces
│
├── blkdev/                // block device abstraction
│   ├── blkdev.go          // BlockDevice interface
│   ├── nor.go             // NOR-specific logical block layer (identity FTL)
│   └── nand.go            // NAND logical block layer (uses dhara)
│
├── dhara/                 // Pure Go port of Dhara NAND FTL
│   ├── nand.go            // NAND interface (user implements)
│   ├── journal.go         // Dhara journal layer
│   ├── map.go             // Dhara map layer (logical→physical sector mapping)
│   └── error.go           // ECC/bad block error types
│
├── driver/                // Hardware driver interface (user implements)
│   └── driver.go          // Flash, NORFlash, NANDFlash interfaces
│
├── compress/              // optional compression
│   └── lz4.go             // LZ4 block compression (pure Go)
│
└── hash/                  // hash utilities
    └── hash.go            // SHA-256/BLAKE2s abstraction, HMAC wrapper
```

### 3.2 Hardware Driver Interface

This is the lowest-level interface. Users implement it for their specific hardware (SPI controller, QSPI, FMC, etc.). The interface is deliberately minimal — FlashStore never calls hardware directly.

```go
// package driver

// Flash is the base interface for any flash device.
// All implementations must be safe for use by a single goroutine.
// Concurrency control, if needed, is handled by upper layers.
type Flash interface {
    // Geometry returns the device geometry.
    Geometry() Geometry
}

// Geometry describes the physical layout of a flash device.
type Geometry struct {
    // TotalSize is the total device size in bytes.
    TotalSize int64

    // EraseBlockSize is the size of one erase block in bytes.
    // For NOR: typically 4KB–64KB.
    // For NAND: typically 128KB–256KB.
    EraseBlockSize int

    // PageSize is the minimum write (program) granularity in bytes.
    // For NOR: typically 1 byte or 256 bytes (page program).
    // For NAND: typically 2KB–4KB.
    PageSize int

    // EraseValue is the byte value of erased cells (0xFF for most flash).
    EraseValue byte
}

// NORFlash extends Flash for NOR flash devices.
// NOR allows byte-level random reads and page-level writes.
type NORFlash interface {
    Flash

    // ReadAt reads len(buf) bytes starting at byte offset addr.
    // Returns the number of bytes read and any error.
    ReadAt(buf []byte, addr int64) (int, error)

    // WriteAt programs len(data) bytes starting at byte offset addr.
    // addr must be page-aligned. data length must be a multiple of PageSize.
    // The region must be in erased state (all EraseValue bytes).
    // Partial page writes within the same page before erase are
    // device-specific; the caller must not rely on them.
    WriteAt(data []byte, addr int64) (int, error)

    // EraseBlock erases the erase block containing byte offset addr.
    // addr must be erase-block-aligned.
    EraseBlock(addr int64) error
}

// NANDFlash extends Flash for NAND flash devices.
// NAND requires page-aligned I/O and has OOB/spare area.
type NANDFlash interface {
    Flash

    // NANDGeometry returns NAND-specific geometry.
    NANDGeometry() NANDGeometry

    // ReadPage reads one page of data and its OOB/spare area.
    // page is the absolute page number (0-indexed).
    // data must be exactly PageSize bytes (or nil to skip data).
    // oob must be exactly OOBSize bytes (or nil to skip OOB).
    //
    // Returns the number of bit errors corrected by ECC, or an error.
    // If ECC correction fails (uncorrectable), returns ErrECCFailed.
    ReadPage(page int64, data []byte, oob []byte) (bitflips int, err error)

    // WritePage programs one page of data and its OOB/spare area.
    // page is the absolute page number.
    // Pages within an erase block must be programmed sequentially
    // (page 0, then page 1, ...). No page may be programmed twice
    // before the containing erase block is erased.
    WritePage(page int64, data []byte, oob []byte) error

    // EraseBlock erases the erase block at the given block number.
    EraseBlock(block int64) error

    // IsBadBlock returns true if the given block is marked bad.
    IsBadBlock(block int64) (bool, error)

    // MarkBadBlock marks the given block as bad.
    MarkBadBlock(block int64) error
}

// NANDGeometry extends Geometry with NAND-specific parameters.
type NANDGeometry struct {
    // PagesPerBlock is the number of pages in one erase block.
    PagesPerBlock int

    // OOBSize is the out-of-band (spare) area size per page in bytes.
    OOBSize int

    // MaxBitflips is the ECC strength: max correctable bit errors per page.
    MaxBitflips int

    // MaxBadBlocks is the maximum number of bad blocks guaranteed by
    // the manufacturer (used for capacity planning).
    MaxBadBlocks int
}

// Error sentinels
var (
    ErrECCFailed = errors.New("driver: uncorrectable ECC error")
    ErrBadBlock  = errors.New("driver: bad block")
    ErrWriteFail = errors.New("driver: write/program failure")
    ErrEraseFail = errors.New("driver: erase failure")
)
```

### 3.3 Block Device Abstraction

This layer provides a uniform logical block interface to upper layers, hiding whether the underlying device is NOR (direct mapping) or NAND (via Dhara FTL).

```go
// package blkdev

// BlockDevice provides a uniform logical block interface.
// Upper layers see a flat array of logical blocks that can be
// read, written, and erased without worrying about wear leveling
// or bad block management.
type BlockDevice interface {
    // BlockSize returns the logical block (LEB) size in bytes.
    BlockSize() int

    // BlockCount returns the total number of logical blocks available.
    BlockCount() int

    // WriteSize returns the minimum write granularity in bytes.
    // All writes must be aligned to this and be a multiple of this.
    WriteSize() int

    // ReadAt reads len(buf) bytes from logical block blk at byte
    // offset off within the block.
    ReadAt(blk int, off int, buf []byte) error

    // WriteAt writes data to logical block blk at byte offset off.
    // off and len(data) must be aligned to WriteSize().
    // For NOR: the target region must be erased.
    // For NAND (via FTL): writes are translated by the FTL.
    WriteAt(blk int, off int, data []byte) error

    // EraseBlock erases logical block blk, resetting all bytes to
    // the erase value. For NAND, this may trigger FTL garbage
    // collection internally.
    EraseBlock(blk int) error

    // Sync ensures all pending writes are committed to persistent
    // storage. For NAND/Dhara, this is a Dhara sync point.
    Sync() error

    // Close releases any resources held by the block device.
    Close() error
}

// NewNORBlockDevice wraps a NORFlash driver as a BlockDevice.
// Logical blocks map 1:1 to physical erase blocks.
// Wear leveling for NOR is handled at the FlashStore layer
// (log-structured writes distribute erases across LEBs).
func NewNORBlockDevice(nor driver.NORFlash) BlockDevice

// NewNANDBlockDevice wraps a NANDFlash driver through the Dhara
// FTL to produce a BlockDevice with wear leveling and bad block
// management.
func NewNANDBlockDevice(nand driver.NANDFlash, opts NANDOptions) (BlockDevice, error)

type NANDOptions struct {
    // GCRatio is the fraction of blocks reserved for garbage collection.
    // Dhara default: ~12.5% (1/8). Minimum viable: 4 blocks.
    GCRatio float64
}
```

### 3.4 Dhara Go Port

A pure Go port of dlbeer/dhara, preserving the original's architecture: a journal layer providing sequential page writes with sync points, and a map layer providing logical-to-physical sector translation with O(log n) lookups.

```go
// package dhara

// NAND is the interface that users must implement to provide
// raw NAND access to Dhara. This mirrors dhara/nand.h.
type NAND interface {
    // NumBlocks returns the total number of erase blocks.
    NumBlocks() int

    // PagesPerBlock returns pages per erase block (must be power of 2).
    PagesPerBlock() int

    // PageSize returns the page data size in bytes.
    PageSize() int

    // ReadPage reads the given page. Returns ECC bit-flip count.
    // Returns ErrECC if uncorrectable.
    ReadPage(page int, buf []byte) (bitflips int, err error)

    // WritePage programs the given page.
    WritePage(page int, data []byte) error

    // EraseBlock erases the given block.
    EraseBlock(block int) error

    // IsBadBlock checks the manufacturer bad block marker.
    IsBadBlock(block int) (bool, error)

    // MarkBadBlock marks a block as bad.
    MarkBadBlock(block int) error
}

// Journal provides the low-level journaling layer.
// Corresponds to dhara/journal.h.
type Journal struct { /* ... */ }

func NewJournal(nand NAND) *Journal

func (j *Journal) Init() error
func (j *Journal) Resume() error   // scan and recover state
func (j *Journal) Enqueue(data []byte) (page int, err error)
func (j *Journal) Dequeue() error
func (j *Journal) Clear() error
func (j *Journal) Size() int       // number of enqueued pages
func (j *Journal) Capacity() int
func (j *Journal) RootPage() int   // current root metadata page

// Map provides logical sector → physical page translation.
// Corresponds to dhara/map.h.
type Map struct { /* ... */ }

func NewMap(nand NAND, gcRatio float64) *Map

func (m *Map) Init() error
func (m *Map) Resume() error      // scan and recover saved state
func (m *Map) Clear() error       // delete all data
func (m *Map) Capacity() int      // total logical sectors
func (m *Map) Size() int          // used logical sectors

func (m *Map) Find(sector int) (page int, err error)
func (m *Map) Read(sector int, buf []byte) error
func (m *Map) Write(sector int, data []byte) error
func (m *Map) CopyPage(srcPage int, dstSector int) error
func (m *Map) CopySector(srcSector int, dstSector int) error
func (m *Map) Trim(sector int) error
func (m *Map) Sync() error
func (m *Map) GC() error          // manually trigger one GC cycle
```

*Reference: Dhara original (github.com/dlbeer/dhara) — map.h and journal.h interfaces. The Go port preserves the algorithmic structure: journal uses a greedy sequential allocator with checkpoint pages; map uses a radix tree stored in journal pages for O(log n) sector lookup. Perfect wear leveling (erase counts differ by at most 1) is preserved.*

### 3.5 FlashStore API

This is the primary interface for JetStream integration.

```go
// package flashstore

// Store is the top-level FlashStore instance.
type Store struct { /* ... */ }

// Format initializes a new FlashStore on the given block device,
// erasing all existing data. Must be called once before first Mount.
func Format(dev blkdev.BlockDevice, opts FormatOptions) error

// Mount opens an existing FlashStore on the given block device.
// Replays the journal, verifies the master node HMAC (if auth is
// enabled), and prepares for read/write operations.
func Mount(dev blkdev.BlockDevice, opts MountOptions) (*Store, error)

// FormatOptions configures the on-flash layout.
type FormatOptions struct {
    // JournalLEBs is the number of LEBs reserved for the journal.
    // Minimum: 4. Default: 8. Larger values reduce commit frequency.
    JournalLEBs int

    // EnableAuth enables Merkle tree authentication.
    EnableAuth bool

    // HashAlgorithm selects the hash function.
    // 0 = SHA-256 (default), 1 = BLAKE2s-256.
    HashAlgorithm uint8

    // EnableCompression enables LZ4 data compression.
    EnableCompression bool

    // UUID is an optional volume identifier.
    // If zero, a random UUID is generated.
    UUID [16]byte
}

// MountOptions configures runtime behavior.
type MountOptions struct {
    // AuthKey is the HMAC key for authentication verification.
    // Required if the volume was formatted with EnableAuth.
    // Sourced from platform RoT (DICE CDI, OTP, etc.).
    AuthKey []byte

    // ReadOnly mounts the store in read-only mode.
    // No journal replay, no writes, no GC.
    ReadOnly bool

    // CacheSize is the number of index nodes to cache in RAM.
    // Each cached node consumes ~WriteSize bytes.
    // Default: 64. Minimum: 16.
    CacheSize int

    // SyncInterval is the maximum time between automatic commits.
    // Zero means manual sync only (caller must call Sync()).
    SyncInterval time.Duration

    // GCReserve is the number of free LEBs to maintain.
    // GC triggers when free count drops below this.
    // Default: 4.
    GCReserve int

    // VerifyOnRead controls whether every read verifies the full
    // Merkle path from leaf to root. Disabling trades security
    // for read performance (CRC is always checked regardless).
    VerifyOnRead bool
}

// --- Namespace Management ---

// CreateNamespace allocates a new namespace (stream/bucket/KV store).
// Returns a unique NamespaceID.
func (s *Store) CreateNamespace(name string) (NamespaceID, error)

// DeleteNamespace removes a namespace and all its data.
// Space is reclaimed by GC.
func (s *Store) DeleteNamespace(id NamespaceID) error

// LookupNamespace returns the NamespaceID for the given name.
func (s *Store) LookupNamespace(name string) (NamespaceID, bool, error)

// ListNamespaces returns all namespace names and IDs.
func (s *Store) ListNamespaces() ([]NamespaceEntry, error)

// NamespaceID is an opaque 64-bit identifier for a namespace.
type NamespaceID uint64

type NamespaceEntry struct {
    ID   NamespaceID
    Name string
}

// --- Append Log (JetStream Stream Messages) ---

// Append writes a new entry to the given namespace.
// Returns the assigned sequence number.
// The entry is durable after the next Sync() or automatic commit.
func (s *Store) Append(ns NamespaceID, entry *Entry) (uint64, error)

// Entry represents a single data entry (message).
type Entry struct {
    Subject   string
    Payload   []byte
    Header    []byte   // optional NATS headers
    Timestamp int64    // unix nanos; zero = auto-assign
}

// LoadEntry reads a single entry by namespace and sequence number.
// If VerifyOnRead is enabled, the full Merkle path is verified.
func (s *Store) LoadEntry(ns NamespaceID, seq uint64) (*StoredEntry, error)

// StoredEntry is an entry as read from storage.
type StoredEntry struct {
    Sequence  uint64
    Subject   string
    Payload   []byte
    Header    []byte
    Timestamp int64
    Size      int    // on-flash size including overhead
}

// LoadEntryBySubject returns the entry at the given subject-relative
// sequence position. Used for JetStream per-subject access.
func (s *Store) LoadEntryBySubject(ns NamespaceID, subject string, subjectSeq uint64) (*StoredEntry, error)

// FirstSequence returns the lowest live sequence number in the namespace.
func (s *Store) FirstSequence(ns NamespaceID) (uint64, error)

// LastSequence returns the highest sequence number in the namespace.
func (s *Store) LastSequence(ns NamespaceID) (uint64, error)

// Purge removes all entries from a namespace.
func (s *Store) Purge(ns NamespaceID) error

// PurgeUpTo removes all entries with sequence <= seq.
func (s *Store) PurgeUpTo(ns NamespaceID, seq uint64) error

// RemoveEntry removes a single entry (for interest/workqueue retention).
func (s *Store) RemoveEntry(ns NamespaceID, seq uint64) error

// --- Key-Value (JetStream KV Store) ---
// KV operations are implemented on top of append log + subject indexing.
// Each KV key maps to subject "$KV.<bucket>.<key>", latest entry wins.
// Delete markers are tombstone entries.

// --- Metadata Store (Consumer State, Stream Config) ---
// Small metadata blobs stored in dedicated index entries.

// PutMeta stores a metadata blob for the given namespace + key.
func (s *Store) PutMeta(ns NamespaceID, key string, data []byte) error

// GetMeta retrieves a metadata blob.
func (s *Store) GetMeta(ns NamespaceID, key string) ([]byte, error)

// DeleteMeta removes a metadata blob.
func (s *Store) DeleteMeta(ns NamespaceID, key string) error

// --- Lifecycle ---

// Sync commits all pending journal entries to the main area,
// updates the index, and writes a new master node.
// This is the durability boundary: data is guaranteed to survive
// power loss only after Sync returns nil.
func (s *Store) Sync() error

// Close syncs and releases all resources.
func (s *Store) Close() error

// Stats returns storage statistics.
func (s *Store) Stats() StoreStats

type StoreStats struct {
    TotalLEBs     int
    FreeLEBs      int
    UsedLEBs      int
    DirtyLEBs     int   // LEBs with some dead data, eligible for GC
    TotalEntries  int64
    TotalBytes    int64 // logical data bytes (uncompressed)
    FlashBytes    int64 // actual flash bytes consumed
    IndexNodes    int
    JournalUsage  float64 // fraction of journal space used
    CommitCount   uint64  // total commits since format
    GCCount       uint64  // total GC cycles since format
}

// --- Verification ---

// Verify performs a full offline integrity check of the store.
// Walks the entire index tree, verifies all Merkle hashes,
// verifies the master node HMAC, and optionally verifies all
// data node hashes. Returns nil if the store is consistent.
func (s *Store) Verify(opts VerifyOptions) error

type VerifyOptions struct {
    // CheckData also reads and verifies every data node hash.
    // Without this, only the index structure is verified.
    CheckData bool
}
```

---

## 4. Internal Design Details

### 4.1 B+ Tree Index

The index is a B+ tree keyed on `(NamespaceID, Sequence)` tuples with 16-byte keys. The tree order (fanout) is determined at format time based on the device's write size:

```
fanout = (write_size - node_header - crc) / (key_size + child_pointer_size + hash_size)
       = (256 - 8 - 4) / (16 + 8 + 32)
       = 244 / 56
       ≈ 4   (for 256-byte write size, NOR)

       = (2048 - 8 - 4) / (16 + 8 + 32)
       = 2036 / 56
       ≈ 36  (for 2KB write size, NOR page program)

       = (4096 - 8 - 4) / (16 + 8 + 32)
       = 4084 / 56
       ≈ 72  (for 4KB write size, NAND page)
```

At fanout 36 (2KB NOR page program), 36^3 = 46,656 entries fit in a 3-level tree. At fanout 72 (4KB NAND), 72^3 = 373,248 entries in 3 levels. This is sufficient for typical embedded JetStream workloads.

*Reference: UBIFS uses a B+ tree (technically a variant they call the "TNC" — Tree Node Cache) with wandering/COW updates. littlefs uses a CTZ skip-list for file data and metadata pair logs for directories. The B+ tree approach is taken from UBIFS as it provides better random-access performance for sequence number lookups.*

### 4.2 Wandering Tree (COW Updates)

When a leaf node is modified (insert/delete), a new copy of that leaf is written to free space in the main area. Its parent must then be updated to point to the new leaf location and recompute the hash — so the parent is also COW'd. This cascades up to the root.

The wandering tree approach means:
- Old versions of nodes remain on flash until their LEB is garbage-collected
- A crash at any point during the update leaves the previous consistent tree intact (the master node still points to the old root)
- The new tree becomes current only when the master node is atomically updated

Write amplification from wandering tree updates: one rewrite per tree level per commit. At depth 3, that's 3 index node writes per commit. Amortized across a batch of journal entries committed together, this is very efficient.

*Reference: UBIFS wandering tree (UBIFS design documentation). Also conceptually similar to Btrfs COW B-trees and ZFS indirect block trees.*

### 4.3 Journal & Commit Protocol

Writes follow this sequence:

1. **Append to journal**: data entry is written to the journal area. The journal is a simple sequential log within the journal LEBs. When one journal LEB fills, writing continues to the next. This is a single flash write (one page/write-unit).

2. **Accumulate**: multiple appends accumulate in the journal. The in-RAM index is updated optimistically (dirty cache).

3. **Commit** (triggered by `Sync()`, `SyncInterval` timer, or journal fullness):
   a. Write all dirty index nodes to the main area (COW — new copies)
   b. Write updated free LEB bitmap
   c. Compute new index root hash, free map hash
   d. Write new master node (to the other master LEB — double buffered)
   e. Increment commit sequence number

4. **Journal reclaim**: after a successful commit, the journal entries before the commit barrier are no longer needed. Their LEBs become reclaimable.

On recovery after power loss:
1. Read both master nodes, select the one with the higher sequence number
2. Verify HMAC (if auth enabled)
3. The index is consistent as of the last commit
4. Scan the journal for entries after the last commit barrier
5. Replay valid entries to reconstruct the in-RAM dirty state
6. Optionally auto-commit or leave for the caller to decide

*Reference: UBIFS commit protocol (write index, write master, journal becomes reclaimable). JetStream filestore's `recoverFullState()` performs a similar journal replay but without authentication verification.*

### 4.4 Garbage Collection

GC reclaims LEBs in the main area that contain dead data (entries that have been deleted or overwritten by wandering tree updates).

**Strategy**: greedy — pick the LEB with the most dead data (least live data to relocate). This minimizes write amplification per reclaimed LEB.

**Process**:
1. Select target LEB (highest dead-data ratio)
2. Scan the LEB for live nodes (cross-reference with current index)
3. Copy live nodes to a new LEB (or to space in a partially-filled LEB)
4. Update index pointers (this is a normal tree update, triggering COW)
5. Erase the target LEB

GC runs incrementally: one LEB per GC cycle. It's triggered when free LEB count falls below `GCReserve`. The GC cycle is interleaved with normal operations (not stop-the-world).

For NOR flash, GC provides wear leveling: by moving data between LEBs, erases are distributed across the device. Static data that never changes will eventually be relocated by GC when its LEB is selected, ensuring all LEBs participate in wear leveling over time.

*Reference: UBIFS greedy GC algorithm. JFFS2 also uses a greedy GC. littlefs does block-level GC but without the greedy selection (it uses a simpler round-robin-ish approach).*

### 4.5 Subject Index

JetStream requires efficient lookup by subject (for per-subject consumers, KV store, etc.). The primary B+ tree is keyed on `(namespace, sequence)`. Subject lookups are supported by a secondary mechanism:

Each leaf index entry stores a `subject_hash` (truncated 8-byte hash of the subject string). A subject lookup scans leaf nodes for matching `subject_hash` values, then verifies the full subject by reading the data node header.

For KV store workloads (where only the latest value per subject matters), a separate metadata entry per subject stores the current sequence number, allowing O(1) lookup after the metadata read.

This avoids a full secondary index (which would double write amplification) while still supporting the JetStream access patterns.

### 4.6 Compression

When enabled, data node payloads are compressed with LZ4 before writing. The `data_hash` in the leaf index entry covers the **uncompressed** data (allowing verification without decompression of the full Merkle path — the index hashes cover the leaf entry which includes `data_hash`, and the data node's CRC covers the compressed bytes on flash).

LZ4 is chosen for its decompression speed (~1 GB/s on Cortex-A7) and zero-allocation decompression. Compression is per-node, not per-LEB, allowing random access without decompressing unrelated data.

*Reference: UBIFS supports per-node compression with LZO/zlib/zstd. LZ4 is preferred here for bare-metal use due to its minimal code size and RAM requirements.*

---

## 5. Implementation Plan

### Phase 1: Foundation (4 weeks)

**Goal**: minimal working storage on NOR flash, no authentication, no compression.

| Week | Deliverable |
|------|-------------|
| 1 | `driver` package interfaces finalized. `blkdev` NOR implementation. In-memory `NORFlash` test driver (RAM-backed, simulates erase blocks and write granularity). |
| 2 | Superblock format/parse. Master node double-buffer write/read. Journal append/read/scan. Basic LEB allocator (free bitmap). |
| 3 | B+ tree implementation: insert, lookup, delete, split, merge. COW node writes. Tree serialization to/from flash pages. |
| 4 | `Append`, `LoadEntry`, `Sync`, `Mount` (with journal replay), `Format`, `Close`. Integration test: format, write 10K entries, unmount, remount, verify all entries readable. |

**Test methodology**: RAM-backed NOR driver. Power-loss simulation: inject crashes at every write point, verify recovery. Property-based testing with random operation sequences.

### Phase 2: Authentication (2 weeks)

**Goal**: Merkle tree hashes on index nodes, HMAC on master node.

| Week | Deliverable |
|------|-------------|
| 5 | `merkle` package: SHA-256 hash computation on index nodes, hash propagation during COW updates, verification path walk. Master node HMAC. Superblock HMAC. |
| 6 | `Verify()` full-store check. `VerifyOnRead` path. Journal authentication (commit barrier hashes). Integration test: modify random flash bytes offline, verify detection. Benchmark: overhead measurement of hash computation per read/write. |

### Phase 3: GC & Wear Leveling (2 weeks)

**Goal**: garbage collection, NOR wear distribution, storage space reclamation.

| Week | Deliverable |
|------|-------------|
| 7 | GC implementation: dead-data tracking per LEB, greedy LEB selection, live node relocation, LEB erase. Free map hash update. |
| 8 | GC integration with `Append`/`Sync` cycle. `PurgeUpTo`, `RemoveEntry`, `DeleteNamespace` with space reclamation. Long-running test: fill device to 90%, run mixed append/purge workload for 100K operations, verify no space leaks. Erase count distribution analysis. |

### Phase 4: JetStream Integration (2 weeks)

**Goal**: implement the interfaces needed for NATS JetStream server to use FlashStore as its persistence backend.

| Week | Deliverable |
|------|-------------|
| 9 | `StreamStore` adapter: wrap FlashStore API to match nats-server's internal `StreamStore` interface. `ConsumerStore` adapter: consumer state via `PutMeta`/`GetMeta`. Per-subject sequence tracking for KV store. |
| 10 | Object store support (chunked large objects across multiple entries). End-to-end test: run nats-server (TamaGo build) with FlashStore backend, publish/subscribe 10K messages, verify KV operations, verify consumer state survives restart. |

### Phase 5: Dhara Go Port & NAND Support (3 weeks)

**Goal**: pure Go port of Dhara, NAND block device adapter.

| Week | Deliverable |
|------|-------------|
| 11 | Port `dhara/journal.c` → `dhara/journal.go`. Port `dhara/map.c` → `dhara/map.go`. Preserve original test vectors. |
| 12 | `blkdev.NewNANDBlockDevice` implementation. RAM-backed NAND test driver (simulates pages, OOB, ECC errors, bad blocks). Run full FlashStore test suite on NAND block device. |
| 13 | Bad block injection testing. Power-loss testing on NAND path. Wear leveling verification (erase count uniformity). Performance benchmarks: NOR vs NAND path. |

### Phase 6: Compression & Polish (1 week)

| Week | Deliverable |
|------|-------------|
| 14 | LZ4 compression integration. `FormatOptions.EnableCompression`. Benchmark: compression ratio and throughput on typical JetStream message payloads. Documentation. API stabilization. |

### Total: ~14 weeks (3.5 months) for one developer

---

## 6. Resource Budget

### 6.1 RAM

| Component | Size (default config) | Notes |
|-----------|----------------------|-------|
| Index node cache | 64 × 2KB = 128 KB | 64 nodes at 2KB write size; configurable |
| Journal write buffer | 1 × 2KB = 2 KB | One write-unit buffer |
| Data read buffer | 1 × 2KB = 2 KB | For LoadEntry; reusable |
| Free LEB bitmap | 128 bytes | For 1024 LEBs (1 bit each) |
| Master node | ~256 bytes | In-RAM current master |
| Hash computation | ~256 bytes | SHA-256 state |
| B+ tree path cache | 4 × 2KB = 8 KB | Depth 4 worst case, for COW path |
| **Total** | **~140 KB** | Configurable 64–512 KB |

For NAND via Dhara, add Dhara's working memory:
| Dhara journal state | ~2 KB | Checkpoint + page buffer |
| Dhara map state | ~4 KB | Radix tree cache |

### 6.2 Flash Overhead

| Component | Size | Notes |
|-----------|------|-------|
| Superblock × 2 | 2 LEBs | One primary, one backup |
| Master node × 2 | 2 LEBs | Double-buffered |
| Journal | 8 LEBs (default) | Configurable |
| Free LEB bitmap | 1 LEB | For up to 65,536 LEBs |
| **Total fixed overhead** | **13 LEBs** | At 4KB LEB: 52 KB. At 64KB LEB: 832 KB |

GC reserve: additional 4 LEBs minimum must remain free for GC to operate.

### 6.3 Write Amplification Analysis

**Per-entry write cost** (amortized across a commit batch of B entries):

| Component | Writes | Notes |
|-----------|--------|-------|
| Journal entry | 1 page per entry | Sequential append |
| Data node (on commit) | 1 page per entry | COW to main area |
| Index leaf update | 1 page per commit | COW of modified leaf |
| Index internal nodes | (depth-1) pages per commit | COW cascade |
| Master node | 1 page per commit | Double-buffered write |
| **Total per entry** | **2 + (depth+1)/B pages** | For B=100, depth=3: ~2.04 pages/entry |

Compare: littlefs COW chain rewrite can be up to `depth` pages per single metadata update, not amortized. UBIFS achieves similar amortization via its journal. JetStream's current filestore on a traditional filesystem has ~1 write per entry (the OS handles the rest).

The journal batching is the key optimization: by deferring index updates until commit, the tree COW cost is paid once per batch, not once per entry.

---

## 7. Testing Strategy

### 7.1 Unit Tests

- B+ tree: insert/delete/lookup/split/merge with property-based testing (go-fuzz or similar)
- Merkle hashes: known-answer tests, tamper detection on every node type
- Journal: append/replay/wrap-around/recovery
- Superblock/master: format/parse/HMAC verify round-trip
- Dhara port: original C test vectors translated to Go table-driven tests

### 7.2 Integration Tests

- RAM-backed NOR driver with configurable erase block / page sizes
- Full lifecycle: format → mount → write N entries → sync → close → mount → verify
- Namespace CRUD: create/delete/list with concurrent writes
- KV store: put/get/delete/watch with subject-based lookup

### 7.3 Power-Loss Simulation

Inject crash (abort all pending writes) at every possible write point:
- During journal append (partial page write)
- During commit (between index write and master node write)
- During GC (between node relocation and LEB erase)
- During master node write (between LEB 2 and LEB 3)

For each injection point, verify that `Mount()` after crash:
- Succeeds (no panic, no unrecoverable corruption)
- Returns data consistent with the last successful `Sync()`
- Replays valid journal entries correctly
- Detects and reports any authentication failures

*Reference: littlefs has an excellent power-loss testing framework (littlefs-fuse + powerloss test script). UBIFS relies on UBI's atomic LEB change for its power-loss guarantees.*

### 7.4 Tamper Detection Tests

After a successful format+write+sync cycle:
- Flip random bits in each LEB type (superblock, master, journal, index, data)
- Verify that `Mount()` or subsequent reads return `ErrAuthenticationFailed`
- Verify that the tampered component is correctly identified in the error

### 7.5 Stress Tests

- Fill device to capacity, then run mixed append/purge workload
- Track erase count distribution across all LEBs (should be uniform for NOR)
- Run for simulated equivalent of 5 years of operation
- Verify no space leaks, no orphaned nodes, no hash inconsistencies

---

## 8. eMMC Future Path

eMMC presents as a block device (512-byte sectors, no erase blocks exposed to host). The internal FTL handles wear leveling and bad block management. To support eMMC:

1. Add a `driver.BlockFlash` interface (read sector, write sector, no explicit erase)
2. Add `blkdev.NewBlockBlockDevice` that maps logical blocks to sector ranges
3. The `EraseBlock` operation becomes a TRIM/DISCARD hint (or no-op)
4. FlashStore's log-structured design still benefits eMMC: sequential writes are FTL-friendly, and the GC pattern (write new, discard old) maps to TRIM semantics

The on-flash format is identical — the superblock `NAND mode` flag would be extended with a third mode (block device). No changes to the index, journal, or authentication layers.

---

## 9. Migration Path: Approach B → Approach A

When a general-purpose filesystem is needed, the following layers are added on top of the existing FlashStore core:

1. **Directory layer**: namespace hierarchy (currently flat) becomes a tree of directories. Each directory is a namespace containing directory entries.
2. **File abstraction**: a file is a sequence of data entries in a namespace, addressed by byte offset rather than sequence number. The existing append log becomes the backing store for file writes.
3. **Inode table**: mapping from inode number to file metadata (size, timestamps, type). Stored as a metadata namespace.
4. **POSIX-lite API**: open/read/write/close/stat/mkdir/readdir/unlink. No permissions, no xattrs, no hardlinks.

The authenticated B+ tree, journal, GC, and block device layers are unchanged. The authentication model extends naturally: directory entries and inode metadata are just more entries in the Merkle-authenticated index.

Estimated additional effort for Approach A: 4–6 weeks on top of the completed Approach B.

---

## 10. Open Questions

1. **Hash algorithm choice**: SHA-256 is the safe default (hardware acceleration available on many SoCs). BLAKE2s-256 is faster in pure software on small cores. Should we support both, or pick one?

2. **Subject index scaling**: the truncated-hash approach works for moderate subject counts. If a single namespace has >10K unique subjects (unusual for embedded), a secondary B+ tree for subjects may be warranted. Defer until measured.

3. **Large object chunking**: JetStream Object Store chunks large files into ~128KB pieces. Should FlashStore handle chunking internally, or leave it to the JetStream adapter? Recommendation: leave to adapter (keeps FlashStore simpler).

4. **Concurrent access**: TamaGo on multi-core (AST2700 quad-A35) may run JetStream with concurrent goroutines. The B+ tree cache needs a mutex or read-write lock. Journal appends can be serialized. Index reads can be concurrent if the cache is protected. Define the concurrency model early.

5. **NAND ECC responsibility**: Dhara expects the NAND driver to handle ECC (ReadPage returns corrected data + bitflip count). Some NAND chips have on-die ECC. For chips without it, the driver must implement ECC in software. Should FlashStore provide a software ECC library, or require the driver to handle it? Recommendation: provide a `hamming` or `bch` package as optional helper, but keep it out of the core interface.

6. **Dhara GC ratio**: Dhara reserves a fraction of blocks for GC. The default ~12.5% may be too much for small devices. Allow configuration, document trade-offs (lower reserve = higher GC frequency under load, risk of GC stalls).
