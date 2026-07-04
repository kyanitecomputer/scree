# Scree

Scree is an experimental Go flash filesystem/store aimed at backing NATS JetStream on bare-metal or embedded flash devices.

Part of the [Kyanite](https://github.com/kyanitecomputer) stack.

> **Status:** experimental — expect breaking changes.

## Current capabilities

- Formats and mounts a Scree volume on a logical block device.
- Stores redundant superblocks and master nodes, with fallback when the primary copy is unreadable.
- Uses an append-only journal with CRC-protected records.
- Replays journal state after remount, including after an interrupted append where previous entries remain readable.
- Supports NOR flash through `blkdev.NewNOR` and the `driver.NORFlash` interface.
- Provides an in-memory NOR flash implementation for tests under `internal/testflash`.
- Manages named namespaces:
  - create, lookup, list, and delete namespaces
  - persist namespace changes across remounts
- Stores entries in namespaces:
  - append subject/header/payload/timestamp records
  - auto-assign monotonically increasing sequence numbers
  - load by sequence
  - load by subject occurrence
  - report first and last sequence numbers
- Supports retention operations:
  - remove one entry
  - purge a namespace
  - purge entries up to a sequence
  - persist removals and purges across remounts
- Stores namespace metadata with put/get/delete operations.
- Supports read-only mounts that reject write operations.
- Provides basic verification of persistent metadata and journal entry decoding.
- Includes a preliminary NATS JetStream stream-store adapter that can store, load, remove, purge, and report basic stream state.

## Not yet complete

- Free-space accounting is static after format; journal compaction and block reclamation are not implemented.
- Entries currently live in the journal and are rebuilt into memory on mount.
- Journal capacity is finite; writes return `ErrNoSpace` when reserved journal units are exhausted.
- Authentication is only represented as a volume flag and mount policy check; data authentication is not implemented yet.
- NAND support is defined at the driver interface level but does not have a block-device adapter yet.
- The JetStream adapter is partial. Several advanced APIs are stubs or simplified, including snapshots, subject totals, wildcard/multi-subject filtering, time lookup, consumer persistence encoding, and utilization reporting.

## Packages

- `scree`: core format, mount, namespace, entry, metadata, retention, verification, and recovery logic.
- `blkdev`: logical block-device abstraction and NOR adapter.
- `driver`: flash driver interfaces and geometry validation.
- `jetstream`: NATS JetStream storage-provider adapter.
- `internal/testflash`: in-memory flash devices used by tests.

## Development

Run the test suite with:

```sh
go test ./...
```

The module currently replaces `github.com/nats-io/nats-server/v2` with `../nats-server`, so JetStream adapter builds require that checkout to exist next to this repository.

## Contributing

See the org-wide [CONTRIBUTING guide](https://github.com/kyanitecomputer/.github/blob/main/CONTRIBUTING.md).
Contributions are dual-licensed.

## Security

See the org-wide [SECURITY policy](https://github.com/kyanitecomputer/.github/blob/main/SECURITY.md).

## License

Dual-licensed under either of Apache-2.0 ([LICENSE-APACHE](LICENSE-APACHE)) or
MIT ([LICENSE-MIT](LICENSE-MIT)) at your option.
