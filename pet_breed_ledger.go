package task

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	ledgerSnapshotVersion       = 1
	pendingRetention            = 30 * 24 * time.Hour
	maxPendingBatches           = 32
	defaultRandomSeed     int64 = 1
)

// Pet is one pet owned by a player.
type Pet struct {
	ID         string
	Species    string
	OwnerID    string
	ParentIDs  []string
	Traits     map[string]int
	Generation int
}

// BreedResult is one breeding batch, bound to the pair that produced it.
type BreedResult struct {
	BatchID   string
	OwnerID   string
	ParentA   string
	ParentB   string
	Offspring []Pet
	At        time.Time
	HatchedAt time.Time
	Claimed   bool
}

type batchRecord struct {
	result    BreedResult
	released  bool
	releaseAt time.Time
	claimedAt time.Time
}

type dailyCounter struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

// splitMix64 is a small deterministic random source. Its state is part of the
// snapshot so that a restored ledger continues the same sequence.
type splitMix64 struct {
	state uint64
}

func (r *splitMix64) seed(seed int64) {
	r.state = uint64(seed) ^ 0xa0761d6478bd642f
}

func (r *splitMix64) next() uint64 {
	r.state += 0x9e3779b97f4a7c15
	z := r.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (r *splitMix64) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.next() % uint64(n))
}

func (r *splitMix64) float64() float64 {
	return float64(r.next()>>11) / float64(uint64(1)<<53)
}

// PetBreedLedger records breeding batches until their results are claimed.
type PetBreedLedger struct {
	mu sync.Mutex

	pets         map[string][]Pet
	petIndex     map[string]Pet
	batches      map[string]*batchRecord
	ownerBatches map[string][]string

	penCapacity    int
	penCapacitySet bool

	breedCooldown time.Duration
	hatchDuration time.Duration

	dailyLimit    int
	dailyLimitSet bool
	daily         map[string]dailyCounter

	mutationRate float64
	random       splitMix64
	randomSeeded bool

	lastBreed   map[string]time.Time
	nextBatchID uint64
	audit       []string
}

// NewPetBreedLedger builds an empty ledger.
func NewPetBreedLedger() *PetBreedLedger {
	l := &PetBreedLedger{}
	l.initLocked()
	return l
}

func (l *PetBreedLedger) initLocked() {
	if l.pets == nil {
		l.pets = make(map[string][]Pet)
	}
	if l.petIndex == nil {
		l.petIndex = make(map[string]Pet)
	}
	if l.batches == nil {
		l.batches = make(map[string]*batchRecord)
	}
	if l.ownerBatches == nil {
		l.ownerBatches = make(map[string][]string)
	}
	if l.daily == nil {
		l.daily = make(map[string]dailyCounter)
	}
	if l.lastBreed == nil {
		l.lastBreed = make(map[string]time.Time)
	}
	if l.nextBatchID == 0 {
		l.nextBatchID = 1
	}
	if !l.randomSeeded {
		l.random.seed(defaultRandomSeed)
		l.randomSeeded = true
	}
}

// RegisterPet adds a pet to its owner pen.
func (l *PetBreedLedger) RegisterPet(p Pet) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()

	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("pet id must not be empty")
	}
	if strings.TrimSpace(p.OwnerID) == "" {
		return fmt.Errorf("pet %s must have an owner", p.ID)
	}
	if _, exists := l.petIndex[p.ID]; exists {
		return fmt.Errorf("pet %s is already registered", p.ID)
	}
	for _, parentID := range p.ParentIDs {
		if strings.TrimSpace(parentID) == "" {
			return fmt.Errorf("pet %s has an empty parent id", p.ID)
		}
		if parentID == p.ID {
			return fmt.Errorf("pet %s cannot be its own parent", p.ID)
		}
	}
	if l.penCapacitySet && len(l.pets[p.OwnerID]) >= l.penCapacity {
		return fmt.Errorf("pen for %s is full", p.OwnerID)
	}

	cp := clonePet(p)
	l.pets[p.OwnerID] = append(l.pets[p.OwnerID], cp)
	l.petIndex[cp.ID] = clonePet(cp)
	l.appendAuditLocked("register pet=%s owner=%s", cp.ID, cp.OwnerID)
	return nil
}

// SetPenCapacity configures how many pets a pen can hold.
func (l *PetBreedLedger) SetPenCapacity(n int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()

	if n < 0 {
		return fmt.Errorf("pen capacity must not be negative")
	}
	l.penCapacity = n
	l.penCapacitySet = true
	return nil
}

// SetBreedCooldown configures the cooldown of a breeding pair.
func (l *PetBreedLedger) SetBreedCooldown(d time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()

	if d < 0 {
		return fmt.Errorf("breed cooldown must not be negative")
	}
	l.breedCooldown = d
	return nil
}

// SetHatchDuration configures how long a batch must incubate.
func (l *PetBreedLedger) SetHatchDuration(d time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()

	if d < 0 {
		return fmt.Errorf("hatch duration must not be negative")
	}
	l.hatchDuration = d
	return nil
}

// SetDailyLimit configures how many breedings one owner may run per day.
func (l *PetBreedLedger) SetDailyLimit(n int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()

	if n < 0 {
		return fmt.Errorf("daily limit must not be negative")
	}
	l.dailyLimit = n
	l.dailyLimitSet = true
	return nil
}

// SetMutationRate configures the mutation probability of an offspring.
func (l *PetBreedLedger) SetMutationRate(rate float64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()

	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate > 1 {
		return fmt.Errorf("mutation rate must be between 0 and 1")
	}
	l.mutationRate = rate
	return nil
}

// SetRandSeed makes inheritance and mutation reproducible.
func (l *PetBreedLedger) SetRandSeed(seed int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()

	l.random.seed(seed)
	l.randomSeeded = true
	return nil
}

// Breed produces one batch from a pair of pets.
func (l *PetBreedLedger) Breed(parentA, parentB, ownerID string, at time.Time) (BreedResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()

	if strings.TrimSpace(ownerID) == "" {
		return BreedResult{}, fmt.Errorf("owner id must not be empty")
	}
	if parentA == parentB {
		return BreedResult{}, fmt.Errorf("a pet cannot breed with itself")
	}

	pa, ok := l.petIndex[parentA]
	if !ok {
		return BreedResult{}, fmt.Errorf("parent %s is not registered", parentA)
	}
	pb, ok := l.petIndex[parentB]
	if !ok {
		return BreedResult{}, fmt.Errorf("parent %s is not registered", parentB)
	}
	if pa.OwnerID != ownerID || pb.OwnerID != ownerID {
		return BreedResult{}, fmt.Errorf("both parents must belong to %s", ownerID)
	}
	if l.relatedLocked(pa, pb) {
		return BreedResult{}, fmt.Errorf("inbreeding between %s and %s is not allowed", parentA, parentB)
	}

	key := pairKey(ownerID, parentA, parentB)
	if last, exists := l.lastBreed[key]; exists && at.Before(last.Add(l.breedCooldown)) {
		return BreedResult{}, fmt.Errorf("pair %s/%s is still cooling down", parentA, parentB)
	}
	if l.dailyLimitSet {
		if l.dailyForLocked(ownerID, at).Count >= l.dailyLimit {
			return BreedResult{}, fmt.Errorf("daily breeding limit reached for %s", ownerID)
		}
	}

	expired := l.expiredPendingLocked(ownerID, at)
	if l.pendingCountLocked(ownerID)-len(expired) >= maxPendingBatches {
		return BreedResult{}, fmt.Errorf("pending batch limit reached for %s", ownerID)
	}
	l.releaseBatchesLocked(expired, at)

	batchID := l.newBatchIDLocked()
	offspring := l.makeOffspringLocked(batchID, pa, pb, ownerID)
	result := BreedResult{
		BatchID:   batchID,
		OwnerID:   ownerID,
		ParentA:   pa.ID,
		ParentB:   pb.ID,
		Offspring: offspring,
		At:        at,
		HatchedAt: at.Add(l.hatchDuration),
	}

	l.batches[batchID] = &batchRecord{result: cloneBreedResult(result)}
	l.ownerBatches[ownerID] = append(l.ownerBatches[ownerID], batchID)
	l.lastBreed[key] = at

	counter := l.dailyForLocked(ownerID, at)
	counter.Count++
	l.daily[ownerID] = counter

	l.appendAuditLocked("breed batch=%s owner=%s parents=%s/%s at=%s",
		batchID, ownerID, result.ParentA, result.ParentB, formatTime(at))
	return cloneBreedResult(result), nil
}

// Batch returns a stored breeding batch.
func (l *PetBreedLedger) Batch(batchID string) (BreedResult, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()

	rec, ok := l.batches[batchID]
	if !ok {
		return BreedResult{}, false
	}
	return cloneBreedResult(rec.result), true
}

// Batches lists the batch ids of an owner.
func (l *PetBreedLedger) Batches(ownerID string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()

	return append([]string(nil), l.ownerBatches[ownerID]...)
}

// Claim moves the offspring of one batch into the owner pen.
func (l *PetBreedLedger) Claim(batchID, ownerID string, at time.Time) ([]Pet, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()

	if strings.TrimSpace(ownerID) == "" {
		return nil, fmt.Errorf("owner id must not be empty")
	}
	rec, ok := l.batches[batchID]
	if !ok {
		return nil, fmt.Errorf("batch %s does not exist", batchID)
	}
	if rec.result.OwnerID != ownerID {
		return nil, fmt.Errorf("batch %s does not belong to %s", batchID, ownerID)
	}
	if rec.released {
		return nil, fmt.Errorf("batch %s has already been released", batchID)
	}
	if rec.result.Claimed {
		return nil, fmt.Errorf("batch %s has already been claimed", batchID)
	}
	if l.expiredLocked(rec, at) {
		l.releaseBatchLocked(batchID, rec, at)
		return nil, fmt.Errorf("batch %s expired after %s", batchID, pendingRetention)
	}
	if at.Before(rec.result.HatchedAt) {
		return nil, fmt.Errorf("batch %s has not hatched yet", batchID)
	}
	if l.penCapacitySet && len(l.pets[ownerID])+len(rec.result.Offspring) > l.penCapacity {
		return nil, fmt.Errorf("pen for %s does not have room for batch %s", ownerID, batchID)
	}
	if err := l.validateClaimPetsLocked(rec.result.Offspring, make(map[string]bool)); err != nil {
		return nil, err
	}

	claimed := make([]Pet, 0, len(rec.result.Offspring))
	for _, child := range rec.result.Offspring {
		cp := normalizeOffspring(child, rec.result, ownerID)
		l.pets[ownerID] = append(l.pets[ownerID], cp)
		l.petIndex[cp.ID] = clonePet(cp)
		claimed = append(claimed, clonePet(cp))
	}
	rec.result.Claimed = true
	rec.claimedAt = at
	l.appendAuditLocked("claim batch=%s owner=%s count=%d at=%s",
		batchID, ownerID, len(claimed), formatTime(at))
	return claimed, nil
}

// ClaimAll claims every pending batch of an owner atomically.
func (l *PetBreedLedger) ClaimAll(ownerID string, at time.Time) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()

	if strings.TrimSpace(ownerID) == "" {
		return 0, fmt.Errorf("owner id must not be empty")
	}

	var activeIDs []string
	var active []*batchRecord
	var expiredIDs []string
	for _, batchID := range l.ownerBatches[ownerID] {
		rec, ok := l.batches[batchID]
		if !ok || rec.result.Claimed || rec.released {
			continue
		}
		if l.expiredLocked(rec, at) {
			expiredIDs = append(expiredIDs, batchID)
			continue
		}
		activeIDs = append(activeIDs, batchID)
		active = append(active, rec)
	}

	total := 0
	seen := make(map[string]bool)
	for _, rec := range active {
		if at.Before(rec.result.HatchedAt) {
			return 0, fmt.Errorf("batch %s has not hatched yet", rec.result.BatchID)
		}
		total += len(rec.result.Offspring)
		if err := l.validateClaimPetsLocked(rec.result.Offspring, seen); err != nil {
			return 0, err
		}
	}
	if l.penCapacitySet && len(l.pets[ownerID])+total > l.penCapacity {
		return 0, fmt.Errorf("pen for %s does not have room for all pending batches", ownerID)
	}

	l.releaseBatchesLocked(expiredIDs, at)
	for i, rec := range active {
		batchID := activeIDs[i]
		for _, child := range rec.result.Offspring {
			cp := normalizeOffspring(child, rec.result, ownerID)
			l.pets[ownerID] = append(l.pets[ownerID], cp)
			l.petIndex[cp.ID] = clonePet(cp)
		}
		rec.result.Claimed = true
		rec.claimedAt = at
		l.appendAuditLocked("claim batch=%s owner=%s count=%d at=%s",
			batchID, ownerID, len(rec.result.Offspring), formatTime(at))
	}
	if len(active) > 1 {
		l.appendAuditLocked("claim-all owner=%s batches=%d pets=%d at=%s",
			ownerID, len(active), total, formatTime(at))
	}
	return total, nil
}

// Pets lists the pets currently in an owner pen.
func (l *PetBreedLedger) Pets(ownerID string) []Pet {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()

	return clonePets(l.pets[ownerID])
}

// DailyBreeds returns how many breedings an owner ran today.
func (l *PetBreedLedger) DailyBreeds(ownerID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()

	return l.daily[ownerID].Count
}

// AuditTrail returns the audit lines of the ledger.
func (l *PetBreedLedger) AuditTrail() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()

	return append([]string(nil), l.audit...)
}

// MergeRemote stores a batch replicated from another server.
func (l *PetBreedLedger) MergeRemote(result BreedResult) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()

	if strings.TrimSpace(result.BatchID) == "" {
		return fmt.Errorf("remote batch id must not be empty")
	}
	if strings.TrimSpace(result.OwnerID) == "" {
		return fmt.Errorf("remote batch %s must have an owner", result.BatchID)
	}
	if strings.TrimSpace(result.ParentA) == "" || strings.TrimSpace(result.ParentB) == "" {
		return fmt.Errorf("remote batch %s must be bound to two parents", result.BatchID)
	}
	if result.ParentA == result.ParentB {
		return fmt.Errorf("remote batch %s must have distinct parents", result.BatchID)
	}

	remote := cloneBreedResult(result)
	for i := range remote.Offspring {
		remote.Offspring[i] = normalizeOffspring(remote.Offspring[i], remote, result.OwnerID)
	}
	seen := make(map[string]bool)
	for _, child := range remote.Offspring {
		if strings.TrimSpace(child.ID) == "" {
			return fmt.Errorf("remote batch %s has an offspring without an id", result.BatchID)
		}
		if seen[child.ID] {
			return fmt.Errorf("remote batch %s contains duplicate offspring %s", result.BatchID, child.ID)
		}
		seen[child.ID] = true
	}

	if rec, exists := l.batches[result.BatchID]; exists {
		if sameBatch(rec.result, remote) {
			return nil
		}
		return fmt.Errorf("batch %s already exists with different data", result.BatchID)
	}

	ref := result.At
	if ref.IsZero() {
		ref = result.HatchedAt
	}
	expired := l.expiredPendingLocked(result.OwnerID, ref)
	if !remote.Claimed && l.pendingCountLocked(result.OwnerID)-len(expired) >= maxPendingBatches {
		return fmt.Errorf("pending batch limit reached for %s", result.OwnerID)
	}
	l.releaseBatchesLocked(expired, ref)

	l.batches[result.BatchID] = &batchRecord{result: cloneBreedResult(remote)}
	l.ownerBatches[result.OwnerID] = append(l.ownerBatches[result.OwnerID], result.BatchID)

	key := pairKey(result.OwnerID, result.ParentA, result.ParentB)
	if last, ok := l.lastBreed[key]; !ok || result.At.After(last) {
		l.lastBreed[key] = result.At
	}
	l.appendAuditLocked("merge-remote batch=%s owner=%s parents=%s/%s at=%s",
		result.BatchID, result.OwnerID, result.ParentA, result.ParentB, formatTime(result.At))
	return nil
}

// Snapshot serialises the ledger for a restart.
func (l *PetBreedLedger) Snapshot() ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()

	snap := ledgerSnapshot{
		Version:        ledgerSnapshotVersion,
		Pets:           make(map[string][]Pet, len(l.pets)),
		NextBatchID:    l.nextBatchID,
		PenCapacity:    l.penCapacity,
		PenCapacitySet: l.penCapacitySet,
		BreedCooldown:  l.breedCooldown,
		HatchDuration:  l.hatchDuration,
		DailyLimit:     l.dailyLimit,
		DailyLimitSet:  l.dailyLimitSet,
		MutationRate:   l.mutationRate,
		RandomState:    l.random.state,
		RandomSeeded:   l.randomSeeded,
		Daily:          make(map[string]dailyCounter, len(l.daily)),
		LastBreed:      make(map[string]time.Time, len(l.lastBreed)),
		Audit:          append([]string(nil), l.audit...),
	}
	for owner, pets := range l.pets {
		snap.Pets[owner] = clonePets(pets)
	}
	owners := make([]string, 0, len(l.ownerBatches))
	for owner := range l.ownerBatches {
		owners = append(owners, owner)
	}
	sort.Strings(owners)
	for _, owner := range owners {
		for _, batchID := range l.ownerBatches[owner] {
			rec, ok := l.batches[batchID]
			if !ok {
				continue
			}
			snap.Batches = append(snap.Batches, batchSnapshot{
				Result:    cloneBreedResult(rec.result),
				Released:  rec.released,
				ReleaseAt: rec.releaseAt,
				ClaimedAt: rec.claimedAt,
			})
		}
	}
	for owner, counter := range l.daily {
		snap.Daily[owner] = counter
	}
	for key, at := range l.lastBreed {
		snap.LastBreed[key] = at
	}
	return json.Marshal(snap)
}

// Restore loads a snapshot produced by Snapshot.
func (l *PetBreedLedger) Restore(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("snapshot is empty")
	}

	var snap ledgerSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("decode snapshot: %w", err)
	}
	if snap.Version != ledgerSnapshotVersion {
		return fmt.Errorf("unsupported snapshot version %d", snap.Version)
	}
	if snap.PenCapacitySet && snap.PenCapacity < 0 {
		return fmt.Errorf("snapshot has a negative pen capacity")
	}
	if snap.DailyLimitSet && snap.DailyLimit < 0 {
		return fmt.Errorf("snapshot has a negative daily limit")
	}
	if snap.BreedCooldown < 0 || snap.HatchDuration < 0 {
		return fmt.Errorf("snapshot has a negative duration")
	}
	if math.IsNaN(snap.MutationRate) || math.IsInf(snap.MutationRate, 0) ||
		snap.MutationRate < 0 || snap.MutationRate > 1 {
		return fmt.Errorf("snapshot has an invalid mutation rate")
	}

	pets := make(map[string][]Pet, len(snap.Pets))
	petIndex := make(map[string]Pet)
	for owner, ownerPets := range snap.Pets {
		if strings.TrimSpace(owner) == "" {
			return fmt.Errorf("snapshot contains an empty owner id")
		}
		for _, p := range ownerPets {
			if strings.TrimSpace(p.ID) == "" {
				return fmt.Errorf("snapshot contains a pet without an id")
			}
			cp := clonePet(p)
			if cp.OwnerID == "" {
				cp.OwnerID = owner
			}
			if cp.OwnerID != owner {
				return fmt.Errorf("pet %s is stored in the wrong pen", cp.ID)
			}
			if _, exists := petIndex[cp.ID]; exists {
				return fmt.Errorf("snapshot contains duplicate pet %s", cp.ID)
			}
			pets[owner] = append(pets[owner], cp)
			petIndex[cp.ID] = clonePet(cp)
		}
	}

	batches := make(map[string]*batchRecord, len(snap.Batches))
	ownerBatches := make(map[string][]string)
	for _, sb := range snap.Batches {
		if strings.TrimSpace(sb.Result.BatchID) == "" {
			return fmt.Errorf("snapshot contains a batch without an id")
		}
		if strings.TrimSpace(sb.Result.OwnerID) == "" {
			return fmt.Errorf("snapshot batch %s has no owner", sb.Result.BatchID)
		}
		if _, exists := batches[sb.Result.BatchID]; exists {
			return fmt.Errorf("snapshot contains duplicate batch %s", sb.Result.BatchID)
		}
		rec := &batchRecord{
			result:    cloneBreedResult(sb.Result),
			released:  sb.Released,
			releaseAt: sb.ReleaseAt,
			claimedAt: sb.ClaimedAt,
		}
		batches[rec.result.BatchID] = rec
		ownerBatches[rec.result.OwnerID] = append(ownerBatches[rec.result.OwnerID], rec.result.BatchID)
	}

	nextBatchID := snap.NextBatchID
	if nextBatchID == 0 {
		nextBatchID = 1
	}
	daily := make(map[string]dailyCounter, len(snap.Daily))
	for owner, counter := range snap.Daily {
		if counter.Count < 0 {
			return fmt.Errorf("snapshot has a negative daily count for %s", owner)
		}
		daily[owner] = counter
	}
	lastBreed := make(map[string]time.Time, len(snap.LastBreed))
	for key, at := range snap.LastBreed {
		lastBreed[key] = at
	}
	random := splitMix64{state: snap.RandomState}
	randomSeeded := snap.RandomSeeded
	if !randomSeeded {
		random.seed(defaultRandomSeed)
		randomSeeded = true
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.pets = pets
	l.petIndex = petIndex
	l.batches = batches
	l.ownerBatches = ownerBatches
	l.penCapacity = snap.PenCapacity
	l.penCapacitySet = snap.PenCapacitySet
	l.breedCooldown = snap.BreedCooldown
	l.hatchDuration = snap.HatchDuration
	l.dailyLimit = snap.DailyLimit
	l.dailyLimitSet = snap.DailyLimitSet
	l.daily = daily
	l.mutationRate = snap.MutationRate
	l.random = random
	l.randomSeeded = randomSeeded
	l.lastBreed = lastBreed
	l.nextBatchID = nextBatchID
	l.audit = append([]string(nil), snap.Audit...)
	return nil
}

type ledgerSnapshot struct {
	Version        int                     `json:"version"`
	Pets           map[string][]Pet        `json:"pets"`
	Batches        []batchSnapshot         `json:"batches"`
	NextBatchID    uint64                  `json:"next_batch_id"`
	PenCapacity    int                     `json:"pen_capacity"`
	PenCapacitySet bool                    `json:"pen_capacity_set"`
	BreedCooldown  time.Duration           `json:"breed_cooldown"`
	HatchDuration  time.Duration           `json:"hatch_duration"`
	DailyLimit     int                     `json:"daily_limit"`
	DailyLimitSet  bool                    `json:"daily_limit_set"`
	MutationRate   float64                 `json:"mutation_rate"`
	RandomState    uint64                  `json:"random_state"`
	RandomSeeded   bool                    `json:"random_seeded"`
	Daily          map[string]dailyCounter `json:"daily"`
	LastBreed      map[string]time.Time    `json:"last_breed"`
	Audit          []string                `json:"audit"`
}

type batchSnapshot struct {
	Result    BreedResult `json:"result"`
	Released  bool        `json:"released"`
	ReleaseAt time.Time   `json:"release_at"`
	ClaimedAt time.Time   `json:"claimed_at"`
}

func (l *PetBreedLedger) newBatchIDLocked() string {
	for {
		id := fmt.Sprintf("batch-%d", l.nextBatchID)
		l.nextBatchID++
		if _, exists := l.batches[id]; !exists {
			return id
		}
	}
}

func (l *PetBreedLedger) makeOffspringLocked(batchID string, pa, pb Pet, ownerID string) []Pet {
	generation := pa.Generation
	if pb.Generation > generation {
		generation = pb.Generation
	}
	generation++

	count := 1 + l.random.intn(2)
	offspring := make([]Pet, 0, count)
	for i := 0; i < count; i++ {
		species := pa.Species
		if pb.Species != pa.Species && l.random.intn(2) == 0 {
			species = pb.Species
		}
		traits := l.inheritTraitsLocked(pa, pb)
		l.mutateTraitsLocked(traits)
		offspring = append(offspring, Pet{
			ID:         fmt.Sprintf("%s-kid-%d", batchID, i+1),
			Species:    species,
			OwnerID:    ownerID,
			ParentIDs:  []string{pa.ID, pb.ID},
			Traits:     traits,
			Generation: generation,
		})
	}
	return offspring
}

func (l *PetBreedLedger) inheritTraitsLocked(pa, pb Pet) map[string]int {
	keys := make([]string, 0, len(pa.Traits)+len(pb.Traits))
	seen := make(map[string]bool, len(pa.Traits)+len(pb.Traits))
	for key := range pa.Traits {
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	for key := range pb.Traits {
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	traits := make(map[string]int, len(keys))
	for _, key := range keys {
		aValue, aOK := pa.Traits[key]
		bValue, bOK := pb.Traits[key]
		switch {
		case aOK && bOK:
			if l.random.intn(2) == 0 {
				traits[key] = aValue
			} else {
				traits[key] = bValue
			}
		case aOK:
			traits[key] = aValue
		case bOK:
			traits[key] = bValue
		}
	}
	return traits
}

func (l *PetBreedLedger) mutateTraitsLocked(traits map[string]int) {
	if len(traits) == 0 || l.mutationRate <= 0 {
		return
	}
	if l.random.float64() >= l.mutationRate {
		return
	}

	keys := make([]string, 0, len(traits))
	for key := range traits {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	key := keys[l.random.intn(len(keys))]
	delta := 1 + l.random.intn(5)
	if l.random.intn(2) == 0 {
		delta = -delta
	}
	traits[key] += delta
}

func (l *PetBreedLedger) relatedLocked(a, b Pet) bool {
	if a.ID == b.ID {
		return true
	}
	aLineage := l.lineageLocked(a.ID)
	bLineage := l.lineageLocked(b.ID)
	for id := range aLineage {
		if bLineage[id] {
			return true
		}
	}
	return false
}

func (l *PetBreedLedger) lineageLocked(id string) map[string]bool {
	lineage := make(map[string]bool)
	visited := make(map[string]bool)
	stack := []string{id}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[current] {
			continue
		}
		visited[current] = true
		lineage[current] = true
		if pet, ok := l.petIndex[current]; ok {
			stack = append(stack, pet.ParentIDs...)
		}
	}
	return lineage
}

func (l *PetBreedLedger) validateClaimPetsLocked(offspring []Pet, seen map[string]bool) error {
	for _, child := range offspring {
		if strings.TrimSpace(child.ID) == "" {
			return fmt.Errorf("batch contains an offspring without an id")
		}
		if seen[child.ID] {
			return fmt.Errorf("batch contains duplicate offspring %s", child.ID)
		}
		seen[child.ID] = true
		if _, exists := l.petIndex[child.ID]; exists {
			return fmt.Errorf("pet %s is already in a pen", child.ID)
		}
	}
	return nil
}

func (l *PetBreedLedger) dailyForLocked(ownerID string, at time.Time) dailyCounter {
	day := at.UTC().Format("2006-01-02")
	counter := l.daily[ownerID]
	if counter.Day != day {
		counter = dailyCounter{Day: day}
	}
	return counter
}

func (l *PetBreedLedger) pendingCountLocked(ownerID string) int {
	count := 0
	for _, batchID := range l.ownerBatches[ownerID] {
		rec, ok := l.batches[batchID]
		if ok && !rec.result.Claimed && !rec.released {
			count++
		}
	}
	return count
}

func (l *PetBreedLedger) expiredPendingLocked(ownerID string, at time.Time) []string {
	var expired []string
	for _, batchID := range l.ownerBatches[ownerID] {
		rec, ok := l.batches[batchID]
		if ok && !rec.result.Claimed && !rec.released && l.expiredLocked(rec, at) {
			expired = append(expired, batchID)
		}
	}
	return expired
}

func (l *PetBreedLedger) expiredLocked(rec *batchRecord, at time.Time) bool {
	if rec.result.Claimed || rec.released || rec.result.At.IsZero() {
		return false
	}
	return !at.Before(rec.result.At.Add(pendingRetention))
}

func (l *PetBreedLedger) releaseBatchLocked(batchID string, rec *batchRecord, at time.Time) {
	if rec.released {
		return
	}
	rec.released = true
	rec.releaseAt = at
	l.appendAuditLocked("release batch=%s owner=%s count=%d reason=expired at=%s",
		batchID, rec.result.OwnerID, len(rec.result.Offspring), formatTime(at))
}

func (l *PetBreedLedger) releaseBatchesLocked(batchIDs []string, at time.Time) {
	for _, batchID := range batchIDs {
		if rec, ok := l.batches[batchID]; ok {
			l.releaseBatchLocked(batchID, rec, at)
		}
	}
}

func (l *PetBreedLedger) appendAuditLocked(format string, args ...any) {
	l.audit = append(l.audit, fmt.Sprintf(format, args...))
}

func pairKey(ownerID, a, b string) string {
	if a > b {
		a, b = b, a
	}
	return ownerID + "\x00" + a + "\x00" + b
}

func formatTime(at time.Time) string {
	return at.UTC().Format(time.RFC3339Nano)
}

func normalizeOffspring(child Pet, batch BreedResult, ownerID string) Pet {
	cp := clonePet(child)
	cp.OwnerID = ownerID
	if len(cp.ParentIDs) == 0 {
		cp.ParentIDs = []string{batch.ParentA, batch.ParentB}
	}
	return cp
}

func clonePet(p Pet) Pet {
	clone := p
	clone.ParentIDs = append([]string(nil), p.ParentIDs...)
	if p.Traits != nil {
		clone.Traits = make(map[string]int, len(p.Traits))
		for key, value := range p.Traits {
			clone.Traits[key] = value
		}
	}
	return clone
}

func clonePets(pets []Pet) []Pet {
	if pets == nil {
		return nil
	}
	clone := make([]Pet, len(pets))
	for i, p := range pets {
		clone[i] = clonePet(p)
	}
	return clone
}

func cloneBreedResult(result BreedResult) BreedResult {
	clone := result
	clone.Offspring = clonePets(result.Offspring)
	return clone
}

func sameBatch(a, b BreedResult) bool {
	if a.BatchID != b.BatchID || a.OwnerID != b.OwnerID ||
		a.ParentA != b.ParentA || a.ParentB != b.ParentB ||
		!a.At.Equal(b.At) || !a.HatchedAt.Equal(b.HatchedAt) {
		return false
	}
	return reflect.DeepEqual(a.Offspring, b.Offspring)
}
