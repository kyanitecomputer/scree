package jetstream

import (
	"errors"
	"iter"
	"maps"
	"math"
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
	// Device holds the stream. The stream mounts it as a Scree store of its
	// own, so no other stream or store may use it at the same time.
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
		ns, err := streamNamespace(store, cfg.StreamConfig)
		if err != nil {
			return nil, errors.Join(err, store.Close())
		}
		return &streamStore{storage: storage, store: store, ns: ns}, nil
	})
}

// streamNamespace returns the namespace of a stream, which it creates if
// needed.
func streamNamespace(store *scree.Store, cfg *server.StreamConfig) (scree.NamespaceID, error) {
	nsName := "stream"
	if cfg != nil && cfg.Name != "" {
		nsName = cfg.Name
	}
	ns, ok, err := store.LookupNamespace(nsName)
	if err != nil || ok {
		return ns, err
	}
	return store.CreateNamespace(nsName)
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

// StoreRawMsg stores a message at seq, which has to be the next sequence; a
// zero seq stands for it.
func (s *streamStore) StoreRawMsg(subject string, hdr, msg []byte, seq uint64, ts int64, ttl int64, discardNewCheck bool) error {
	s.mu.Lock()
	seq, err := s.nextSeq(seq)
	if err == nil {
		_, err = s.store.Append(s.ns, &scree.Entry{Subject: subject, Header: hdr, Payload: msg, Timestamp: ts})
	}
	cb := s.updates
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if cb != nil {
		cb(1, int64(len(hdr)+len(msg)), seq, subject)
	}
	return nil
}

// nextSeq checks that seq is the next sequence, or returns it for a zero
// seq. s.mu must be held.
func (s *streamStore) nextSeq(seq uint64) (uint64, error) {
	next := s.lastSeq() + 1
	if seq != next && seq != 0 {
		return 0, server.ErrSequenceMismatch
	}
	return next, nil
}

// lastSeq returns the last sequence of the stream, or 0.
func (s *streamStore) lastSeq() uint64 {
	last, _ := s.store.LastSequence(s.ns)
	return last
}

// SkipMsg uses seq, the next sequence, without storing a message; a zero
// seq stands for it.
func (s *streamStore) SkipMsg(seq uint64) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq, err := s.nextSeq(seq)
	if err != nil {
		return 0, err
	}
	if _, err := s.store.SetLastSequence(s.ns, seq); err != nil {
		return 0, err
	}
	return seq, nil
}

func (s *streamStore) SkipMsgNoInterest(seq uint64) (uint64, error) {
	return s.SkipMsg(seq)
}

// SkipMsgs uses num sequences from seq, the next sequence, without storing
// messages; a zero seq stands for it.
func (s *streamStore) SkipMsgs(seq uint64, num uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq, err := s.nextSeq(seq)
	if err != nil || num == 0 {
		return err
	}
	_, err = s.store.SetLastSequence(s.ns, seq+num-1)
	return err
}
func (s *streamStore) FlushAllPending() error { return s.store.Sync() }

func (s *streamStore) LoadMsg(seq uint64, sm *server.StoreMsg) (*server.StoreMsg, error) {
	entry, err := s.store.LoadEntry(s.ns, seq)
	if err != nil {
		return nil, server.ErrStoreMsgNotFound
	}
	return setMsg(sm, entry), nil
}

// LoadNextMsg loads the first message from start on whose subject matches
// filter, and returns its sequence too.
func (s *streamStore) LoadNextMsg(filter string, wc bool, start uint64, sm *server.StoreMsg) (*server.StoreMsg, uint64, error) {
	return s.loadNext(func(subject string) bool { return subjectMatches(subject, filter) }, start, sm)
}

func (s *streamStore) LoadNextMsgMulti(sl *gsl.SimpleSublist, start uint64, sm *server.StoreMsg) (*server.StoreMsg, uint64, error) {
	return s.loadNext(sl.HasInterest, start, sm)
}

func (s *streamStore) loadNext(match func(string) bool, start uint64, sm *server.StoreMsg) (*server.StoreMsg, uint64, error) {
	for entry := range s.entries(start, math.MaxUint64) {
		if match(entry.Subject) {
			return setMsg(sm, entry), entry.Sequence, nil
		}
	}
	return nil, s.lastSeq(), server.ErrStoreEOF
}

// LoadLastMsg loads the last message whose subject matches subject, which
// may have wildcards.
func (s *streamStore) LoadLastMsg(subject string, sm *server.StoreMsg) (*server.StoreMsg, error) {
	for entry := range s.entriesBackward(math.MaxUint64, 0) {
		if subjectMatches(entry.Subject, subject) {
			return setMsg(sm, entry), nil
		}
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

// LoadPrevMsg loads the last message up to start whose subject matches
// filter, and returns its sequence too.
func (s *streamStore) LoadPrevMsg(filter string, wc bool, start uint64, sm *server.StoreMsg) (*server.StoreMsg, uint64, error) {
	return s.loadPrev(func(subject string) bool { return subjectMatches(subject, filter) }, start, sm)
}

func (s *streamStore) LoadPrevMsgMulti(sl *gsl.SimpleSublist, start uint64, sm *server.StoreMsg) (*server.StoreMsg, uint64, error) {
	return s.loadPrev(sl.HasInterest, start, sm)
}

func (s *streamStore) loadPrev(match func(string) bool, start uint64, sm *server.StoreMsg) (*server.StoreMsg, uint64, error) {
	for entry := range s.entriesBackward(start, 0) {
		if match(entry.Subject) {
			return setMsg(sm, entry), entry.Sequence, nil
		}
	}
	first, _ := s.store.FirstSequence(s.ns)
	return nil, first, server.ErrStoreEOF
}

// setMsg sets sm, or a new message if it is nil, to entry and returns it.
func setMsg(sm *server.StoreMsg, entry *scree.StoredEntry) *server.StoreMsg {
	if sm == nil {
		sm = &server.StoreMsg{}
	}
	sm.Set(entry.Subject, entry.Header, entry.Payload, entry.Sequence, entry.Timestamp)
	return sm
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

// Purge removes all messages and returns how many it removed.
func (s *streamStore) Purge() (uint64, error) {
	s.mu.Lock()
	bytes := s.bytes(0, math.MaxUint64)
	purged, err := s.store.Purge(s.ns)
	cb := s.updates
	s.mu.Unlock()
	s.removed(cb, purged, bytes, 0, "")
	return purged, err
}

// PurgeEx removes the messages on subject below seq, if not zero, but keeps
// the last keep of them, and returns how many it removed.
func (s *streamStore) PurgeEx(subject string, seq, keep uint64) (uint64, error) {
	if seq == 1 {
		return 0, nil
	}
	if subject == "" || subject == fwcs {
		if keep == 0 && seq == 0 {
			return s.Purge()
		}
		if seq > 1 {
			return s.Compact(seq)
		}
		subject = fwcs
	}
	s.mu.Lock()
	var matching []*scree.StoredEntry
	for entry := range s.entries(0, math.MaxUint64) {
		if subjectMatches(entry.Subject, subject) {
			matching = append(matching, entry)
		}
	}
	if keep >= uint64(len(matching)) {
		matching = nil
	} else {
		matching = matching[:uint64(len(matching))-keep]
	}
	var purged, bytes uint64
	var err error
	for _, entry := range matching {
		if seq > 0 && entry.Sequence >= seq {
			break
		}
		if err = s.store.RemoveEntry(s.ns, entry.Sequence); err != nil {
			break
		}
		purged++
		bytes += msgSize(entry)
	}
	cb := s.updates
	s.mu.Unlock()
	if purged == 1 {
		s.removed(cb, purged, bytes, matching[0].Sequence, matching[0].Subject)
	} else {
		s.removed(cb, purged, bytes, 0, "")
	}
	return purged, err
}

// Compact removes the messages below seq and returns how many it removed.
// The next message gets seq at least.
func (s *streamStore) Compact(seq uint64) (uint64, error) {
	if seq == 0 {
		return s.Purge()
	}
	s.mu.Lock()
	bytes := s.bytes(0, seq-1)
	purged, err := s.store.PurgeUpTo(s.ns, seq-1)
	if err == nil && seq-1 > s.lastSeq() {
		_, err = s.store.SetLastSequence(s.ns, seq-1)
	}
	cb := s.updates
	s.mu.Unlock()
	s.removed(cb, purged, bytes, 0, "")
	return purged, err
}

// Truncate removes the messages after seq, which becomes the last sequence.
func (s *streamStore) Truncate(seq uint64) error {
	s.mu.Lock()
	bytes := s.bytes(seq+1, math.MaxUint64)
	purged, err := s.store.SetLastSequence(s.ns, seq)
	cb := s.updates
	s.mu.Unlock()
	s.removed(cb, purged, bytes, 0, "")
	return err
}

// removed reports removed messages to cb.
func (s *streamStore) removed(cb server.StorageUpdateHandler, msgs, bytes, seq uint64, subject string) {
	if cb != nil && msgs > 0 {
		cb(-int64(msgs), -int64(bytes), seq, subject)
	}
}

// bytes returns the size of the messages from start to end.
func (s *streamStore) bytes(start, end uint64) uint64 {
	var n uint64
	for entry := range s.entries(start, end) {
		n += msgSize(entry)
	}
	return n
}

// entries yields the stream's entries from start to end, in order.
func (s *streamStore) entries(start, end uint64) iter.Seq[*scree.StoredEntry] {
	return func(yield func(*scree.StoredEntry) bool) {
		first, err := s.store.FirstSequence(s.ns)
		if err != nil {
			return
		}
		start, end = max(start, first), min(end, s.lastSeq())
		for seq := start; seq <= end && seq != 0; seq++ {
			entry, err := s.store.LoadEntry(s.ns, seq)
			if err == nil && !yield(entry) {
				return
			}
		}
	}
}

// entriesBackward yields the stream's entries from start down to end.
func (s *streamStore) entriesBackward(start, end uint64) iter.Seq[*scree.StoredEntry] {
	return func(yield func(*scree.StoredEntry) bool) {
		first, err := s.store.FirstSequence(s.ns)
		if err != nil {
			return
		}
		start, end = min(start, s.lastSeq()), max(end, first)
		for seq := start; seq >= end && seq != 0; seq-- {
			entry, err := s.store.LoadEntry(s.ns, seq)
			if err == nil && !yield(entry) {
				return
			}
		}
	}
}

// fwcs is the subject filter that matches all subjects.
const fwcs = ">"

// subjectMatches reports whether subject matches filter, which may have
// wildcards; an empty filter matches all subjects.
func subjectMatches(subject, filter string) bool {
	return filter == "" || filter == fwcs || server.SubjectMatchesFilter(subject, filter)
}

// msgSize returns the bytes a message counts for.
func msgSize(entry *scree.StoredEntry) uint64 {
	return uint64(len(entry.Header) + len(entry.Payload))
}

// GetSeqFromTime returns the first sequence of a message stored at t or
// later, or the sequence after the last if there is none.
func (s *streamStore) GetSeqFromTime(t time.Time) uint64 {
	ts := t.UnixNano()
	for entry := range s.entries(0, math.MaxUint64) {
		if entry.Timestamp >= ts {
			return entry.Sequence
		}
	}
	return s.lastSeq() + 1
}

// subjectStates returns the state of each subject that match selects, over
// the messages from start on.
func (s *streamStore) subjectStates(start uint64, match func(string) bool) map[string]server.SimpleState {
	states := make(map[string]server.SimpleState)
	for entry := range s.entries(start, math.MaxUint64) {
		if !match(entry.Subject) {
			continue
		}
		state := states[entry.Subject]
		if state.Msgs == 0 {
			state.First = entry.Sequence
		}
		state.Last = entry.Sequence
		state.Msgs++
		states[entry.Subject] = state
	}
	return states
}

// filterMatcher returns a match function for a subject filter.
func filterMatcher(filter string) func(string) bool {
	return func(subject string) bool { return subjectMatches(subject, filter) }
}

func (s *streamStore) FilteredState(seq uint64, subject string) (server.SimpleState, error) {
	var total server.SimpleState
	for _, state := range s.subjectStates(seq, filterMatcher(subject)) {
		if total.Msgs == 0 || state.First < total.First {
			total.First = state.First
		}
		total.Last = max(total.Last, state.Last)
		total.Msgs += state.Msgs
	}
	return total, nil
}

func (s *streamStore) SubjectsState(filterSubject string) map[string]server.SimpleState {
	states := s.subjectStates(0, filterMatcher(filterSubject))
	if len(states) == 0 {
		return nil
	}
	return states
}

func (s *streamStore) SubjectsTotals(filterSubject string) map[string]uint64 {
	states := s.subjectStates(0, filterMatcher(filterSubject))
	if len(states) == 0 {
		return nil
	}
	totals := make(map[string]uint64, len(states))
	for subject, state := range states {
		totals[subject] = state.Msgs
	}
	return totals
}

// AllLastSeqs returns the last sequence of each subject, sorted.
func (s *streamStore) AllLastSeqs() ([]uint64, error) {
	return lastSeqs(s.subjectStates(0, filterMatcher(fwcs))), nil
}

// MultiLastSeqs returns the last sequence up to maxSeq, or the stream's last
// if it is zero, of each subject that matches one of filters, sorted.
func (s *streamStore) MultiLastSeqs(filters []string, maxSeq uint64, maxAllowed int) ([]uint64, error) {
	if maxSeq == 0 {
		maxSeq = math.MaxUint64
	}
	states := make(map[string]server.SimpleState)
	for entry := range s.entries(0, maxSeq) {
		if slices.ContainsFunc(filters, func(filter string) bool { return subjectMatches(entry.Subject, filter) }) {
			states[entry.Subject] = server.SimpleState{Last: entry.Sequence}
		}
	}
	if maxAllowed > 0 && len(states) > maxAllowed {
		return nil, server.ErrTooManyResults
	}
	return lastSeqs(states), nil
}

func lastSeqs(states map[string]server.SimpleState) []uint64 {
	if len(states) == 0 {
		return nil
	}
	seqs := make([]uint64, 0, len(states))
	for _, state := range states {
		seqs = append(seqs, state.Last)
	}
	slices.Sort(seqs)
	return seqs
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
		return "", server.ErrStoreMsgNotFound
	}
	return e.Subject, nil
}

// NumPending returns the number of messages from sseq on that match filter,
// or of their subjects if lastPerSubject, and the last sequence it is valid
// through.
func (s *streamStore) NumPending(sseq uint64, filter string, lastPerSubject bool) (uint64, uint64, error) {
	return s.numPending(sseq, filterMatcher(filter), lastPerSubject)
}

func (s *streamStore) NumPendingMulti(sseq uint64, sl *gsl.SimpleSublist, lastPerSubject bool) (uint64, uint64, error) {
	return s.numPending(sseq, sl.HasInterest, lastPerSubject)
}

func (s *streamStore) numPending(sseq uint64, match func(string) bool, lastPerSubject bool) (uint64, uint64, error) {
	states := s.subjectStates(sseq, match)
	if lastPerSubject {
		return uint64(len(states)), s.lastSeq(), nil
	}
	var total uint64
	for _, state := range states {
		total += state.Msgs
	}
	return total, s.lastSeq(), nil
}

func (s *streamStore) State() server.StreamState {
	return s.state(true)
}

func (s *streamStore) FastState(st *server.StreamState) {
	*st = s.state(false)
}

// state returns the state of the stream, with the list of interior deletes
// if withDeleted.
func (s *streamStore) state(withDeleted bool) server.StreamState {
	var st server.StreamState
	st.LastSeq = s.lastSeq()
	subjects := make(map[string]struct{})
	next := uint64(0)
	for entry := range s.entries(0, math.MaxUint64) {
		if st.Msgs == 0 {
			st.FirstSeq = entry.Sequence
			st.FirstTime = time.Unix(0, entry.Timestamp).UTC()
		} else if withDeleted {
			for seq := next; seq < entry.Sequence; seq++ {
				st.Deleted = append(st.Deleted, seq)
			}
		}
		next = entry.Sequence + 1
		st.Msgs++
		st.Bytes += msgSize(entry)
		st.LastTime = time.Unix(0, entry.Timestamp).UTC()
		subjects[entry.Subject] = struct{}{}
	}
	if st.Msgs == 0 {
		if st.LastSeq > 0 {
			st.FirstSeq = st.LastSeq + 1
		}
	} else {
		st.NumDeleted = int(st.LastSeq - st.FirstSeq + 1 - st.Msgs)
		for seq := next; withDeleted && seq <= st.LastSeq; seq++ {
			st.Deleted = append(st.Deleted, seq)
		}
	}
	st.NumSubjects = len(subjects)
	s.mu.RLock()
	st.Consumers = len(s.consumers)
	s.mu.RUnlock()
	return st
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

// Delete removes the stream's namespace and stops the store.
func (s *streamStore) Delete(inline bool) error {
	s.mu.Lock()
	err := s.store.DeleteNamespace(s.ns)
	s.mu.Unlock()
	return errors.Join(err, s.Stop())
}

func (s *streamStore) Stop() error {
	if err := s.store.Close(); err != nil && !errors.Is(err, scree.ErrClosed) {
		return err
	}
	return nil
}

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

// errNoSnapshots reports that the store cannot take snapshots.
var errNoSnapshots = errors.New("jetstream: scree stores do not support snapshots")

func (s *streamStore) Snapshot(deadline time.Duration, includeConsumers, checkMsgs bool) (*server.SnapshotResult, error) {
	return nil, errNoSnapshots
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
		c.state = copyConsumerState(state)
	}
	return nil
}
func (c *consumerStore) State() (*server.ConsumerState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := copyConsumerState(&c.state)
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

// copyConsumerState returns a copy of st that shares no maps or pending
// entries with it.
func copyConsumerState(st *server.ConsumerState) server.ConsumerState {
	cp := *st
	if st.Pending != nil {
		cp.Pending = make(map[uint64]*server.Pending, len(st.Pending))
		for seq, p := range st.Pending {
			if p != nil {
				p = &server.Pending{Sequence: p.Sequence, Timestamp: p.Timestamp}
			}
			cp.Pending[seq] = p
		}
	}
	cp.Redelivered = maps.Clone(st.Redelivered)
	return cp
}

func sortedSeqs(m map[uint64]scree.StoredEntry) []uint64 {
	seqs := make([]uint64, 0, len(m))
	for seq := range m {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	return seqs
}
