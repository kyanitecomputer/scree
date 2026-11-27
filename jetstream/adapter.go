package jetstream

import (
	"errors"
	"io"
	"iter"
	"maps"
	"slices"
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

// streamStore is a NATS stream in a Scree namespace. The Scree store locks
// itself; mu serializes the changes of the stream, so that a check and the
// change it guards are not interleaved with another change, and guards the
// fields below it. The callbacks are called without it.
type streamStore struct {
	storage   server.StorageType
	mu        sync.RWMutex
	store     *scree.Store
	ns        scree.NamespaceID
	updates   server.StorageUpdateHandler
	removes   server.StorageRemoveMsgHandler
	process   server.ProcessJetStreamMsgHandler
	consumers []server.ConsumerStore
	sources   map[string]server.StreamSourceState
}

func (s *streamStore) StoreMsg(subject string, hdr, msg []byte, ttl int64) (uint64, int64, error) {
	ts := time.Now().UnixNano()
	s.mu.Lock()
	seq, err := s.store.Append(s.ns, &scree.Entry{Subject: subject, Header: hdr, Payload: msg, Timestamp: ts})
	cb := s.updates
	s.mu.Unlock()
	if err != nil {
		return 0, 0, err
	}
	if cb != nil {
		cb(1, int64(len(hdr)+len(msg)), seq, subject)
	}
	return seq, ts, nil
}

func (s *streamStore) StoreRawMsg(subject string, hdr, msg []byte, seq uint64, ts int64, ttl int64, discardNewCheck bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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

func (s *streamStore) LoadLastSeq(subject string) (uint64, error) {
	sm, err := s.LoadLastMsg(subject, nil)
	if err != nil {
		return 0, err
	}
	return sm.Sequence(), nil
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
	s.mu.Lock()
	err := s.store.RemoveEntry(s.ns, seq)
	cb := s.removes
	s.mu.Unlock()
	if err != nil {
		return false, err
	}
	if cb != nil {
		cb(seq)
	}
	return true, nil
}

func (s *streamStore) EraseMsg(seq uint64) (bool, error) { return s.RemoveMsg(seq) }

func (s *streamStore) Purge() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Purge(s.ns)
}

func (s *streamStore) PurgeEx(subject string, seq, keep uint64) (uint64, error) { return s.Purge() }
func (s *streamStore) Compact(seq uint64) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.store.PurgeUpTo(s.ns, seq)
	return seq, err
}
func (s *streamStore) Truncate(seq uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.store.PurgeUpTo(s.ns, seq)
	return err
}
func (s *streamStore) GetSeqFromTime(t time.Time) uint64 { return 0 }

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
func (s *streamStore) MultiLastMsgs(filters []string, minSeq, maxSeq uint64, maxAllowed int, cb func(sm *server.StoreMsg, np uint64) bool) (uint64, uint64, error) {
	seqs, err := s.MultiLastSeqs(filters, maxSeq, maxAllowed)
	if err != nil || len(seqs) == 0 {
		return 0, 0, err
	}
	total := uint64(len(seqs))
	np := total
	for _, seq := range seqs {
		np--
		if seq < minSeq {
			continue
		}
		sm, err := s.LoadMsg(seq, nil)
		if err != nil {
			return total, np, err
		}
		if !cb(sm, np) {
			break
		}
	}
	return total, np, nil
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

func (s *streamStore) EncodedStreamState(failed uint64, withSources bool) ([]byte, error) {
	return nil, nil
}

// SourcesState returns the sourcing state last applied; the store does not
// track the sources of the messages it stores.
func (s *streamStore) SourcesState() map[string]server.StreamSourceState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.sources) == 0 {
		return nil
	}
	return maps.Clone(s.sources)
}

func (s *streamStore) ApplySourcesState(sources map[string]server.StreamSourceState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sources = maps.Clone(sources)
}

func (s *streamStore) Ready() {}

func (s *streamStore) SyncDeleted(dbs server.DeleteBlocks) error { return nil }
func (s *streamStore) Type() server.StorageType                  { return s.storage }
func (s *streamStore) RegisterStorageUpdates(cb server.StorageUpdateHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = cb
}
func (s *streamStore) RegisterStorageRemoveMsg(cb server.StorageRemoveMsgHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removes = cb
}
func (s *streamStore) RegisterProcessJetStreamMsg(cb server.ProcessJetStreamMsgHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.process = cb
}
func (s *streamStore) UpdateConfig(cfg *server.StreamConfig) error { return nil }
func (s *streamStore) Delete(inline bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.store.Purge(s.ns)
	return err
}
func (s *streamStore) Stop() error { return s.store.Close() }

func (s *streamStore) ConsumerStore(name string, created time.Time, cfg *server.ConsumerConfig) (server.ConsumerStore, error) {
	if name == "" || cfg == nil {
		return nil, errors.New("jetstream: bad consumer config")
	}
	cs := &consumerStore{stream: s, name: name, cfg: *cfg, storage: s.storage}
	if err := s.AddConsumer(cs); err != nil {
		return nil, err
	}
	return cs, nil
}

func (s *streamStore) AddConsumer(o server.ConsumerStore) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consumers = append(s.consumers, o)
	return nil
}

func (s *streamStore) RemoveConsumer(o server.ConsumerStore) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consumers = slices.DeleteFunc(s.consumers, func(c server.ConsumerStore) bool { return c == o })
	return nil
}

func (s *streamStore) Consumers() iter.Seq[server.ConsumerStore] {
	s.mu.RLock()
	consumers := slices.Clone(s.consumers)
	s.mu.RUnlock()
	return slices.Values(consumers)
}

func (s *streamStore) Snapshot(deadline time.Duration, includeConsumers, checkMsgs bool) (*server.SnapshotResult, error) {
	return &server.SnapshotResult{Reader: io.NopCloser(&emptyReader{}), State: s.State()}, nil
}
func (s *streamStore) Utilization() (uint64, uint64, error) { return 0, 0, nil }
func (s *streamStore) ResetState()                          {}

// consumerStore keeps a consumer's state in memory. mu guards cfg and state.
type consumerStore struct {
	stream  *streamStore
	name    string
	storage server.StorageType
	mu      sync.Mutex
	cfg     server.ConsumerConfig
	state   server.ConsumerState
}

func (c *consumerStore) SetStarting(sseq uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Delivered.Stream = sseq
	return nil
}
func (c *consumerStore) UpdateStarting(sseq uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Delivered.Stream = sseq
}
func (c *consumerStore) Reset(sseq uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = server.ConsumerState{}
	c.state.Delivered.Stream = sseq
	return nil
}
func (c *consumerStore) HasState() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state.Delivered.Stream != 0 || c.state.AckFloor.Stream != 0
}
func (c *consumerStore) UpdateDelivered(dseq, sseq, dc uint64, ts int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Delivered.Consumer = dseq
	c.state.Delivered.Stream = sseq
	return nil
}
func (c *consumerStore) UpdateAcks(dseq, sseq uint64, explicit bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.AckFloor.Consumer = dseq
	c.state.AckFloor.Stream = sseq
	return nil
}
func (c *consumerStore) RemoveRedeliveredBelow(seq uint64) {}
func (c *consumerStore) GetConfig() *server.ConsumerConfig {
	c.mu.Lock()
	defer c.mu.Unlock()
	cfg := c.cfg
	cfg.Name = c.name
	return &cfg
}
func (c *consumerStore) UpdateConfig(cfg *server.ConsumerConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cfg != nil {
		c.cfg = *cfg
	}
	return nil
}
func (c *consumerStore) Update(state *server.ConsumerState) error { return c.ForceUpdate(state) }
func (c *consumerStore) ForceUpdate(state *server.ConsumerState) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if state != nil {
		c.state = *state
	}
	return nil
}
func (c *consumerStore) State() (*server.ConsumerState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.state
	return &st, nil
}
func (c *consumerStore) BorrowState() (*server.ConsumerState, error) { return c.State() }
func (c *consumerStore) EncodedState() ([]byte, error)               { return nil, nil }
func (c *consumerStore) Type() server.StorageType                    { return c.storage }
func (c *consumerStore) Stop() error                                 { return c.stream.RemoveConsumer(c) }
func (c *consumerStore) Delete() error {
	c.mu.Lock()
	c.state = server.ConsumerState{}
	c.mu.Unlock()
	return c.Stop()
}
func (c *consumerStore) StreamDelete() error { return c.Delete() }

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
