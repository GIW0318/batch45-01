package task

import (
	"errors"
	"time"
)

// Sentinel errors of the quota enforcer.
var (
	ErrProjectNotFound  = errors.New("project not found")
	ErrDuplicateProject = errors.New("duplicate project")
)

// ReasonQuotaExceeded is reported when a charge would break a hard limit.
const ReasonQuotaExceeded = "project quota exceeded"

// ProjectID identifies a quota project.
type ProjectID uint32

// Usage is the accounted consumption of a project.
type Usage struct {
	Bytes  uint64
	Inodes uint64
}

// Limits are the hard limits of a project.
type Limits struct {
	Bytes  uint64
	Inodes uint64
}

// ProjectDesc registers a project.
type ProjectDesc struct {
	ID          ProjectID
	Parent      ProjectID
	Root        string
	Limits      Limits
	GracePeriod time.Duration
	CreatedAt   time.Time
}

// ChargeResult is the outcome of a charge attempt.
type ChargeResult struct {
	Allowed bool
	Reason  string
	Charged Usage
}

// FileStat describes one file for accounting.
type FileStat struct {
	Path      string
	Dev       uint64
	Ino       uint64
	Size      int64
	Blocks    int64
	BlockSize int64
	LinkCount int64
	Symlink   bool
}

// ProjectQuotaEnforcer enforces per-project disk quotas.
type ProjectQuotaEnforcer struct {
}

// NewProjectQuotaEnforcer creates an empty enforcer.
func NewProjectQuotaEnforcer() *ProjectQuotaEnforcer {
	return &ProjectQuotaEnforcer{}
}

// AddProject registers a project with its limits and parent.
func (e *ProjectQuotaEnforcer) AddProject(desc ProjectDesc) error {
	return errors.New("not implemented")
}

// Charge accounts usage of one user against a project.
func (e *ProjectQuotaEnforcer) Charge(project ProjectID, user string, delta Usage) (ChargeResult, error) {
	return ChargeResult{}, errors.New("not implemented")
}

// ChargeAt is Charge with an explicit clock reading.
func (e *ProjectQuotaEnforcer) ChargeAt(project ProjectID, user string, delta Usage, now time.Time) (ChargeResult, error) {
	return ChargeResult{}, errors.New("not implemented")
}

// Release gives usage back, for example after a delete.
func (e *ProjectQuotaEnforcer) Release(project ProjectID, delta Usage) (Usage, error) {
	return Usage{}, errors.New("not implemented")
}

// UsageOf returns the accounted usage of a project.
func (e *ProjectQuotaEnforcer) UsageOf(project ProjectID) (Usage, error) {
	return Usage{}, errors.New("not implemented")
}

// EffectiveLimit returns the limit after applying parent limits.
func (e *ProjectQuotaEnforcer) EffectiveLimit(project ProjectID) (Limits, error) {
	return Limits{}, errors.New("not implemented")
}

// GraceDeadline reports when the grace period of a project ends.
func (e *ProjectQuotaEnforcer) GraceDeadline(project ProjectID) (time.Time, bool, error) {
	return time.Time{}, false, errors.New("not implemented")
}

// CountFile accounts one file, deduplicating hard links.
func (e *ProjectQuotaEnforcer) CountFile(project ProjectID, f FileStat) (Usage, error) {
	return Usage{}, errors.New("not implemented")
}

// Rename moves accounted usage between two projects.
func (e *ProjectQuotaEnforcer) Rename(from, to ProjectID, delta Usage) error {
	return errors.New("not implemented")
}

// Truncate adjusts usage after a file shrank or grew.
func (e *ProjectQuotaEnforcer) Truncate(project ProjectID, oldSize, newSize int64) (Usage, error) {
	return Usage{}, errors.New("not implemented")
}

// UpdateLimits changes the limits of an existing project.
func (e *ProjectQuotaEnforcer) UpdateLimits(project ProjectID, limits Limits) error {
	return errors.New("not implemented")
}

// AttributePath maps a path, including snapshot dirs, to its project.
func (e *ProjectQuotaEnforcer) AttributePath(path string) (ProjectID, error) {
	return 0, errors.New("not implemented")
}
