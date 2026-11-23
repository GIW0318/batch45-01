package task

import (
	"errors"
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

// MacsecGuard runs the key agreement state machine of one port.
type MacsecGuard struct{}

// NewMacsecGuard creates the guard from a connectivity association key.
func NewMacsecGuard(cak []byte, suite string) (*MacsecGuard, error) {
	return nil, errors.New("not implemented")
}

// Join adds or refreshes a participant. A participant whose suite differs
// from the port suite must be rejected.
func (g *MacsecGuard) Join(p Participant) error {
	return errors.New("not implemented")
}

// Leave removes a participant from future elections.
func (g *MacsecGuard) Leave(sci string) error {
	return errors.New("not implemented")
}

// ElectKeyServer elects exactly one key server: highest priority wins and a
// tie is broken by the smallest SCI. It must never return two winners.
func (g *MacsecGuard) ElectKeyServer() (string, error) {
	return "", errors.New("not implemented")
}

// InstallSAK installs a new SAK for association number an. The replacement is
// atomic: afterwards only the new AN is usable.
func (g *MacsecGuard) InstallSAK(keyID string, sak []byte, an int) error {
	return errors.New("not implemented")
}

// ActiveSAK returns the installed SAK, or ErrCAKMissing when the guard was
// restarted without its CAK.
func (g *MacsecGuard) ActiveSAK() (SAK, error) {
	return SAK{}, errors.New("not implemented")
}

// NextPN returns the next packet number for an active AN. PN starts at 1,
// counts per SA, and stops with ErrPNExhausted once the configured maximum is
// reached.
func (g *MacsecGuard) NextPN(an int) (uint64, error) {
	return 0, errors.New("not implemented")
}

// VerifyICV checks icv against the first 16 bytes of SHA-256(sak || frame).
func (g *MacsecGuard) VerifyICV(an int, frame, icv []byte) error {
	return errors.New("not implemented")
}

// HandleControlFrame accepts strictly increasing control frame sequence
// numbers and rejects replays.
func (g *MacsecGuard) HandleControlFrame(seq uint32, payload []byte) error {
	return errors.New("not implemented")
}

// ExpireParticipants drops peers not seen within ttl.
func (g *MacsecGuard) ExpireParticipants(now time.Time, ttl time.Duration) error {
	return errors.New("not implemented")
}

// SetMaxPN configures the PN ceiling used by CheckPN. Zero means no limit.
func (g *MacsecGuard) SetMaxPN(n uint64) error {
	return errors.New("not implemented")
}

// ConfidentialityOffset returns the confidentiality offset for the port
// suite: 0 for GCM suites, 8 for the legacy CBC suite, and an error for
// unknown suites.
func (g *MacsecGuard) ConfidentialityOffset(an int) (int, error) {
	return 0, errors.New("not implemented")
}

// Restart simulates a process restart. Without a CAK the installed SAKs must
// no longer be usable.
func (g *MacsecGuard) Restart(cak []byte) error {
	return errors.New("not implemented")
}

// Counters returns the failure counters.
func (g *MacsecGuard) Counters() Counters {
	return Counters{}
}

// Participants returns the current participant list.
func (g *MacsecGuard) Participants() []Participant {
	return nil
}
