package task

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	impl "gsb/batch45-game-a-18"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func base() time.Time {
	return time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
}

func parent(id string, traits map[string]int) impl.Pet {
	return impl.Pet{ID: id, Species: "wolf", OwnerID: "acc", Traits: traits}
}

func registered(t *testing.T, l *impl.PetBreedLedger, pets ...impl.Pet) {
	t.Helper()
	for _, p := range pets {
		must(t, l.RegisterPet(p))
	}
}

func TestBatchIsBoundToItsParents(t *testing.T) {
	l := impl.NewPetBreedLedger()
	must(t, l.SetDailyLimit(50))
	must(t, l.SetHatchDuration(0))
	registered(t, l, parent("pa", map[string]int{"str": 10}), parent("pb", map[string]int{"str": 20}))
	res, err := l.Breed("pa", "pb", "acc", base())
	must(t, err)
	if res.BatchID == "" {
		t.Fatalf("batch id is empty")
	}
	if res.ParentA != "pa" || res.ParentB != "pb" {
		t.Fatalf("batch parents = %s/%s, want pa/pb", res.ParentA, res.ParentB)
	}
	stored, ok := l.Batch(res.BatchID)
	if !ok {
		t.Fatalf("batch %s was not stored", res.BatchID)
	}
	if stored.ParentA != "pa" || stored.ParentB != "pb" {
		t.Fatalf("stored batch lost its parents: %+v", stored)
	}
}

func TestClaimBeforeHatchRejected(t *testing.T) {
	l := impl.NewPetBreedLedger()
	must(t, l.SetDailyLimit(50))
	must(t, l.SetHatchDuration(0))
	must(t, l.SetHatchDuration(time.Hour))
	must(t, l.SetPenCapacity(10))
	registered(t, l, parent("pa", nil), parent("pb", nil))
	res, err := l.Breed("pa", "pb", "acc", base())
	must(t, err)
	if _, err := l.Claim(res.BatchID, "acc", base().Add(30*time.Minute)); err == nil {
		t.Fatalf("claiming before the hatch duration must fail")
	}
	if got := len(l.Pets("acc")); got != 2 {
		t.Fatalf("pets = %d, want only the 2 parents", got)
	}
}

func TestClaimAfterHatchSucceeds(t *testing.T) {
	l := impl.NewPetBreedLedger()
	must(t, l.SetDailyLimit(50))
	must(t, l.SetHatchDuration(0))
	must(t, l.SetHatchDuration(time.Hour))
	must(t, l.SetPenCapacity(10))
	registered(t, l, parent("pa", nil), parent("pb", nil))
	res, err := l.Breed("pa", "pb", "acc", base())
	must(t, err)
	if len(res.Offspring) == 0 {
		t.Fatalf("batch produced no offspring")
	}
	claimed, err := l.Claim(res.BatchID, "acc", base().Add(time.Hour))
	must(t, err)
	if len(claimed) != len(res.Offspring) {
		t.Fatalf("claimed = %d offspring, want %d", len(claimed), len(res.Offspring))
	}
	if got := len(l.Pets("acc")); got != 2+len(claimed) {
		t.Fatalf("pets = %d after claim, want %d", got, 2+len(claimed))
	}
}

func TestConcurrentBreedCreatesSingleLitter(t *testing.T) {
	l := impl.NewPetBreedLedger()
	must(t, l.SetDailyLimit(50))
	must(t, l.SetHatchDuration(0))
	must(t, l.SetBreedCooldown(time.Hour))
	must(t, l.SetPenCapacity(10))
	registered(t, l, parent("pa", nil), parent("pb", nil))
	const workers = 8
	var wg sync.WaitGroup
	success := 0
	var mu sync.Mutex
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := l.Breed("pa", "pb", "acc", base()); err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("successful concurrent breeds = %d, want exactly 1 litter", success)
	}
	if got := len(l.Batches("acc")); got != 1 {
		t.Fatalf("batches = %d, want 1", got)
	}
	if got := l.DailyBreeds("acc"); got != 1 {
		t.Fatalf("daily breeds = %d, want 1", got)
	}
}

func TestTraitsInheritedFromBothParents(t *testing.T) {
	l := impl.NewPetBreedLedger()
	must(t, l.SetDailyLimit(50))
	must(t, l.SetHatchDuration(0))
	must(t, l.SetBreedCooldown(0))
	must(t, l.SetDailyLimit(100))
	must(t, l.SetMutationRate(0))
	must(t, l.SetRandSeed(42))
	registered(t, l, parent("pa", map[string]int{"str": 10}), parent("pb", map[string]int{"str": 20}))
	seen := map[int]bool{}
	for i := 0; i < 20; i++ {
		res, err := l.Breed("pa", "pb", "acc", base().Add(time.Duration(i)*time.Minute))
		must(t, err)
		for _, child := range res.Offspring {
			seen[child.Traits["str"]] = true
		}
	}
	if !seen[10] || !seen[20] {
		t.Fatalf("inherited strengths = %v, want a random mix of both parents' 10 and 20", seen)
	}
}

func TestMutationIgnoresServerClock(t *testing.T) {
	newLedger := func() *impl.PetBreedLedger {
		l := impl.NewPetBreedLedger()
		must(t, l.SetDailyLimit(50))
		must(t, l.SetHatchDuration(0))
		must(t, l.SetBreedCooldown(0))
		must(t, l.SetDailyLimit(10))
		must(t, l.SetMutationRate(1))
		must(t, l.SetRandSeed(7))
		registered(t, l, parent("pa", map[string]int{"str": 10}), parent("pb", map[string]int{"str": 20}))
		return l
	}
	first := newLedger()
	second := newLedger()
	r1, err := first.Breed("pa", "pb", "acc", base())
	must(t, err)
	r2, err := second.Breed("pa", "pb", "acc", base().Add(24*time.Hour))
	must(t, err)
	if len(r1.Offspring) != len(r2.Offspring) {
		t.Fatalf("same seed produced %d and %d offspring", len(r1.Offspring), len(r2.Offspring))
	}
	for i := range r1.Offspring {
		if !reflect.DeepEqual(r1.Offspring[i].Traits, r2.Offspring[i].Traits) {
			t.Fatalf("offspring %d traits %v vs %v depend on the server clock",
				i, r1.Offspring[i].Traits, r2.Offspring[i].Traits)
		}
	}
}

func TestFailedBreedKeepsCooldownFree(t *testing.T) {
	l := impl.NewPetBreedLedger()
	must(t, l.SetDailyLimit(50))
	must(t, l.SetHatchDuration(0))
	must(t, l.SetBreedCooldown(time.Hour))
	must(t, l.SetPenCapacity(10))
	child := impl.Pet{ID: "pb", Species: "wolf", OwnerID: "acc", ParentIDs: []string{"pa"}}
	registered(t, l, parent("pa", nil), child, parent("pc", nil))
	if _, err := l.Breed("pa", "pb", "acc", base()); err == nil {
		t.Fatalf("inbreeding must fail")
	}
	if _, err := l.Breed("pa", "pc", "acc", base().Add(time.Second)); err != nil {
		t.Fatalf("a failed breed must not consume the pair cooldown: %v", err)
	}
	if got := l.DailyBreeds("acc"); got != 1 {
		t.Fatalf("daily breeds = %d, want only the successful 1", got)
	}
}

func TestDailyLimitConsumedOnce(t *testing.T) {
	l := impl.NewPetBreedLedger()
	must(t, l.SetDailyLimit(50))
	must(t, l.SetHatchDuration(0))
	must(t, l.SetBreedCooldown(0))
	must(t, l.SetDailyLimit(1))
	must(t, l.SetPenCapacity(10))
	registered(t, l, parent("pa", nil), parent("pb", nil), parent("pc", nil))
	if _, err := l.Breed("pa", "pb", "acc", base()); err != nil {
		must(t, err)
	}
	if _, err := l.Breed("pa", "pc", "acc", base().Add(time.Second)); err == nil {
		t.Fatalf("the second breed of the day must exceed the daily limit")
	}
	if got := l.DailyBreeds("acc"); got != 1 {
		t.Fatalf("daily breeds = %d, want 1", got)
	}
}

func TestInbreedingRejected(t *testing.T) {
	l := impl.NewPetBreedLedger()
	must(t, l.SetDailyLimit(50))
	must(t, l.SetHatchDuration(0))
	must(t, l.SetPenCapacity(10))
	registered(t, l, parent("pa", nil), parent("pc", nil))
	if _, err := l.Breed("pa", "pc", "acc", base()); err != nil {
		must(t, err)
	}
	child := impl.Pet{ID: "pb", Species: "wolf", OwnerID: "acc", ParentIDs: []string{"pa"}}
	registered(t, l, child)
	if _, err := l.Breed("pa", "pb", "acc", base().Add(time.Second)); err == nil {
		t.Fatalf("breeding a parent with its own child must fail")
	}
	if got := len(l.Batches("acc")); got != 1 {
		t.Fatalf("batches = %d after the rejected inbreeding, want only the first valid one", got)
	}
}

func TestPenFullKeepsPendingResult(t *testing.T) {
	l := impl.NewPetBreedLedger()
	must(t, l.SetDailyLimit(50))
	must(t, l.SetHatchDuration(0))
	must(t, l.SetPenCapacity(2))
	registered(t, l, parent("pa", nil), parent("pb", nil))
	res, err := l.Breed("pa", "pb", "acc", base())
	must(t, err)
	if _, err := l.Claim(res.BatchID, "acc", base().Add(time.Second)); err == nil {
		t.Fatalf("claiming into a full pen must fail")
	}
	if got := len(l.Pets("acc")); got != 2 {
		t.Fatalf("pets = %d, want the pending offspring to stay out of the full pen", got)
	}
	must(t, l.SetPenCapacity(2+len(res.Offspring)))
	claimed, err := l.Claim(res.BatchID, "acc", base().Add(2*time.Second))
	must(t, err)
	if len(claimed) != len(res.Offspring) {
		t.Fatalf("claimed = %d, want the %d pending offspring", len(claimed), len(res.Offspring))
	}
}

func TestClaimAllAtomicOnCapacityFailure(t *testing.T) {
	l := impl.NewPetBreedLedger()
	must(t, l.SetDailyLimit(50))
	must(t, l.SetHatchDuration(0))
	must(t, l.SetBreedCooldown(0))
	must(t, l.SetDailyLimit(10))
	must(t, l.SetPenCapacity(2))
	registered(t, l, parent("pa", nil), parent("pb", nil))
	r1, err := l.Breed("pa", "pb", "acc", base())
	must(t, err)
	r2, err := l.Breed("pa", "pb", "acc", base().Add(time.Second))
	must(t, err)
	total := len(r1.Offspring) + len(r2.Offspring)
	must(t, l.SetPenCapacity(3))
	if _, err := l.ClaimAll("acc", base().Add(2*time.Second)); err == nil {
		t.Fatalf("claim all with room for only one offspring must fail")
	}
	if got := len(l.Pets("acc")); got != 2 {
		t.Fatalf("pets = %d after the failed batch claim, want no partial claim", got)
	}
	must(t, l.SetPenCapacity(2+total))
	n, err := l.ClaimAll("acc", base().Add(3*time.Second))
	must(t, err)
	if n != total {
		t.Fatalf("claimed = %d, want both batches totalling %d", n, total)
	}
	if got := len(l.Pets("acc")); got != 2+total {
		t.Fatalf("pets = %d after claim all, want %d", got, 2+total)
	}
}

func TestRemoteBatchClaimable(t *testing.T) {
	l := impl.NewPetBreedLedger()
	must(t, l.SetDailyLimit(50))
	must(t, l.SetHatchDuration(0))
	must(t, l.SetPenCapacity(10))
	remote := impl.BreedResult{
		BatchID:   "remote-1",
		OwnerID:   "acc",
		ParentA:   "ra",
		ParentB:   "rb",
		Offspring: []impl.Pet{{ID: "kid-1", Species: "wolf", OwnerID: "acc"}},
		At:        base(),
		HatchedAt: base(),
	}
	must(t, l.MergeRemote(remote))
	if _, ok := l.Batch("remote-1"); !ok {
		t.Fatalf("remote batch was not stored")
	}
	claimed, err := l.Claim("remote-1", "acc", base().Add(time.Second))
	must(t, err)
	if len(claimed) != 1 {
		t.Fatalf("claimed = %d, want 1 remote offspring", len(claimed))
	}
}

func TestSnapshotRestoreKeepsBatches(t *testing.T) {
	l := impl.NewPetBreedLedger()
	must(t, l.SetDailyLimit(50))
	must(t, l.SetHatchDuration(0))
	must(t, l.SetPenCapacity(10))
	registered(t, l, parent("pa", nil), parent("pb", nil))
	res, err := l.Breed("pa", "pb", "acc", base())
	must(t, err)
	data, err := l.Snapshot()
	must(t, err)
	restored := impl.NewPetBreedLedger()
	must(t, restored.Restore(data))
	must(t, restored.SetPenCapacity(10))
	if _, ok := restored.Batch(res.BatchID); !ok {
		t.Fatalf("unclaimed batch was lost across the restart")
	}
	if _, err := restored.Claim(res.BatchID, "acc", base().Add(time.Second)); err != nil {
		t.Fatalf("claim after restore failed: %v", err)
	}
}

func TestAuditTrailRecordsBreedAndClaim(t *testing.T) {
	l := impl.NewPetBreedLedger()
	must(t, l.SetDailyLimit(50))
	must(t, l.SetHatchDuration(0))
	must(t, l.SetPenCapacity(10))
	registered(t, l, parent("pa", nil), parent("pb", nil))
	res, err := l.Breed("pa", "pb", "acc", base())
	must(t, err)
	_, err = l.Claim(res.BatchID, "acc", base().Add(time.Second))
	must(t, err)
	log := strings.Join(l.AuditTrail(), "\n")
	if log == "" {
		t.Fatalf("audit trail is empty after breed and claim")
	}
	if !strings.Contains(log, res.BatchID) {
		t.Fatalf("audit trail %q does not mention batch %s", log, res.BatchID)
	}
}

func TestClaimWrongOwnerRejected(t *testing.T) {
	l := impl.NewPetBreedLedger()
	must(t, l.SetDailyLimit(50))
	must(t, l.SetHatchDuration(0))
	must(t, l.SetPenCapacity(10))
	registered(t, l, parent("pa", nil), parent("pb", nil))
	res, err := l.Breed("pa", "pb", "acc", base())
	must(t, err)
	if _, err := l.Claim(res.BatchID, "intruder", base()); err == nil {
		t.Fatalf("claiming another owner's batch must fail")
	}
	if _, err := l.Claim(res.BatchID, "acc", base().Add(time.Second)); err != nil {
		t.Fatalf("the real owner could not claim after the rejected attempt: %v", err)
	}
}
