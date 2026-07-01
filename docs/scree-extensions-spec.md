# Scree: Pluggable Extensions

**Parent document**: `kyanite-flashstore-spec.md` (Scree core specification)
**Status**: Draft — reserved in on-flash format, implementation deferred

---

## 1. Extension Model

Scree supports pluggable data transformations applied at the data-node payload level. Transformations sit between the caller-supplied plaintext and the bytes written to flash. They do not touch index structure, journal framing, master nodes, superblock, or free maps — the filesystem machinery operates on post-transformation bytes and never needs access to the original content.

This placement is deliberate. It preserves:

- **Authenticate-before-decrypt ordering**: Merkle hashes and CRC cover the on-flash (post-transform) bytes. On read, the full hash chain is verified before any decryption runs. Corrupted or tampered ciphertext is rejected without ever being decrypted.
- **GC transparency**: garbage collection relocates data nodes byte-for-byte. It never interprets payload content, so it works without access to encryption keys or compressor state.
- **Per-namespace granularity**: each namespace (JetStream stream, KV bucket, object store) can have its own transformation pipeline. One namespace may use encryption + compression, another may use neither.
- **Read-only keyless mount**: the entire index tree is navigable and the Merkle chain is fully verifiable without any data-node keys. Space accounting, integrity checking, and GC can run on a volume whose payload keys are unavailable.

### 1.1 Transformation Pipeline

Transformations apply in a fixed order. On write, compression runs first (reducing plaintext size), then encryption runs on the compressed output. On read, the inverse: decrypt first, then decompress.

```
Write path:  plaintext → Compressor.Compress → Encryptor.Encrypt → flash
Read path:   flash → (verify Merkle chain) → Encryptor.Decrypt → Compressor.Decompress → plaintext
```

Either or both stages may be absent (identity pass-through). The data node header flags record which transformations were applied, so mixed-mode namespaces are supported (e.g., compression enabled midway through a stream's lifetime).

### 1.2 Data Node Header Flags

The existing 16-bit `Flags` field in the data node header (spec §2.2.5) reserves bits for extensions:

```
Bit 0:  Compressed (existing, spec §4.6)
Bit 1:  Encrypted (reserved by this document)
Bits 2-3:  Compression algorithm (00=LZ4, 01-11=reserved)
Bits 4-5:  Encryption algorithm (00=AES-256-GCM, 01=XChaCha20-Poly1305, 10-11=reserved)
Bits 6-15: Reserved (must be 0)
```

These flags are per data node, not per namespace. A reader that encounters an unknown algorithm bit pattern must return `ErrUnsupportedAlgorithm` rather than silently misinterpreting the payload.

---

## 2. Compressor Interface

```go
// Compressor transforms data-node payloads to reduce on-flash size.
// Implementations must be deterministic: the same input must always
// produce the same output, because the Merkle hash covers the
// compressed bytes on flash. Non-deterministic compressors would
// cause hash mismatches on read-back or GC relocation verification.
//
// Compressor instances must be safe for concurrent use.
type Compressor interface {
    // Compress compresses src and appends the result to dst.
    // Returns the appended dst slice.
    //
    // If compression would not reduce the size (incompressible data),
    // the implementation should return nil. Scree will then store
    // the payload uncompressed and clear the Compressed flag.
    // This avoids expansion on already-dense data (encrypted payloads,
    // binary blobs, pre-compressed content).
    Compress(dst, src []byte) []byte

    // Decompress decompresses src into dst.
    // dst must be pre-allocated to the uncompressed size (stored
    // in the data node header's UncompressedLength field).
    // Returns the number of bytes written to dst, or an error.
    Decompress(dst, src []byte) (int, error)

    // Algorithm returns the algorithm identifier stored in the
    // data node flags (bits 2-3).
    Algorithm() uint8
}
```

### 2.1 LZ4 (Default)

LZ4 block compression. Pure Go implementation (`kyanite.computer/scree/compress/lz4`). Chosen for:

- Decompression throughput: ~1 GB/s on Cortex-A7, ~3 GB/s on Cortex-A35
- Zero-allocation decompression when dst is pre-allocated
- Minimal code size (~2 KB compiled)
- Deterministic output for a given input

LZ4 has no framing overhead in block mode — the compressed output is raw LZ4 sequences. Frame headers are unnecessary because the data node already stores compressed and uncompressed lengths.

### 2.2 Future Compressors

Algorithm IDs 01–11 are reserved. Candidates for later addition:

- **Zstandard** (zstd): better ratio than LZ4, heavier decoder (~50 KB code). Worthwhile for large payloads where flash space is the constraint.
- **None with padding**: a "compressor" that pads to write-unit alignment. Useful if a platform requires exact-page writes and the caller can't guarantee alignment.

### 2.3 Interaction with Encryption

Compression **must** run before encryption. Encrypted data is indistinguishable from random noise — compressors achieve zero compression ratio on ciphertext. The pipeline order (compress-then-encrypt on write, decrypt-then-decompress on read) is enforced by Scree, not by the caller.

The `Compressed` flag in the data node header refers to the pre-encryption payload. On read, after decryption yields the compressed bytes, the `UncompressedLength` field tells the decompressor how large the output is.

---

## 3. Encryptor Interface

```go
// Encryptor provides authenticated encryption for data-node payloads.
// Operates on already-compressed data (if compression is enabled).
//
// Scree constructs a unique nonce for each data node from the
// namespace ID and sequence number, guaranteeing nonce uniqueness
// without random generation or on-flash nonce storage.
//
// The authentication tag produced by AEAD encryption is stored
// as part of the ciphertext. It is technically redundant with the
// Merkle hash chain (which also covers the ciphertext), providing
// a defense-in-depth layer: the AEAD tag detects tampering even
// if the Merkle verification is skipped (e.g., VerifyOnRead=false),
// and the Merkle chain detects tampering even if the encryption
// key is unavailable.
//
// Encryptor instances must be safe for concurrent use.
type Encryptor interface {
    // Encrypt encrypts plaintext and appends the result to dst.
    // nonce is guaranteed to be NonceSize() bytes, unique per entry.
    // Returns the appended dst slice (ciphertext + auth tag).
    //
    // Additional authenticated data (AAD) is provided by Scree and
    // consists of the data node header bytes (type, flags, namespace
    // ID, sequence number). This binds the ciphertext to its metadata:
    // an attacker cannot swap encrypted payloads between entries
    // without detection even if both entries share the same key.
    Encrypt(dst, plaintext, nonce, aad []byte) []byte

    // Decrypt decrypts ciphertext and appends the result to dst.
    // Returns the appended dst slice (plaintext), or an error if
    // authentication fails.
    Decrypt(dst, ciphertext, nonce, aad []byte) ([]byte, error)

    // NonceSize returns the required nonce size in bytes.
    // Must be >= 12 (AES-GCM) or >= 24 (XChaCha20-Poly1305).
    NonceSize() int

    // Overhead returns the number of bytes added by encryption
    // (auth tag size). Used for capacity planning.
    Overhead() int

    // Algorithm returns the algorithm identifier stored in the
    // data node flags (bits 4-5).
    Algorithm() uint8
}
```

### 3.1 Nonce Construction

The nonce is constructed by Scree, not by the Encryptor:

```
nonce = namespace_id (8 bytes, little-endian) || sequence (8 bytes, little-endian)
```

Truncated or padded to `NonceSize()`:
- **AES-256-GCM** (12-byte nonce): `SHA-256(namespace_id || sequence)[:12]`. Hashing avoids the birthday-bound risk of truncating structured data to 96 bits. With 2^32 entries per namespace the collision probability remains negligible.
- **XChaCha20-Poly1305** (24-byte nonce): `namespace_id || sequence || 0-pad` — the full 16 bytes fit within the 24-byte nonce directly with room to spare. No hashing needed.

Nonce uniqueness is guaranteed by the `(namespace_id, sequence)` pair being unique and monotonically assigned. No nonce is stored on flash — it is reconstructed from the data node header fields on read.

### 3.2 Key Derivation

Scree does not manage master key storage. The caller provides keys at the namespace or mount level. Recommended derivation scheme:

```
platform_root_key          (from DICE CDI, OTP, TPM)
    │
    ├── HKDF-SHA256(root_key, salt=volume_uuid, info="scree-hmac")
    │       → HMAC key (for Merkle authentication, already in core spec)
    │
    └── HKDF-SHA256(root_key, salt=volume_uuid, info="scree-encrypt")
            → master_encrypt_key
                │
                └── HKDF-SHA256(master_encrypt_key, salt=namespace_id, info="ns")
                        → per-namespace data key
```

Per-namespace key derivation is the caller's responsibility. Scree accepts the derived key as part of namespace configuration:

```go
type NamespaceOptions struct {
    // Encryptor for this namespace's data payloads.
    // nil = no encryption.
    Encryptor Encryptor

    // Compressor for this namespace's data payloads.
    // nil = no compression.
    Compressor Compressor
}

func (s *Store) CreateNamespace(name string, opts NamespaceOptions) (NamespaceID, error)
```

A namespace's encryption/compression settings are recorded in the namespace metadata entry. Changing them after creation applies only to newly written entries — existing entries retain their original flags and are decoded accordingly.

### 3.3 AES-256-GCM (Default)

Pure Go `crypto/aes` + `crypto/cipher` (available in TamaGo's Go runtime). Properties:

- 16-byte authentication tag (128-bit security)
- ~200 MB/s on Cortex-A35 with ARMv8 AES instructions
- ~15 MB/s on Cortex-A7 without hardware AES (software fallback)
- 12-byte nonce (constructed via hash, see §3.1)

### 3.4 XChaCha20-Poly1305 (Alternative)

Pure Go `golang.org/x/crypto/chacha20poly1305`. Properties:

- 16-byte authentication tag
- ~250 MB/s on Cortex-A35 (no special instructions needed, NEON helps)
- ~60 MB/s on Cortex-A7 (significantly faster than software AES)
- 24-byte nonce (direct construction, no hashing needed)

Recommended over AES-GCM on platforms without ARMv8 crypto extensions (e.g., Cortex-M33, older Cortex-A cores, RISC-V without Zkn).

### 3.5 Subject Name Encryption

Data node headers contain the subject string in cleartext by default. This reveals the JetStream subject hierarchy (topic names, KV keys) to anyone with flash access, even without the data key.

Optional subject encryption can be added as a separate flag (bit 6, reserved):

```
Subject encryption nonce = HKDF-SHA256(ns_key, info="subject") XOR subject_hash
```

This is a separate concern from payload encryption and can be deferred further. UBIFS/fscrypt handles this by encrypting filenames with the parent directory's key — the same pattern applies here with namespace key encrypting subject names.

---

## 4. Stored Metadata for Extensions

Each namespace's metadata entry (written via `PutMeta` at creation) records the active extensions:

```
Key: "__scree_ns_config"
Value (JSON for simplicity during bring-up, migrate to binary later):
{
    "compress": 0,          // algorithm ID, or -1 for none
    "encrypt": 0,           // algorithm ID, or -1 for none
    "encrypt_nonce_mode": 0 // 0=hash-truncated, 1=direct-padded
}
```

On `Mount`, if a namespace has encryption configured but no `Encryptor` is provided in `NamespaceOptions`, payload reads return `ErrEncryptionKeyRequired`. Index traversal, authentication verification, GC, and space accounting still work — only payload decryption is blocked.

---

## 5. Impact on Core Spec

Changes to the core Scree spec required to support extensions:

| Spec Section | Change | Reason |
|-------------|--------|--------|
| §2.2.5 Data Node Flags | Reserve bits 1–5 per §1.2 above | Algorithm identification |
| §2.2.5 Data Node | `data_hash` covers ciphertext, not plaintext | Authenticate-before-decrypt |
| §3.5 Store API `CreateNamespace` | Add `NamespaceOptions` parameter | Per-namespace encryptor/compressor |
| §3.5 Store API `LoadEntry` | Return `ErrEncryptionKeyRequired` if key missing | Graceful degradation |
| §4.6 Compression | Generalize from hardcoded LZ4 to `Compressor` interface | Pluggability |
| §6.1 RAM | Add encryptor state (~1 KB for AES-GCM context) | Resource budget |

No changes to: superblock format, master node, journal structure, index node format, Merkle authentication, GC, Dhara port, block device interface.

---

## 6. Performance Considerations

### 6.1 Overhead Budget

Per-entry overhead on the write path (worst case, both extensions active):

| Step | Cost (Cortex-A35) | Cost (Cortex-A7) |
|------|-------------------|-------------------|
| LZ4 compress (1 KB payload) | ~1 µs | ~5 µs |
| AES-256-GCM encrypt (1 KB) | ~5 µs (hw AES) | ~67 µs (sw AES) |
| SHA-256 for nonce (16 bytes) | ~0.3 µs | ~2 µs |
| **Total per entry** | **~6 µs** | **~74 µs** |

For comparison, a single NOR flash page write (256 bytes over 40 MHz QSPI) takes ~50 µs. On A7-class cores without AES hardware, XChaCha20-Poly1305 is the better choice (~17 µs for 1 KB vs 67 µs for software AES-GCM).

### 6.2 GC Impact

Zero. GC copies encrypted+compressed bytes verbatim. No decryption, no decompression, no re-encryption.

### 6.3 Flash Space Impact

| Extension | Space overhead per entry |
|-----------|------------------------|
| LZ4 compression | Negative (typical 40–60% reduction on text/JSON payloads) |
| AES-256-GCM | +16 bytes (auth tag) |
| XChaCha20-Poly1305 | +16 bytes (auth tag) |
| Both (compress then encrypt) | Net reduction on compressible data, +16 bytes on incompressible |

The AEAD tag is the only fixed overhead. No nonces stored on flash. No per-entry key material.

---

## 7. Implementation Sequence

Extensions are not part of the initial Scree implementation (Phases 1–5 in the core spec). They slot in as:

**Phase 6a** (1 week): `Compressor` interface, LZ4 implementation, integration into data node write/read path. This is already partially scoped in the core spec's Phase 6.

**Phase 7** (2 weeks): `Encryptor` interface, AES-256-GCM and XChaCha20-Poly1305 implementations, nonce construction, per-namespace key wiring, `NamespaceOptions` API change, `ErrEncryptionKeyRequired` path, integration tests (encrypt→mount without key→verify index still works→provide key→read succeeds), tamper tests (flip ciphertext bits→AEAD rejects before Merkle even runs).

**Phase 8** (optional, deferred): Subject name encryption. Depends on whether subject confidentiality is a real threat in the deployment model.
