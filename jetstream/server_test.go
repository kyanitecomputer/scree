package jetstream

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

func TestRegisterServesStream(t *testing.T) {
	const storage = server.StorageType(1000)
	dev := newTestDevice(t)
	err := Register(storage, "scree", func(cfg server.StreamStoreConfig) (Options, error) {
		return Options{Device: dev, Format: true}, nil
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	srv, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoSigs: true})
	if err != nil {
		t.Fatalf("server.NewServer() error = %v", err)
	}
	srv.Start()
	defer srv.Shutdown()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("server not ready")
	}
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatalf("nats.Connect() error = %v", err)
	}
	defer nc.Close()

	msg, err := nc.Request("$JS.API.STREAM.CREATE.TEST", []byte(`{"name":"TEST","subjects":["foo.>"],"storage":"scree"}`), 5*time.Second)
	if err != nil {
		t.Fatalf("stream create error = %v", err)
	}
	var created struct {
		Error *struct{ Description string } `json:"error"`
	}
	if err := json.Unmarshal(msg.Data, &created); err != nil || created.Error != nil {
		t.Fatalf("stream create = %s, %v", msg.Data, err)
	}

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream() error = %v", err)
	}
	for _, subject := range []string{"foo.a", "foo.b", "foo.a"} {
		if _, err := js.Publish(subject, []byte(subject)); err != nil {
			t.Fatalf("Publish(%s) error = %v", subject, err)
		}
	}
	if state := streamState(t, nc); state.Msgs != 3 || state.LastSeq != 3 {
		t.Fatalf("stream state = %+v, want 3 messages", state)
	}
	last, err := js.GetLastMsg("TEST", "foo.b")
	if err != nil || last.Sequence != 2 {
		t.Fatalf("GetLastMsg(foo.b) = %+v, %v, want sequence 2", last, err)
	}
	if err := js.PurgeStream("TEST", &nats.StreamPurgeRequest{Subject: "foo.a"}); err != nil {
		t.Fatalf("PurgeStream(foo.a) error = %v", err)
	}
	if state := streamState(t, nc); state.Msgs != 1 || state.FirstSeq != 2 {
		t.Fatalf("stream state after purge = %+v, want message 2", state)
	}
}

// streamState returns the state of stream TEST. The client cannot decode the
// stream info, since it does not know the storage type.
func streamState(t *testing.T, nc *nats.Conn) server.StreamState {
	t.Helper()
	msg, err := nc.Request("$JS.API.STREAM.INFO.TEST", nil, 5*time.Second)
	if err != nil {
		t.Fatalf("stream info error = %v", err)
	}
	var info server.JSApiStreamInfoResponse
	if err := json.Unmarshal(msg.Data, &info); err != nil || info.Error != nil {
		t.Fatalf("stream info = %s, %v", msg.Data, err)
	}
	return info.State
}
