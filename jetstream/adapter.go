package jetstream

import (
	"errors"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats-server/v2/server/gsl"

	"src.kyanite.computer/scree"
	"src.kyanite.computer/scree/blkdev"
)

// Provider returns storage options for a NATS stream.
type Provider func(server.StreamStoreConfig) (Options, error)

// Options configures a Scree-backed NATS stream store.
type Options struct {
	Device        blkdev.BlockDevice
	Format        bool
	FormatOptions scree.FormatOptions
	MountOptions  scree.MountOptions
}

// Register registers a Scree-backed StreamStore provider with NATS.
func Register(storage server.StorageType, name string, provider Provider) error {
	return server.RegisterStreamStoreProvider(storage, name, func(cfg server.StreamStoreConfig) (server.StreamStore, error) {
		opts, err := provider(cfg)
		if err != nil {
			return nil, err
		}
		if opts.Device == nil {
			return nil, errors.New("jetstream: nil scree block device")
		}
		if opts.Format {
			if err := scree.Format(opts.Device, opts.FormatOptions); err != nil {
				return nil, err
			}
		}
		store, err := scree.Mount(opts.Device, opts.MountOptions)
		if err != nil {
			return nil, err
		}
		name := "stream"
		if cfg.StreamConfig != nil && cfg.StreamConfig.Name != "" {
			name = cfg.StreamConfig.Name
		}
		ns, ok, err := store.LookupNamespace(name)
		if err != nil {
			return nil, err
		}
		if !ok {
			ns, err = store.CreateNamespace(name)
			if err != nil {
				return nil, err
			}
		}
		return &streamStore{storage: storage, store: store, ns: ns}, nil
	})
}

type streamStore struct {
	mu        sync.RWMutex
	storage   server.StorageType
	store     *scree.Store
	ns        scree.NamespaceID
	updates   server.StorageUpdateHandler
	removes   server.StorageRemoveMsgHandler
	process   server.ProcessJetStreamMsgHandler
	consumers map[string]server.ConsumerStore
}

func (s *streamStore) StoreMsg(subject string, hdr, msg []byte, ttl int64) (uint64, int64, error) {
	ts := time.Now().UnixNano()
	seq, err := s.store.Append(s.ns, &scree.Entry{Subject: subject, Header: hdr, Payload: msg, Timestamp: ts})
	if err != nil {
		return 0, 0, err
	}
	if s.updates != nil {
		s.updates(1, int64(len(hdr)+len(msg)), seq, subject)
	}
	return seq, ts, nil
}

func (s *streamStore) StoreRawMsg(subject string, hdr, msg []byte, seq uint64, ts int64, ttl int64, discardNewCheck bool) error {
	got, err := s.store.Append(s.ns, &scree.Entry{Subject: subject, Header: hdr, Payload: msg, Timestamp: ts})
	if err != nil {
		return err
	}
	if got != seq {
		return server.ErrSequenceMismatch
	}
	return nil
}

func (s *streamStore) SkipMsg(seq uint64) (uint64, error) { return seq, nil }
func (s *streamStore) SkipMsgNoInterest(seq uint64) (uint64, error) {
	return s.SkipMsg(seq)
}
func (s *streamStore) SkipMsgs(seq uint64, num uint64) error { return nil }
func (s *streamStore) FlushAllPending() error                { return s.store.Sync() }

func (s *streamStore) LoadMsg(seq uint64, sm *server.StoreMsg) (*server.StoreMsg, error) {
	entry, err := s.store.LoadEntry(s.ns, seq)
	if err != nil {
		return nil, server.ErrStoreMsgNotFound
	}
	if sm == nil {
		sm = &server.StoreMsg{}
	}
	sm.Set(entry.Subject, entry.Header, entry.Payload, entry.Sequence, entry.Timestamp)
	return sm, nil
}

func (s *streamStore) LoadNextMsg(filter string, wc bool, start uint64, sm *server.StoreMsg) (*server.StoreMsg, uint64, error) {
	last, _ := s.store.LastSequence(s.ns)
	for seq := start; seq <= last; seq++ {
		entry, err := s.store.LoadEntry(s.ns, seq)
		if err != nil || (filter != "" && entry.Subject != filter) {
			continue
		}
		if sm == nil {
			sm = &server.StoreMsg{}
		}
		sm.Set(entry.Subject, entry.Header, entry.Payload, entry.Sequence, entry.Timestamp)
		return sm, 0, nil
	}
	return nil, 0, server.ErrStoreEOF
}

func (s *streamStore) LoadNextMsgMulti(sl *gsl.SimpleSublist, start uint64, sm *server.StoreMsg) (*server.StoreMsg, uint64, error) {
	return s.LoadNextMsg("", false, start, sm)
}

func (s *streamStore) LoadLastMsg(subject string, sm *server.StoreMsg) (*server.StoreMsg, error) {
	last, _ := s.store.LastSequence(s.ns)
	for seq := last; seq > 0; seq-- {
		entry, err := s.store.LoadEntry(s.ns, seq)
		if err != nil || (subject != "" && entry.Subject != subject) {
			continue
		}
		if sm == nil {
			sm = &server.StoreMsg{}
		}
		sm.Set(entry.Subject, entry.Header, entry.Payload, entry.Sequence, entry.Timestamp)
		return sm, nil
	}
	return nil, server.ErrStoreMsgNotFound
}

func (s *streamStore) LoadPrevMsg(filter string, wc bool, start uint64, sm *server.StoreMsg) (*server.StoreMsg, uint64, error) {
	for seq := start; seq > 0; seq-- {
		entry, err := s.store.LoadEntry(s.ns, seq)
		if err != nil || (filter != "" && entry.Subject != filter) {
			continue
		}
		if sm == nil {
			sm = &server.StoreMsg{}
		}
		sm.Set(entry.Subject, entry.Header, entry.Payload, entry.Sequence, entry.Timestamp)
		return sm, 0, nil
	}
	return nil, 0, server.ErrStoreEOF
}

func (s *streamStore) LoadPrevMsgMulti(sl *gsl.SimpleSublist, start uint64, sm *server.StoreMsg) (*server.StoreMsg, uint64, error) {
	return s.LoadPrevMsg("", false, start, sm)
}

func (s *streamStore) RemoveMsg(seq uint64) (bool, error) {
	if err := s.store.RemoveEntry(s.ns, seq); err != nil {
		return false, err
	}
	if s.removes != nil {
		s.removes(seq)
	}
	return true, nil
}

func (s *streamStore) EraseMsg(seq uint64) (bool, error) { return s.RemoveMsg(seq) }

func (s *streamStore) Purge() (uint64, error) {
	last, _ := s.store.LastSequence(s.ns)
	return last, s.store.Purge(s.ns)
}

func (s *streamStore) PurgeEx(subject string, seq, keep uint64) (uint64, error) { return s.Purge() }
func (s *streamStore) Compact(seq uint64) (uint64, error)                       { return seq, s.store.PurgeUpTo(s.ns, seq) }
func (s *streamStore) Truncate(seq uint64) error                                { return s.store.PurgeUpTo(s.ns, seq) }
func (s *streamStore) GetSeqFromTime(t time.Time) uint64                        { return 0 }

func (s *streamStore) FilteredState(seq uint64, subject string) (server.SimpleState, error) {
	state := server.SimpleState{}
	last, _ := s.store.LastSequence(s.ns)
	for i := seq; i <= last; i++ {
		entry, err := s.store.LoadEntry(s.ns, i)
		if err != nil || (subject != "" && entry.Subject != subject) {
			continue
		}
		if state.First == 0 {
			state.First = i
		}
		state.Last = i
		state.Msgs++
	}
	return state, nil
}

func (s *streamStore) SubjectsState(filterSubject string) map[string]server.SimpleState { return nil }
func (s *streamStore) SubjectsTotals(filterSubject string) map[string]uint64            { return nil }
func (s *streamStore) AllLastSeqs() ([]uint64, error) {
	last, _ := s.store.LastSequence(s.ns)
	return []uint64{last}, nil
}
func (s *streamStore) MultiLastSeqs(filters []string, maxSeq uint64, maxAllowed int) ([]uint64, error) {
	return nil, nil
}
func (s *streamStore) SubjectForSeq(seq uint64) (string, error) {
	e, err := s.store.LoadEntry(s.ns, seq)
	if err != nil {
		return "", err
	}
	return e.Subject, nil
}
func (s *streamStore) NumPending(sseq uint64, filter string, lastPerSubject bool) (uint64, uint64, error) {
	st, err := s.FilteredState(sseq, filter)
	return st.Msgs, st.Last, err
}
func (s *streamStore) NumPendingMulti(sseq uint64, sl *gsl.SimpleSublist, lastPerSubject bool) (uint64, uint64, error) {
	return s.NumPending(sseq, "", lastPerSubject)
}

func (s *streamStore) State() server.StreamState {
	var st server.StreamState
	s.FastState(&st)
	return st
}

func (s *streamStore) FastState(st *server.StreamState) {
	first, _ := s.store.FirstSequence(s.ns)
	last, _ := s.store.LastSequence(s.ns)
	st.FirstSeq = first
	st.LastSeq = last
	for seq := first; first != 0 && seq <= last; seq++ {
		entry, err := s.store.LoadEntry(s.ns, seq)
		if err != nil {
			continue
		}
		st.Msgs++
		st.Bytes += uint64(len(entry.Header) + len(entry.Payload))
		if st.FirstTime.IsZero() {
			st.FirstTime = time.Unix(0, entry.Timestamp)
		}
		st.LastTime = time.Unix(0, entry.Timestamp)
	}
}

func (s *streamStore) EncodedStreamState(failed uint64) ([]byte, error)           { return nil, nil }
func (s *streamStore) SyncDeleted(dbs server.DeleteBlocks) error                  { return nil }
func (s *streamStore) Type() server.StorageType                                   { return s.storage }
func (s *streamStore) RegisterStorageUpdates(cb server.StorageUpdateHandler)      { s.updates = cb }
func (s *streamStore) RegisterStorageRemoveMsg(cb server.StorageRemoveMsgHandler) { s.removes = cb }
func (s *streamStore) RegisterProcessJetStreamMsg(cb server.ProcessJetStreamMsgHandler) {
	s.process = cb
}
func (s *streamStore) UpdateConfig(cfg *server.StreamConfig) error { return nil }
func (s *streamStore) Delete(inline bool) error                    { return s.store.Purge(s.ns) }
func (s *streamStore) Stop() error                                 { return s.store.Close() }

func (s *streamStore) ConsumerStore(name string, created time.Time, cfg *server.ConsumerConfig) (server.ConsumerStore, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.consumers == nil {
		s.consumers = make(map[string]server.ConsumerStore)
	}
	cs := &consumerStore{storage: s.storage}
	s.consumers[name] = cs
	return cs, nil
}

func (s *streamStore) AddConsumer(o server.ConsumerStore) error    { return nil }
func (s *streamStore) RemoveConsumer(o server.ConsumerStore) error { return nil }
func (s *streamStore) Snapshot(deadline time.Duration, includeConsumers, checkMsgs bool) (*server.SnapshotResult, error) {
	return &server.SnapshotResult{Reader: io.NopCloser(&emptyReader{}), State: s.State()}, nil
}
func (s *streamStore) Utilization() (uint64, uint64, error) { return 0, 0, nil }
func (s *streamStore) ResetState()                          {}

type consumerStore struct {
	storage server.StorageType
	state   server.ConsumerState
}

func (c *consumerStore) SetStarting(sseq uint64) error { c.state.Delivered.Stream = sseq; return nil }
func (c *consumerStore) UpdateStarting(sseq uint64)    { c.state.Delivered.Stream = sseq }
func (c *consumerStore) Reset(sseq uint64) error {
	c.state = server.ConsumerState{}
	c.state.Delivered.Stream = sseq
	return nil
}
func (c *consumerStore) HasState() bool {
	return c.state.Delivered.Stream != 0 || c.state.AckFloor.Stream != 0
}
func (c *consumerStore) UpdateDelivered(dseq, sseq, dc uint64, ts int64) error {
	c.state.Delivered.Consumer = dseq
	c.state.Delivered.Stream = sseq
	return nil
}
func (c *consumerStore) UpdateAcks(dseq, sseq uint64) error {
	c.state.AckFloor.Consumer = dseq
	c.state.AckFloor.Stream = sseq
	return nil
}
func (c *consumerStore) RemoveRedeliveredBelow(seq uint64)             {}
func (c *consumerStore) UpdateConfig(cfg *server.ConsumerConfig) error { return nil }
func (c *consumerStore) Update(state *server.ConsumerState) error      { return c.ForceUpdate(state) }
func (c *consumerStore) ForceUpdate(state *server.ConsumerState) error {
	if state != nil {
		c.state = *state
	}
	return nil
}
func (c *consumerStore) State() (*server.ConsumerState, error)       { st := c.state; return &st, nil }
func (c *consumerStore) BorrowState() (*server.ConsumerState, error) { return c.State() }
func (c *consumerStore) EncodedState() ([]byte, error)               { return nil, nil }
func (c *consumerStore) Type() server.StorageType                    { return c.storage }
func (c *consumerStore) Stop() error                                 { return nil }
func (c *consumerStore) Delete() error                               { c.state = server.ConsumerState{}; return nil }
func (c *consumerStore) StreamDelete() error                         { return c.Delete() }

type emptyReader struct{}

func (e *emptyReader) Read(p []byte) (int, error) { return 0, io.EOF }

func sortedSeqs(m map[uint64]scree.StoredEntry) []uint64 {
	seqs := make([]uint64, 0, len(m))
	for seq := range m {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	return seqs
}
