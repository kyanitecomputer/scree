module src.kyanite.computer/scree/jetstream

go 1.26.0

require (
	github.com/nats-io/nats-server/v2 v2.12.1
	github.com/nats-io/nats.go v1.55.0
	src.kyanite.computer/scree v0.0.0-00010101000000-000000000000
)

require (
	github.com/antithesishq/antithesis-sdk-go v0.9.0-default-no-op // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/klauspost/compress v1.20.4 // indirect
	github.com/minio/highwayhash v1.0.4 // indirect
	github.com/nats-io/jwt/v2 v2.8.3 // indirect
	github.com/nats-io/nkeys v0.4.17 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
	golang.org/x/time v0.16.0 // indirect
)

// The fork adds server.RegisterStreamStoreProvider, which upstream does not
// take. Use a go.work to build against a local checkout instead.
replace github.com/nats-io/nats-server/v2 => github.com/kyanitecomputer/nats-server/v2 v2.0.0-20261127151409-2a2d571d7581

replace src.kyanite.computer/scree => ../
