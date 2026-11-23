package task

import (
	"errors"
	"time"
)

var errNotImplemented = errors.New("not implemented")

// Family is the nftables address family.
type Family string

const (
	FamilyIPv4 Family = "ip"
	FamilyIPv6 Family = "ipv6"
)

// Rule is one nftables rule.
type Rule struct {
	Chain   string
	Expr    string
	Comment string
	Family  Family
	SetRefs []string
}

// Set is one named set of the table.
type Set struct {
	Name     string
	Family   Family
	Timeout  time.Duration
	Elements []string
}

// Op is one operation of a batch.
type Op struct {
	Kind    string
	Table   string
	Payload string
}

// Batch is an atomic group of nftables operations.
type Batch struct {
	Ops []Op
}

// ReconcilerOptions configures a reconciler.
type ReconcilerOptions struct {
	Family   Family
	Table    string
	MaxBatch int
}

// NftReconciler renders and applies nftables state.
type NftReconciler struct{}

// NewNftReconciler returns a reconciler for one table.
func NewNftReconciler(opts ReconcilerOptions) *NftReconciler {
	return &NftReconciler{}
}

// DeclareChain declares a chain in the table.
func (r *NftReconciler) DeclareChain(name string) error {
	return errNotImplemented
}

// DeclareSet declares a set in the table.
func (r *NftReconciler) DeclareSet(s Set) error {
	return errNotImplemented
}

// AddRule adds a rule to a declared chain.
func (r *NftReconciler) AddRule(rule Rule) error {
	return errNotImplemented
}

// ElementBatch builds the batch that updates set elements in place.
func (r *NftReconciler) ElementBatch(setName string, add, remove []string) (*Batch, error) {
	return nil, errNotImplemented
}

// SetCounter records a packet counter for a rule.
func (r *NftReconciler) SetCounter(ruleID string, v uint64) error {
	return errNotImplemented
}

// Counters returns the current counters.
func (r *NftReconciler) Counters() map[string]uint64 {
	return nil
}

// Render produces the full table text.
func (r *NftReconciler) Render() (string, error) {
	return "", errNotImplemented
}

// RenderBatch produces the atomic batch that installs the table.
func (r *NftReconciler) RenderBatch() (*Batch, error) {
	return nil, errNotImplemented
}

// Apply executes the batch, rolling back when it fails.
func (r *NftReconciler) Apply(b *Batch, exec func(*Batch) error) error {
	return errNotImplemented
}

// Snapshot returns the last applied table text.
func (r *NftReconciler) Snapshot() (string, error) {
	return "", errNotImplemented
}

// Restore loads table text produced by Render.
func (r *NftReconciler) Restore(text string) error {
	return errNotImplemented
}
