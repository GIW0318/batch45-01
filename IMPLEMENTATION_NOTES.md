# Pet Breed Ledger Implementation Notes

## Behaviour

- Every batch has a unique id and is permanently bound to its owner and parents.
- Claims require the exact batch id and owner, a reached hatch time, and pen space.
- A pair has one litter per cooldown window. Breeding is serialized, so concurrent
  attempts produce one success; daily limits and cooldowns are committed only after
  all validation succeeds.
- Lineage checks reject self, parent/child, sibling, and other shared-ancestor pairs.
- `ClaimAll` validates hatch state, capacity, and duplicate ids before mutating any
  batch or pen, so failure leaves the ledger unchanged.
- Remote batches are merged idempotently and remain claimable by their owner.
- Unclaimed batches expire after 30 days; expiry is written to the audit trail as a
  release notification and frees the pending slot. Each owner is capped at 32
  unclaimed batches.
- Snapshots include pets, batches, claim/release state, configuration, daily counts,
  pair cooldowns, audit lines, and the PRNG state, so restart claims and the random
  sequence both continue.

## Random Source Memo

Inheritance and mutation use an injected-seed, deterministic SplitMix64 generator
held inside the ledger. No server clock, `time.Now`, package-level `math/rand`, or
global random source is used. The generator state is serialized in snapshots; a
restored ledger continues the exact same sequence.

## Verification

`go test ./...`, `go test -race ./...`, and `go vet ./...` pass. Additional local
self-checks covered the 32-batch cap, 30-day release, sibling inbreeding, atomic
`ClaimAll`, concurrent breeding, and restart RNG/claim continuation.
