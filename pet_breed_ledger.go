package task

import (
	"errors"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

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

// PetBreedLedger records breeding batches until their results are claimed.
type PetBreedLedger struct {
}

// NewPetBreedLedger builds an empty ledger.
func NewPetBreedLedger() *PetBreedLedger {
	return &PetBreedLedger{}
}

// RegisterPet adds a pet to its owner pen.
func (l *PetBreedLedger) RegisterPet(p Pet) error {
	return errNotImplemented
}

// SetPenCapacity configures how many pets a pen can hold.
func (l *PetBreedLedger) SetPenCapacity(n int) error {
	return errNotImplemented
}

// SetBreedCooldown configures the cooldown of a breeding pair.
func (l *PetBreedLedger) SetBreedCooldown(d time.Duration) error {
	return errNotImplemented
}

// SetHatchDuration configures how long a batch must incubate.
func (l *PetBreedLedger) SetHatchDuration(d time.Duration) error {
	return errNotImplemented
}

// SetDailyLimit configures how many breedings one owner may run per day.
func (l *PetBreedLedger) SetDailyLimit(n int) error {
	return errNotImplemented
}

// SetMutationRate configures the mutation probability of an offspring.
func (l *PetBreedLedger) SetMutationRate(rate float64) error {
	return errNotImplemented
}

// SetRandSeed makes inheritance and mutation reproducible.
func (l *PetBreedLedger) SetRandSeed(seed int64) error {
	return errNotImplemented
}

// Breed produces one batch from a pair of pets.
func (l *PetBreedLedger) Breed(parentA, parentB, ownerID string, at time.Time) (BreedResult, error) {
	return BreedResult{}, errNotImplemented
}

// Batch returns a stored breeding batch.
func (l *PetBreedLedger) Batch(batchID string) (BreedResult, bool) {
	return BreedResult{}, false
}

// Batches lists the batch ids of an owner.
func (l *PetBreedLedger) Batches(ownerID string) []string {
	return nil
}

// Claim moves the offspring of one batch into the owner pen.
func (l *PetBreedLedger) Claim(batchID, ownerID string, at time.Time) ([]Pet, error) {
	return nil, errNotImplemented
}

// ClaimAll claims every pending batch of an owner atomically.
func (l *PetBreedLedger) ClaimAll(ownerID string, at time.Time) (int, error) {
	return 0, errNotImplemented
}

// Pets lists the pets currently in an owner pen.
func (l *PetBreedLedger) Pets(ownerID string) []Pet {
	return nil
}

// DailyBreeds returns how many breedings an owner ran today.
func (l *PetBreedLedger) DailyBreeds(ownerID string) int {
	return 0
}

// AuditTrail returns the audit lines of the ledger.
func (l *PetBreedLedger) AuditTrail() []string {
	return nil
}

// MergeRemote stores a batch replicated from another server.
func (l *PetBreedLedger) MergeRemote(result BreedResult) error {
	return errNotImplemented
}

// Snapshot serialises the ledger for a restart.
func (l *PetBreedLedger) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore loads a snapshot produced by Snapshot.
func (l *PetBreedLedger) Restore(data []byte) error {
	return errNotImplemented
}
