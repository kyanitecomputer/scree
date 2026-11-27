module src.kyanite.computer/scree/jetstream

go 1.25.0

require (
	github.com/nats-io/nats-server/v2 v2.12.1
	src.kyanite.computer/scree v0.0.0-00010101000000-000000000000
)

require (
	github.com/antithesishq/antithesis-sdk-go v0.7.0-default-no-op // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/minio/highwayhash v1.0.4 // indirect
	github.com/nats-io/jwt/v2 v2.8.2 // indirect
	github.com/nats-io/nkeys v0.4.16 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/time v0.15.0 // indirect
)

// The fork adds server.RegisterStreamStoreProvider, which upstream does not
// take. Use a go.work to build against a local checkout instead.
replace github.com/nats-io/nats-server/v2 => github.com/kyanitecomputer/nats-server/v2 v2.0.0-20260922125455-5574e9c2c87d

replace src.kyanite.computer/scree => ../
