package task

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

var limitNow = time.Unix(1700000000, 0)

// crossSignedPool builds groups of cross signed intermediates: every group
// issues the next one and the last issues the first, with several copies per
// subject, so the number of simple paths explodes combinatorially.
func crossSignedPool(groups, copies int) []Cert {
	from := limitNow.Add(-time.Hour)
	to := limitNow.Add(time.Hour)
	var pool []Cert
	for g := 0; g < groups; g++ {
		for c := 0; c < copies; c++ {
			pool = append(pool, Cert{
				Subject:        fmt.Sprintf("CN=X%d", g),
				Issuer:         fmt.Sprintf("CN=X%d", (g+1)%groups),
				Serial:         fmt.Sprintf("x%d-%d", g, c),
				Fingerprint:    fmt.Sprintf("fp-x%d-%d", g, c),
				IsCA:           true,
				PathLen:        -1,
				NotBefore:      from,
				NotAfter:       to,
				SignatureValid: true,
			})
		}
	}
	return pool
}

func TestSearchNodeLimitTruncatesDeterministically(t *testing.T) {
	leaf := Cert{
		Subject:        "CN=leaf",
		Issuer:         "CN=X0",
		Serial:         "leaf",
		Fingerprint:    "fp-leaf",
		NotBefore:      limitNow.Add(-time.Hour),
		NotAfter:       limitNow.Add(time.Hour),
		SignatureValid: true,
	}
	pool := crossSignedPool(6, 4)
	b := NewChainBuilder(BuilderOptions{MaxDepth: 64, CurrentTime: limitNow})

	start := time.Now()
	_, errFirst := b.Build(leaf, pool, nil)
	elapsed := time.Since(start)
	if !errors.Is(errFirst, ErrSearchLimitExceeded) {
		t.Fatalf("a cross signing explosion must be truncated with a recognizable error, got %v", errFirst)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("the search must be truncated instead of burning CPU, took %v", elapsed)
	}

	_, errSecond := b.Build(leaf, pool, nil)
	if errFirst != errSecond {
		t.Fatalf("truncation must be deterministic, got %v then %v", errFirst, errSecond)
	}
}

func TestBuildResultStableAcrossRuns(t *testing.T) {
	from := limitNow.Add(-time.Hour)
	to := limitNow.Add(time.Hour)
	leaf := Cert{
		Subject:        "CN=leaf",
		Issuer:         "CN=Int",
		Serial:         "leaf",
		Fingerprint:    "fp-leaf",
		NotBefore:      from,
		NotAfter:       to,
		SignatureValid: true,
	}
	inter := Cert{
		Subject:        "CN=Int",
		Issuer:         "CN=Root",
		Serial:         "int",
		Fingerprint:    "fp-int",
		IsCA:           true,
		PathLen:        -1,
		NotBefore:      from,
		NotAfter:       to,
		SignatureValid: true,
	}
	root := Cert{
		Subject:        "CN=Root",
		Issuer:         "CN=Root",
		Serial:         "root",
		Fingerprint:    "fp-root",
		IsCA:           true,
		SelfSigned:     true,
		PathLen:        -1,
		NotBefore:      from,
		NotAfter:       to,
		SignatureValid: true,
	}
	pool := []Cert{inter, inter, inter}
	b := NewChainBuilder(BuilderOptions{MaxDepth: 5, CurrentTime: limitNow, PinnedAnchors: []string{"fp-root"}})

	first, err := b.Build(leaf, pool, []Cert{root})
	if err != nil {
		t.Fatalf("Build returned unexpected error: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := b.Build(leaf, pool, []Cert{root})
		if err != nil {
			t.Fatalf("Build returned unexpected error on run %d: %v", i, err)
		}
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("same input must build the same chain, got %+v then %+v", first, again)
		}
	}
}
