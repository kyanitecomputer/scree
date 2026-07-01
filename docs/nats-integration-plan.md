# NATS JetStream Integration Plan

Scree targets the latest local `nats-server` API in `../nats-server`.

## Current NATS storage shape

The current server defines storage through internal interfaces in package `server`:

- `StreamStore` in `../nats-server/server/store.go`
- `ConsumerStore` in `../nats-server/server/store.go`
- store selection in `../nats-server/server/stream.go`

The existing selection path only constructs memory or file stores. `StoreMsg` also has unexported fields, so an out-of-package backend cannot fully implement the interface without an upstreamable integration seam.

## Upstreamable seam

The preferred NATS change is a small storage-provider registry in package `server`:

1. Add a storage type or backend identifier for external stores.
2. Add a provider interface that constructs a `StreamStore` from stream config, server context, and creation time.
3. Route `stream.setupStore` through the provider when a registered backend is selected.
4. Keep existing memory and file behavior unchanged.

This keeps Scree-specific code out of core NATS while allowing a downstream build to register a Scree provider.

## StoreMsg access

Because `StoreMsg` fields are unexported, one of these upstreamable options is required:

1. Add exported constructor/accessor methods for `StoreMsg`.
2. Add a small exported message value type used at the storage boundary.
3. Move the Scree adapter into package `server` as a build-tagged file.

Option 1 is the smallest likely upstream patch. Option 3 is acceptable for an MVP fork but less desirable upstream.

## Scree-side adapter boundary

Scree core must not import `nats-server`. The NATS adapter should live in a separate package or NATS-side provider and translate between:

- Scree namespaces and NATS streams.
- Scree entries and NATS stored messages.
- Scree metadata records and NATS consumer state.

Core Scree remains usable without NATS.

## Authentication policy

Scree should model authentication failures as structured errors and support a mount/read policy:

- report-only: return data with recorded verification status where the API supports it, and expose failures through `Verify`/stats callbacks.
- strict: fail mount or read when authenticated content cannot be verified.

Strict mode should be the default for authenticated volumes. Report-only mode is for diagnostics and recovery tooling.
