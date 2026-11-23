package task

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"sort"
	"sync"
	"time"
)

// Cipher suite names understood by the guard.
const (
	SuiteGCM128    = "gcm-aes-128"
	SuiteGCM256    = "gcm-aes-256"
	SuiteGCMXPN128 = "gcm-aes-xpn-128"
	SuiteCBC128    = "cbc-aes-128"
)

var (
	ErrCAKMissing    = errors.New("macsec: CAK missing")
	ErrNoWinner      = errors.New("macsec: no unique key server")
	ErrSuiteMismatch = errors.New("macsec: cipher suite mismatch")
	ErrReplay        = errors.New("macsec: control frame replay")
	ErrPNExhausted   = errors.New("macsec: packet number exhausted, rekey required")
	ErrICV           = errors.New("macsec: ICV verification failed")
	ErrInactiveAN    = errors.New("macsec: association number not active")
	ErrUnknownAN     = errors.New("macsec: association number out of range")
	ErrTableFull     = errors.New("macsec: participant table full")
)

const (
	// maxAN is the exclusive upper bound of the 2-bit AN field.
	maxAN = 4
	// maxParticipants bounds the participant table.
	maxParticipants = 256
	// maxAuditEntries bounds the installation audit trail.
	maxAuditEntries = 1024
)

// Participant is one MACsec peer.
type Participant struct {
	SCI       string
	Priority  int
	Suite     string
	LastSeen  time.Time
	KeyServer bool
}

// SAK is the secure association key currently installed for one AN.
type SAK struct {
	KeyID     string
	AN        int
	PN        uint64
	Installed time.Time
}

// Counters aggregates failure counters.
type Counters struct {
	ICVFailures int
	Replays     int
	Rekeys      int
}

// InstallRecord is one immutable entry in the installation audit trail.
type InstallRecord struct {
	KeyID     string
	AN        int
	Installed time.Time
}

// activeKey holds the single SAK that is currently allowed to carry traffic.
// At any moment at most one AN is active: installing a new SAK atomically
// replaces the previous one and makes its AN unusable.
type activeKey struct {
	keyID     string
	material  []byte
	an        int
	pn        uint64
	exhausted bool
	installed time.Time
}

// MacsecGuard runs the key agreement state machine of one port.
//
// A single mutex serializes elections and SAK installation so that they are
// mutually exclusive; two goroutines can never announce different winners or
// observe two simultaneously active ANs.
type MacsecGuard struct {
	mu sync.RWMutex

	suite string

	// cakConfigured is false only after a Restart without CAK material.
	// While false every SAK-backed operation fails with ErrCAKMissing.
	cakConfigured bool

	participants map[string]Participant

	active *activeKey

	// maxPN is the PN ceiling; zero means unlimited.
	maxPN uint64

	// ctrlSeen reports whether any control frame has been accepted and
	// lastCtrl is its highest accepted sequence number.
	ctrlSeen bool
	lastCtrl uint32

	counters Counters

	audit []InstallRecord

	// clock is indirected so audit timestamps remain deterministic in
	// tests while still using wall time by default.
	clock func() time.Time
}

// NewMacsecGuard creates the guard from a connectivity association key.
func NewMacsecGuard(cak []byte, suite string) (*MacsecGuard, error) {
	if len(cak) == 0 {
		return nil, ErrCAKMissing
	}
	if !validSuite(suite) {
		return nil, ErrSuiteMismatch
	}
	return &MacsecGuard{
		suite:         suite,
		cakConfigured: true,
		participants:  make(map[string]Participant),
		clock:         time.Now,
	}, nil
}

// Join adds or refreshes a participant. A participant whose suite differs
// from the port suite must be rejected.
func (g *MacsecGuard) Join(p Participant) error {
	if p.Suite != g.suite {
		return ErrSuiteMismatch
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.participants[p.SCI]; !exists && len(g.participants) >= maxParticipants {
		return ErrTableFull
	}
	g.participants[p.SCI] = p
	return nil
}

// Leave removes a participant from future elections.
func (g *MacsecGuard) Leave(sci string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.participants[sci]; !ok {
		return errors.New("macsec: unknown participant")
	}
	delete(g.participants, sci)
	return nil
}

// ElectKeyServer elects exactly one key server: highest priority wins and a
// tie is broken by the smallest SCI. It must never return two winners.
func (g *MacsecGuard) ElectKeyServer() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.participants) == 0 {
		return "", ErrNoWinner
	}

	winner := ""
	for sci, p := range g.participants {
		if winner == "" || higherPriority(p, g.participants[winner]) {
			winner = sci
		}
	}

	for sci, p := range g.participants {
		p.KeyServer = sci == winner
		g.participants[sci] = p
	}
	return winner, nil
}

// InstallSAK installs a new SAK for association number an. The replacement is
// atomic: afterwards only the new AN is usable.
func (g *MacsecGuard) InstallSAK(keyID string, sak []byte, an int) error {
	if an < 0 || an >= maxAN {
		return ErrUnknownAN
	}
	if len(sak) == 0 {
		return errors.New("macsec: empty SAK material")
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.cakConfigured {
		return ErrCAKMissing
	}

	material := append([]byte{}, sak...)
	now := g.clock()

	// Single-slot replacement: the previous active key (and its AN) is
	// dropped before the new one becomes visible, so two ANs can never be
	// usable at the same time.
	g.active = &activeKey{
		keyID:     keyID,
		material:  material,
		an:        an,
		pn:        0,
		installed: now,
	}

	g.audit = append(g.audit, InstallRecord{KeyID: keyID, AN: an, Installed: now})
	if len(g.audit) > maxAuditEntries {
		g.audit = g.audit[len(g.audit)-maxAuditEntries:]
	}
	return nil
}

// ActiveSAK returns the installed SAK, or ErrCAKMissing when the guard was
// restarted without its CAK.
func (g *MacsecGuard) ActiveSAK() (SAK, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if !g.cakConfigured {
		return SAK{}, ErrCAKMissing
	}
	if g.active == nil {
		return SAK{}, ErrInactiveAN
	}
	return SAK{
		KeyID:     g.active.keyID,
		AN:        g.active.an,
		PN:        g.active.pn,
		Installed: g.active.installed,
	}, nil
}

// NextPN returns the next packet number for an active AN. PN starts at 1,
// counts per SA, and stops with ErrPNExhausted once the configured maximum is
// reached.
func (g *MacsecGuard) NextPN(an int) (uint64, error) {
	if an < 0 || an >= maxAN {
		return 0, ErrUnknownAN
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.cakConfigured {
		return 0, ErrCAKMissing
	}
	if g.active == nil || g.active.an != an {
		return 0, ErrInactiveAN
	}
	if g.active.exhausted {
		return 0, ErrPNExhausted
	}

	next := g.active.pn + 1
	if g.maxPN != 0 && next > g.maxPN {
		// Force a rekey exactly once for the exhausted SA.
		g.active.exhausted = true
		g.counters.Rekeys++
		return 0, ErrPNExhausted
	}
	g.active.pn = next
	return next, nil
}

// VerifyICV checks icv against the first 16 bytes of SHA-256(sak || frame).
func (g *MacsecGuard) VerifyICV(an int, frame, icv []byte) error {
	if an < 0 || an >= maxAN {
		return ErrUnknownAN
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.cakConfigured {
		return ErrCAKMissing
	}
	if g.active == nil || g.active.an != an {
		return ErrInactiveAN
	}

	h := sha256.New()
	h.Write(g.active.material)
	h.Write(frame)
	expected := h.Sum(nil)[:16]

	if len(icv) != len(expected) || subtle.ConstantTimeCompare(icv, expected) != 1 {
		g.counters.ICVFailures++
		return ErrICV
	}
	return nil
}

// HandleControlFrame accepts strictly increasing control frame sequence
// numbers and rejects replays.
func (g *MacsecGuard) HandleControlFrame(seq uint32, payload []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ctrlSeen && seq <= g.lastCtrl {
		g.counters.Replays++
		return ErrReplay
	}
	g.ctrlSeen = true
	g.lastCtrl = seq
	return nil
}

// ExpireParticipants drops peers not seen within ttl.
func (g *MacsecGuard) ExpireParticipants(now time.Time, ttl time.Duration) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for sci, p := range g.participants {
		if now.Sub(p.LastSeen) > ttl {
			delete(g.participants, sci)
		}
	}
	return nil
}

// SetMaxPN configures the PN ceiling used by CheckPN. Zero means no limit.
func (g *MacsecGuard) SetMaxPN(n uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.maxPN = n
	return nil
}

// ConfidentialityOffset returns the confidentiality offset for the port
// suite: 0 for GCM suites, 8 for the legacy CBC suite, and an error for
// unknown suites.
func (g *MacsecGuard) ConfidentialityOffset(an int) (int, error) {
	if an < 0 || an >= maxAN {
		return 0, ErrUnknownAN
	}
	switch g.suite {
	case SuiteGCM128, SuiteGCM256, SuiteGCMXPN128:
		return 0, nil
	case SuiteCBC128:
		return 8, nil
	default:
		return 0, ErrSuiteMismatch
	}
}

// Restart simulates a process restart. Without a CAK the installed SAKs must
// no longer be usable.
func (g *MacsecGuard) Restart(cak []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(cak) == 0 {
		// Lose the key material: no SAK operation may succeed until a CAK
		// is supplied again.
		g.cakConfigured = false
		if g.active != nil {
			for i := range g.active.material {
				g.active.material[i] = 0
			}
		}
		return ErrCAKMissing
	}
	g.cakConfigured = true
	return nil
}

// Counters returns the failure counters.
func (g *MacsecGuard) Counters() Counters {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.counters
}

// Participants returns the current participant list.
func (g *MacsecGuard) Participants() []Participant {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Participant, 0, len(g.participants))
	for _, p := range g.participants {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SCI < out[j].SCI })
	return out
}

// AuditTrail returns a copy of the capped installation audit trail. The
// trail survives restarts so that every SAK installation stays traceable
// (keyID, AN and installation time).
func (g *MacsecGuard) AuditTrail() []InstallRecord {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]InstallRecord, len(g.audit))
	copy(out, g.audit)
	return out
}

// higherPriority reports whether a outranks b: larger Priority wins, and
// equal priorities are broken by the smallest SCI.
func higherPriority(a, b Participant) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	return a.SCI < b.SCI
}

func validSuite(suite string) bool {
	switch suite {
	case SuiteGCM128, SuiteGCM256, SuiteGCMXPN128, SuiteCBC128:
		return true
	default:
		return false
	}
}
