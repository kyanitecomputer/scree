package scree

import "testing"

func TestNamespaceCRUD(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{})
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	id, err := store.CreateNamespace("stream")
	if err != nil {
		t.Fatalf("CreateNamespace() error = %v", err)
	}
	if err := store.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	reopened, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() reopened error = %v", err)
	}
	got, ok, err := reopened.LookupNamespace("stream")
	if err != nil || !ok || got != id {
		t.Fatalf("LookupNamespace() = %d, %v, %v, want %d, true, nil", got, ok, err, id)
	}
	entries, err := reopened.ListNamespaces()
	if err != nil || len(entries) != 1 || entries[0].Name != "stream" {
		t.Fatalf("ListNamespaces() = %+v, %v, want stream", entries, err)
	}
	if err := reopened.DeleteNamespace(id); err != nil {
		t.Fatalf("DeleteNamespace() error = %v", err)
	}
}

func TestMetaRoundTrip(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{})
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	id, err := store.CreateNamespace("stream")
	if err != nil {
		t.Fatalf("CreateNamespace() error = %v", err)
	}
	if err := store.PutMeta(id, "consumer", []byte("state")); err != nil {
		t.Fatalf("PutMeta() error = %v", err)
	}
	if err := store.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	reopened, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() reopened error = %v", err)
	}
	data, err := reopened.GetMeta(id, "consumer")
	if err != nil || string(data) != "state" {
		t.Fatalf("GetMeta() = %q, %v, want state, nil", data, err)
	}
	if err := reopened.DeleteMeta(id, "consumer"); err != nil {
		t.Fatalf("DeleteMeta() error = %v", err)
	}
	if _, err := reopened.GetMeta(id, "consumer"); err != ErrNotFound {
		t.Fatalf("GetMeta() after delete error = %v, want %v", err, ErrNotFound)
	}
}
