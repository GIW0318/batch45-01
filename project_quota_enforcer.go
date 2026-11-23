package task

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Sentinel errors of the quota enforcer.
var (
	ErrProjectNotFound  = errors.New("project not found")
	ErrDuplicateProject = errors.New("duplicate project")
	ErrQuotaExceeded    = errors.New(ReasonQuotaExceeded)
	ErrReservationGone  = errors.New("reservation gone")
)

// ReasonQuotaExceeded is reported when a charge would break a hard limit.
const ReasonQuotaExceeded = "project quota exceeded"

const defaultReservationTTL = 30 * time.Second

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

// ReservationID identifies a pending quota reservation.
type ReservationID = uint64

// Reservation is quota held by one writer until it is committed or expires.
type Reservation struct {
	ID        uint64
	Project   ProjectID
	User      string
	Usage     Usage
	ExpiresAt time.Time
	Allowed   bool
	Reason    string
	Charged   Usage
}

type fileKey struct {
	dev uint64
	ino uint64
}

type projectState struct {
	desc       ProjectDesc
	used       Usage
	users      map[string]Usage
	files      map[fileKey]struct{}
	graceStart time.Time
	graceSet   bool
}

// ProjectQuotaEnforcer enforces per-project disk quotas.
type ProjectQuotaEnforcer struct {
	mu              sync.Mutex
	projects        map[ProjectID]*projectState
	reservations    map[uint64]*Reservation
	nextReservation uint64
	reservationTTL  time.Duration
}

// NewProjectQuotaEnforcer creates an empty enforcer.
func NewProjectQuotaEnforcer() *ProjectQuotaEnforcer {
	return &ProjectQuotaEnforcer{
		projects:       make(map[ProjectID]*projectState),
		reservations:   make(map[uint64]*Reservation),
		reservationTTL: defaultReservationTTL,
	}
}

func (e *ProjectQuotaEnforcer) initLocked() {
	if e.projects == nil {
		e.projects = make(map[ProjectID]*projectState)
	}
	if e.reservations == nil {
		e.reservations = make(map[uint64]*Reservation)
	}
	if e.reservationTTL <= 0 {
		e.reservationTTL = defaultReservationTTL
	}
}

// AddProject registers a project with its limits and parent.
func (e *ProjectQuotaEnforcer) AddProject(desc ProjectDesc) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.initLocked()

	if _, ok := e.projects[desc.ID]; ok {
		return ErrDuplicateProject
	}
	if desc.Parent == desc.ID {
		return ErrProjectNotFound
	}
	if desc.Parent != 0 {
		if _, ok := e.projects[desc.Parent]; !ok {
			return ErrProjectNotFound
		}
	}
	if desc.Root != "" {
		desc.Root = filepath.Clean(desc.Root)
	}
	e.projects[desc.ID] = &projectState{
		desc:  desc,
		users: make(map[string]Usage),
		files: make(map[fileKey]struct{}),
	}
	return nil
}

// Charge accounts usage of one user against a project.
func (e *ProjectQuotaEnforcer) Charge(project ProjectID, user string, delta Usage) (ChargeResult, error) {
	return e.ChargeAt(project, user, delta, time.Now())
}

// ChargeAt is Charge with an explicit clock reading.
func (e *ProjectQuotaEnforcer) ChargeAt(project ProjectID, user string, delta Usage, now time.Time) (ChargeResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.initLocked()

	st, ok := e.projects[project]
	if !ok {
		return ChargeResult{}, ErrProjectNotFound
	}
	lim, err := e.effectiveLimitLocked(project)
	if err != nil {
		return ChargeResult{}, err
	}
	if !e.admitLocked(st, delta, lim, now, true) {
		return ChargeResult{Allowed: false, Reason: ReasonQuotaExceeded}, nil
	}
	e.addUsageLocked(st, user, delta)
	e.refreshGraceLocked(st)
	return ChargeResult{Allowed: true, Charged: delta}, nil
}

// Release gives usage back, for example after a delete.
func (e *ProjectQuotaEnforcer) Release(project ProjectID, delta Usage) (Usage, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.initLocked()

	st, ok := e.projects[project]
	if !ok {
		return Usage{}, ErrProjectNotFound
	}
	e.releaseUsageLocked(st, delta)
	e.refreshGraceLocked(st)
	return st.used, nil
}

// UsageOf returns the accounted usage of a project.
func (e *ProjectQuotaEnforcer) UsageOf(project ProjectID) (Usage, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.initLocked()

	st, ok := e.projects[project]
	if !ok {
		return Usage{}, ErrProjectNotFound
	}
	return st.used, nil
}

// UserUsageOf returns the usage attributed to one writer of a project.
func (e *ProjectQuotaEnforcer) UserUsageOf(project ProjectID, user string) (Usage, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.initLocked()

	st, ok := e.projects[project]
	if !ok {
		return Usage{}, ErrProjectNotFound
	}
	return st.users[user], nil
}

// EffectiveLimit returns the limit after applying parent limits.
func (e *ProjectQuotaEnforcer) EffectiveLimit(project ProjectID) (Limits, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.initLocked()
	return e.effectiveLimitLocked(project)
}

// GraceDeadline reports when the grace period of a project ends.
func (e *ProjectQuotaEnforcer) GraceDeadline(project ProjectID) (time.Time, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.initLocked()

	st, ok := e.projects[project]
	if !ok {
		return time.Time{}, false, ErrProjectNotFound
	}
	if !st.graceSet {
		return time.Time{}, false, nil
	}
	return st.graceStart.Add(st.desc.GracePeriod), true, nil
}

// CountFile accounts one file, deduplicating hard links.
func (e *ProjectQuotaEnforcer) CountFile(project ProjectID, f FileStat) (Usage, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.initLocked()

	st, ok := e.projects[project]
	if !ok {
		return Usage{}, ErrProjectNotFound
	}
	if f.Symlink {
		return st.used, nil
	}

	key := fileKey{dev: f.Dev, ino: f.Ino}
	if _, seen := st.files[key]; seen {
		return st.used, nil
	}

	delta := Usage{Bytes: allocatedBytes(f), Inodes: 1}
	lim, err := e.effectiveLimitLocked(project)
	if err != nil {
		return Usage{}, err
	}
	if !e.admitLocked(st, delta, lim, time.Now(), true) {
		return Usage{}, ErrQuotaExceeded
	}
	e.addUsageLocked(st, "", delta)
	st.files[key] = struct{}{}
	e.refreshGraceLocked(st)
	return st.used, nil
}

// Rename moves accounted usage between two projects.
func (e *ProjectQuotaEnforcer) Rename(from, to ProjectID, delta Usage) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.initLocked()

	src, ok := e.projects[from]
	if !ok {
		return ErrProjectNotFound
	}
	dst, ok := e.projects[to]
	if !ok {
		return ErrProjectNotFound
	}
	if from == to {
		return nil
	}
	lim, err := e.effectiveLimitLocked(to)
	if err != nil {
		return err
	}
	if exceeds(dst.used, delta, lim) {
		return ErrQuotaExceeded
	}

	e.releaseUsageLocked(src, delta)
	e.addUsageLocked(dst, "", delta)
	e.refreshGraceLocked(src)
	e.refreshGraceLocked(dst)
	return nil
}

// Truncate adjusts usage after a file shrank or grew.
func (e *ProjectQuotaEnforcer) Truncate(project ProjectID, oldSize, newSize int64) (Usage, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.initLocked()

	st, ok := e.projects[project]
	if !ok {
		return Usage{}, ErrProjectNotFound
	}
	if oldSize < 0 {
		oldSize = 0
	}
	if newSize < 0 {
		newSize = 0
	}
	if newSize > oldSize {
		delta := Usage{Bytes: uint64(newSize - oldSize)}
		lim, err := e.effectiveLimitLocked(project)
		if err != nil {
			return Usage{}, err
		}
		if exceeds(st.used, delta, lim) {
			return Usage{}, ErrQuotaExceeded
		}
		e.addUsageLocked(st, "", delta)
	} else if oldSize > newSize {
		e.releaseUsageLocked(st, Usage{Bytes: uint64(oldSize - newSize)})
	}
	e.refreshGraceLocked(st)
	return st.used, nil
}

// UpdateLimits changes the limits of an existing project.
func (e *ProjectQuotaEnforcer) UpdateLimits(project ProjectID, limits Limits) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.initLocked()

	st, ok := e.projects[project]
	if !ok {
		return ErrProjectNotFound
	}
	st.desc.Limits = limits
	if st.desc.GracePeriod > 0 && !st.graceSet {
		lim, err := e.effectiveLimitLocked(project)
		if err != nil {
			return err
		}
		if exceeds(st.used, Usage{}, lim) {
			st.graceStart = time.Now()
			st.graceSet = true
		}
	}
	e.refreshGraceLocked(st)
	return nil
}

// AttributePath maps a path, including snapshot dirs, to its project.
func (e *ProjectQuotaEnforcer) AttributePath(path string) (ProjectID, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.initLocked()

	clean := filepath.Clean(path)
	var (
		bestID   ProjectID
		bestRoot string
		found    bool
	)
	for id, st := range e.projects {
		root := st.desc.Root
		if root == "" || !pathWithin(clean, root) {
			continue
		}
		if !found || len(root) > len(bestRoot) || (len(root) == len(bestRoot) && id < bestID) {
			bestID = id
			bestRoot = root
			found = true
		}
	}
	if found {
		return bestID, nil
	}
	if id, ok := e.attributeSnapshotPathLocked(clean); ok {
		return id, nil
	}
	return 0, ErrProjectNotFound
}

// Reserve holds project quota for one writer until it is committed or expires.
func (e *ProjectQuotaEnforcer) Reserve(project ProjectID, user string, delta Usage, opts ...interface{}) (*Reservation, error) {
	return e.ReserveAt(project, user, delta, opts...)
}

// ReserveAt is Reserve with an explicit clock reading.
func (e *ProjectQuotaEnforcer) ReserveAt(project ProjectID, user string, delta Usage, opts ...interface{}) (*Reservation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.initLocked()

	now := time.Now()
	for _, opt := range opts {
		if v, ok := opt.(time.Time); ok && !v.IsZero() {
			now = v
		}
	}
	expiresAt := time.Time{}
	for _, opt := range opts {
		switch v := opt.(type) {
		case time.Duration:
			expiresAt = now.Add(v)
		}
	}
	if expiresAt.IsZero() {
		expiresAt = now.Add(e.reservationTTL)
	}

	st, ok := e.projects[project]
	if !ok {
		return nil, ErrProjectNotFound
	}
	lim, err := e.effectiveLimitLocked(project)
	if err != nil {
		return nil, err
	}
	if !e.admitLocked(st, delta, lim, now, true) {
		return nil, ErrQuotaExceeded
	}

	e.addUsageLocked(st, user, delta)
	e.refreshGraceLocked(st)
	e.nextReservation++
	r := &Reservation{
		ID:        e.nextReservation,
		Project:   project,
		User:      user,
		Usage:     delta,
		ExpiresAt: expiresAt,
		Allowed:   true,
		Charged:   delta,
	}
	e.reservations[r.ID] = r
	cp := *r
	return &cp, nil
}

// CommitReservation keeps a successful reservation charged.
func (e *ProjectQuotaEnforcer) CommitReservation(reservation interface{}) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.initLocked()

	id, ok := reservationID(reservation)
	if !ok {
		return ErrReservationGone
	}
	if _, ok := e.reservations[id]; !ok {
		return ErrReservationGone
	}
	delete(e.reservations, id)
	return nil
}

// CancelReservation drops an uncommitted reservation and reclaims its quota.
func (e *ProjectQuotaEnforcer) CancelReservation(reservation interface{}) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.initLocked()

	id, ok := reservationID(reservation)
	if !ok {
		return ErrReservationGone
	}
	r, ok := e.reservations[id]
	if !ok {
		return ErrReservationGone
	}
	delete(e.reservations, id)
	if st, ok := e.projects[r.Project]; ok {
		e.releaseUsageLocked(st, r.Usage)
		e.refreshGraceLocked(st)
	}
	return nil
}

// ExpiredReservations reclaims reservations whose deadline has passed.
func (e *ProjectQuotaEnforcer) ExpiredReservations(times ...time.Time) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.initLocked()

	now := time.Now()
	if len(times) > 0 {
		now = times[0]
	}
	expired := make([]*Reservation, 0)
	for id, r := range e.reservations {
		if !now.Before(r.ExpiresAt) {
			expired = append(expired, r)
			delete(e.reservations, id)
		}
	}
	for _, r := range expired {
		if st, ok := e.projects[r.Project]; ok {
			e.releaseUsageLocked(st, r.Usage)
			e.refreshGraceLocked(st)
		}
	}
	return len(expired), nil
}

// ExpireReservations is an alias for ExpiredReservations.
func (e *ProjectQuotaEnforcer) ExpireReservations(now time.Time) (int, error) {
	return e.ExpiredReservations(now)
}

func (e *ProjectQuotaEnforcer) findReservationLocked(id uint64) *Reservation {
	return e.reservations[id]
}

func (e *ProjectQuotaEnforcer) effectiveLimitLocked(project ProjectID) (Limits, error) {
	lim := Limits{Bytes: ^uint64(0), Inodes: ^uint64(0)}
	seen := make(map[ProjectID]struct{})
	cur := project
	for {
		if _, ok := seen[cur]; ok {
			return Limits{}, ErrProjectNotFound
		}
		seen[cur] = struct{}{}
		st, ok := e.projects[cur]
		if !ok {
			return Limits{}, ErrProjectNotFound
		}
		if st.desc.Limits.Bytes < lim.Bytes {
			lim.Bytes = st.desc.Limits.Bytes
		}
		if st.desc.Limits.Inodes < lim.Inodes {
			lim.Inodes = st.desc.Limits.Inodes
		}
		if st.desc.Parent == 0 {
			break
		}
		cur = st.desc.Parent
	}
	return lim, nil
}

func (e *ProjectQuotaEnforcer) admitLocked(st *projectState, delta Usage, lim Limits, now time.Time, allowGrace bool) bool {
	if !exceeds(st.used, delta, lim) {
		return true
	}
	if !allowGrace || st.desc.GracePeriod <= 0 {
		return false
	}
	if !st.graceSet {
		st.graceStart = now
		st.graceSet = true
		return true
	}
	return now.Before(st.graceStart.Add(st.desc.GracePeriod))
}

func (e *ProjectQuotaEnforcer) refreshGraceLocked(st *projectState) {
	lim, err := e.effectiveLimitLocked(st.desc.ID)
	if err != nil {
		return
	}
	if !exceeds(st.used, Usage{}, lim) {
		st.graceStart = time.Time{}
		st.graceSet = false
	}
}

func (e *ProjectQuotaEnforcer) addUsageLocked(st *projectState, user string, delta Usage) {
	st.used.Bytes = saturatingAdd(st.used.Bytes, delta.Bytes)
	st.used.Inodes = saturatingAdd(st.used.Inodes, delta.Inodes)
	if user != "" {
		u := st.users[user]
		u.Bytes = saturatingAdd(u.Bytes, delta.Bytes)
		u.Inodes = saturatingAdd(u.Inodes, delta.Inodes)
		st.users[user] = u
	}
}

func (e *ProjectQuotaEnforcer) releaseUsageLocked(st *projectState, delta Usage) {
	st.used.Bytes = saturatingSub(st.used.Bytes, delta.Bytes)
	st.used.Inodes = saturatingSub(st.used.Inodes, delta.Inodes)

	remaining := delta
	for user, used := range st.users {
		takeBytes := minUint64(used.Bytes, remaining.Bytes)
		takeInodes := minUint64(used.Inodes, remaining.Inodes)
		used.Bytes -= takeBytes
		used.Inodes -= takeInodes
		if used.Bytes == 0 && used.Inodes == 0 {
			delete(st.users, user)
		} else {
			st.users[user] = used
		}
		remaining.Bytes -= takeBytes
		remaining.Inodes -= takeInodes
		if remaining.Bytes == 0 && remaining.Inodes == 0 {
			break
		}
	}
}

func (e *ProjectQuotaEnforcer) attributeSnapshotPathLocked(clean string) (ProjectID, bool) {
	markers := map[string]struct{}{
		".snapshot":  {},
		".snapshots": {},
		".backup":    {},
		"backup":     {},
		"backups":    {},
	}
	sep := string(filepath.Separator)
	parts := strings.Split(clean, sep)
	for i, part := range parts {
		if _, ok := markers[part]; !ok {
			continue
		}
		base := strings.Join(parts[:i], sep)
		if base == "" {
			base = sep
		}
		candidates := []string{base}
		if i+1 < len(parts) {
			candidates = append(candidates, filepath.Join(base, parts[i+1]))
		}
		for _, candidate := range candidates {
			candidate = filepath.Clean(candidate)
			var (
				best    ProjectID
				bestLen int
				found   bool
			)
			for id, st := range e.projects {
				if st.desc.Root != "" && candidate == st.desc.Root && (!found || len(st.desc.Root) > bestLen) {
					best = id
					bestLen = len(st.desc.Root)
					found = true
				}
			}
			if found {
				return best, true
			}
		}
	}
	return 0, false
}

func reservationID(v interface{}) (uint64, bool) {
	switch id := v.(type) {
	case *Reservation:
		if id == nil {
			return 0, false
		}
		return id.ID, true
	case Reservation:
		return id.ID, true
	case uint64:
		return id, true
	case uint32:
		return uint64(id), true
	case int:
		if id < 0 {
			return 0, false
		}
		return uint64(id), true
	case int64:
		if id < 0 {
			return 0, false
		}
		return uint64(id), true
	default:
		return 0, false
	}
}

func pathWithin(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if path == root {
		return true
	}
	if root == string(filepath.Separator) {
		return strings.HasPrefix(path, root)
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

func allocatedBytes(f FileStat) uint64 {
	if f.Blocks <= 0 || f.BlockSize <= 0 {
		return 0
	}
	blocks := uint64(f.Blocks)
	blockSize := uint64(f.BlockSize)
	if blocks > ^uint64(0)/blockSize {
		return ^uint64(0)
	}
	return blocks * blockSize
}

func exceeds(used, delta Usage, lim Limits) bool {
	if used.Bytes > lim.Bytes || used.Inodes > lim.Inodes {
		return true
	}
	if delta.Bytes > lim.Bytes-used.Bytes {
		return true
	}
	return delta.Inodes > lim.Inodes-used.Inodes
}

func saturatingAdd(a, b uint64) uint64 {
	if ^uint64(0)-a < b {
		return ^uint64(0)
	}
	return a + b
}

func saturatingSub(a, b uint64) uint64 {
	if b > a {
		return 0
	}
	return a - b
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
