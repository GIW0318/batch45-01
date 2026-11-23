package task

import (
	"bufio"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Verdict is the outcome of a policy check. The zero value is intentionally
// "unknown": an unimplemented check must never be read as a pass.
type Verdict int

const (
	VerdictUnknown Verdict = iota
	VerdictAllow
	VerdictDeny
)

// CertType distinguishes user certificates from host certificates.
type CertType int

const (
	CertTypeUnknown CertType = iota
	CertTypeUser
	CertTypeHost
)

// Cert is an SSH certificate as presented by a client.
type Cert struct {
	Serial        uint64
	Principals    []string
	Type          CertType
	ValidAfter    int64
	ValidBefore   int64
	SourceAddress string
	KeyMatches    bool
	ForceCommand  string
	Extensions    []string
	IssuerCA      string
}

// Session is one authenticated bastion session.
type Session struct {
	Account    string
	CertSerial uint64
	OpenedAt   int64
	Chain      []string
}

// AuditRecord is the log line written for a successful certificate login.
type AuditRecord struct {
	Account    string
	Principal  string
	CertSerial uint64
	At         int64
}

// defaultMaxSessions bounds the live session table when the guard is built
// as a zero value instead of through NewSshCertGuard.
const defaultMaxSessions = 1024

// ErrSessionTableFull rejects new logins when the live session table is at
// capacity. Established sessions are unaffected.
var ErrSessionTableFull = errors.New("ssh_cert_guard: session table full")

// SshCertGuard validates SSH certificate logins on the bastion. The zero
// value is usable; shared state is guarded by a mutex.
type SshCertGuard struct {
	mu          sync.RWMutex
	crl         map[uint64]bool
	crlEpoch    uint64
	maxSessions int
	sessions    map[uint64]Session
	audited     map[uint64]AuditRecord
	ledger      *os.File
}

// NewSshCertGuard builds a guard with a bounded live session table. A
// non-positive maxSessions falls back to defaultMaxSessions.
func NewSshCertGuard(maxSessions int) *SshCertGuard {
	if maxSessions <= 0 {
		maxSessions = defaultMaxSessions
	}
	return &SshCertGuard{maxSessions: maxSessions}
}

func (g *SshCertGuard) capacity() int {
	if g.maxSessions <= 0 {
		return defaultMaxSessions
	}
	return g.maxSessions
}

// Authorize requires the certificate's principal list to contain the account
// that is being logged in to.
func (g *SshCertGuard) Authorize(cert Cert, account string) (Verdict, error) {
	if account == "" {
		return VerdictDeny, nil
	}
	for _, p := range cert.Principals {
		if p == account {
			return VerdictAllow, nil
		}
	}
	return VerdictDeny, nil
}

// EnforceForceCommand returns the effective command for the session: a
// certificate that carries a force-command option always overrides whatever
// the client asked for.
func (g *SshCertGuard) EnforceForceCommand(cert Cert, requested string) (string, error) {
	if cert.ForceCommand != "" {
		return cert.ForceCommand, nil
	}
	return requested, nil
}

// CheckSourceAddress requires remote to be covered by the certificate's
// source-address restriction.
func (g *SshCertGuard) CheckSourceAddress(cert Cert, remote netip.Addr) (Verdict, error) {
	if cert.SourceAddress == "" {
		return VerdictAllow, nil
	}
	if !remote.IsValid() {
		return VerdictDeny, nil
	}
	prefix, err := netip.ParsePrefix(cert.SourceAddress)
	if err != nil {
		addr, aerr := netip.ParseAddr(cert.SourceAddress)
		if aerr != nil {
			return VerdictDeny, nil
		}
		prefix = netip.PrefixFrom(addr, addr.BitLen())
	}
	if prefix.Contains(remote) {
		return VerdictAllow, nil
	}
	return VerdictDeny, nil
}

// CheckValidity requires now to fall inside [ValidAfter, ValidBefore); an
// expired certificate is denied, not only a not-yet-valid one.
func (g *SshCertGuard) CheckValidity(cert Cert, now int64) (Verdict, error) {
	if now < cert.ValidAfter || now >= cert.ValidBefore {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// CheckKeyBinding requires the certificate to be bound to the presented key.
func (g *SshCertGuard) CheckKeyBinding(cert Cert) (Verdict, error) {
	if !cert.KeyMatches {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// CheckExtensions requires every certificate extension to be allow listed.
func (g *SshCertGuard) CheckExtensions(cert Cert, allowed []string) (Verdict, error) {
	set := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		set[a] = true
	}
	for _, ext := range cert.Extensions {
		if !set[ext] {
			return VerdictDeny, nil
		}
	}
	return VerdictAllow, nil
}

// CheckCertType requires the certificate type to match the intended use.
func (g *SshCertGuard) CheckCertType(cert Cert, want CertType) (Verdict, error) {
	if cert.Type != want {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// CheckRevocation consults the loaded revocation list.
func (g *SshCertGuard) CheckRevocation(serial uint64, crl map[uint64]bool) (Verdict, error) {
	if crl[serial] {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// Audit builds the audit record for a successful login. The record must carry
// the certificate serial so the login can be traced back to the certificate.
func (g *SshCertGuard) Audit(s Session, cert Cert) (AuditRecord, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.audited == nil {
		g.audited = make(map[uint64]AuditRecord)
	}
	if rec, ok := g.audited[cert.Serial]; ok {
		return rec, nil
	}
	principal := ""
	if len(cert.Principals) > 0 {
		principal = cert.Principals[0]
	}
	rec := AuditRecord{
		Account:    s.Account,
		Principal:  principal,
		CertSerial: cert.Serial,
		At:         s.OpenedAt,
	}
	if g.ledger != nil {
		line := fmt.Sprintf("%d|%s|%s|%d\n", rec.CertSerial, rec.Account, rec.Principal, rec.At)
		if _, err := g.ledger.WriteString(line); err != nil {
			return AuditRecord{}, fmt.Errorf("audit ledger append: %w", err)
		}
		if err := g.ledger.Sync(); err != nil {
			return AuditRecord{}, fmt.Errorf("audit ledger sync: %w", err)
		}
	}
	g.audited[cert.Serial] = rec
	return rec, nil
}

// PropagateIdentity appends the certificate identity to the delegation chain
// so a multi-hop login keeps the original certificate identity.
func (g *SshCertGuard) PropagateIdentity(chain []string, cert Cert) ([]string, error) {
	if len(cert.Principals) == 0 {
		return nil, errors.New("ssh_cert_guard: certificate carries no principal to propagate")
	}
	next := make([]string, 0, len(chain)+1)
	next = append(next, chain...)
	next = append(next, cert.Principals[0])
	return next, nil
}

// CheckSessionAlive verifies that the session may continue: if the
// certificate expired after the session was opened, the session has to be
// disconnected.
func (g *SshCertGuard) CheckSessionAlive(s Session, cert Cert, now int64) (Verdict, error) {
	if s.CertSerial != cert.Serial {
		return VerdictDeny, nil
	}
	if now < cert.ValidAfter || now >= cert.ValidBefore {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// CheckCAAccepted requires the issuing CA to be in the currently trusted CA
// set; certificates from a rotated-out CA are refused.
func (g *SshCertGuard) CheckCAAccepted(issuer string, trusted []string) (Verdict, error) {
	for _, ca := range trusted {
		if ca == issuer {
			return VerdictAllow, nil
		}
	}
	return VerdictDeny, nil
}

// ReloadCRL swaps in a freshly loaded revocation list. The loader runs
// outside the lock; on success the new entries are merged into the current
// list (revocations are only ever added, never removed) and the CRL epoch
// advances, voiding every allowance granted against the previous list. On
// loader failure the previous list and epoch are kept intact.
func (g *SshCertGuard) ReloadCRL(load func() (map[uint64]bool, error)) error {
	loaded, err := load()
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.crl == nil {
		g.crl = make(map[uint64]bool, len(loaded))
	}
	for serial, revoked := range loaded {
		if revoked {
			g.crl[serial] = true
		}
	}
	g.crlEpoch++
	return nil
}

// CRLSnapshot returns a copy of the current revocation list and its epoch
// for stateless checks via CheckRevocation.
func (g *SshCertGuard) CRLSnapshot() (map[uint64]bool, uint64) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	snap := make(map[uint64]bool, len(g.crl))
	for serial, revoked := range g.crl {
		snap[serial] = revoked
	}
	return snap, g.crlEpoch
}

// Allowance is a pass conclusion tied to a CRL epoch. It becomes void the
// moment a reload completes, so the next connection must re-check.
type Allowance struct {
	serial uint64
	epoch  uint64
}

// GrantAllowance issues a pass conclusion for serial against the current
// CRL. It returns false when the serial is revoked.
func (g *SshCertGuard) GrantAllowance(serial uint64) (Allowance, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.crl[serial] {
		return Allowance{}, false
	}
	return Allowance{serial: serial, epoch: g.crlEpoch}, true
}

// AllowanceValid reports whether a previously granted allowance still holds:
// any completed reload (epoch change) or a newly listed serial voids it.
func (g *SshCertGuard) AllowanceValid(a Allowance) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return a.epoch == g.crlEpoch && !g.crl[a.serial]
}

// RecheckSession re-validates an established session against the current
// revocation list; a session whose serial got revoked since login is denied
// and must be disconnected.
func (g *SshCertGuard) RecheckSession(s Session) (Verdict, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.crl[s.CertSerial] {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// OpenSession registers a live session. When the table is at capacity the
// new connection is refused with ErrSessionTableFull; established sessions
// are never evicted. Re-opening an already registered serial is a no-op.
func (g *SshCertGuard) OpenSession(s Session) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sessions == nil {
		g.sessions = make(map[uint64]Session)
	}
	if _, ok := g.sessions[s.CertSerial]; ok {
		return nil
	}
	if len(g.sessions) >= g.capacity() {
		return ErrSessionTableFull
	}
	g.sessions[s.CertSerial] = s
	return nil
}

// CloseSession releases the slot held by the given certificate serial.
func (g *SshCertGuard) CloseSession(serial uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.sessions, serial)
}

// SessionCount reports how many live sessions are registered.
func (g *SshCertGuard) SessionCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.sessions)
}

// AttachLedger replays an append-only audit ledger from path and keeps it
// open for future Audit calls. After a restart, serials already present in
// the ledger are not recorded again.
func (g *SshCertGuard) AttachLedger(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open audit ledger: %w", err)
	}
	replayed := make(map[uint64]AuditRecord)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			f.Close()
			return fmt.Errorf("audit ledger: malformed line %q", line)
		}
		serial, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil {
			f.Close()
			return fmt.Errorf("audit ledger: bad serial in %q", line)
		}
		at, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil {
			f.Close()
			return fmt.Errorf("audit ledger: bad timestamp in %q", line)
		}
		replayed[serial] = AuditRecord{
			Account:    parts[1],
			Principal:  parts[2],
			CertSerial: serial,
			At:         at,
		}
	}
	if err := scanner.Err(); err != nil {
		f.Close()
		return fmt.Errorf("read audit ledger: %w", err)
	}
	if _, err := f.Seek(0, 2); err != nil {
		f.Close()
		return fmt.Errorf("seek audit ledger: %w", err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.audited == nil {
		g.audited = make(map[uint64]AuditRecord, len(replayed))
	}
	for serial, rec := range replayed {
		if _, ok := g.audited[serial]; !ok {
			g.audited[serial] = rec
		}
	}
	g.ledger = f
	return nil
}

// LoginResult carries everything a successful certificate login produced.
type LoginResult struct {
	Session Session
	Command string
	Chain   []string
	Audit   AuditRecord
}

// Login runs the full certificate login pipeline in order: principal
// mapping, source address, validity window, key binding, extension allow
// list, certificate type, trusted CA, revocation, force-command, identity
// propagation, session registration and audit. Any failed control point
// aborts the login with an error.
func (g *SshCertGuard) Login(cert Cert, account string, remote netip.Addr, requested string, allowedExt, trustedCAs, chain []string, now int64) (LoginResult, error) {
	steps := []struct {
		name string
		run  func() (Verdict, error)
	}{
		{"authorize", func() (Verdict, error) { return g.Authorize(cert, account) }},
		{"source-address", func() (Verdict, error) { return g.CheckSourceAddress(cert, remote) }},
		{"validity", func() (Verdict, error) { return g.CheckValidity(cert, now) }},
		{"key-binding", func() (Verdict, error) { return g.CheckKeyBinding(cert) }},
		{"extensions", func() (Verdict, error) { return g.CheckExtensions(cert, allowedExt) }},
		{"cert-type", func() (Verdict, error) { return g.CheckCertType(cert, CertTypeUser) }},
		{"issuer-ca", func() (Verdict, error) { return g.CheckCAAccepted(cert.IssuerCA, trustedCAs) }},
		{"revocation", func() (Verdict, error) {
			crl, _ := g.CRLSnapshot()
			return g.CheckRevocation(cert.Serial, crl)
		}},
	}
	for _, step := range steps {
		verdict, err := step.run()
		if err != nil {
			return LoginResult{}, fmt.Errorf("login %s: %w", step.name, err)
		}
		if verdict != VerdictAllow {
			return LoginResult{}, fmt.Errorf("login denied at %s", step.name)
		}
	}
	command, err := g.EnforceForceCommand(cert, requested)
	if err != nil {
		return LoginResult{}, fmt.Errorf("login force-command: %w", err)
	}
	nextChain, err := g.PropagateIdentity(chain, cert)
	if err != nil {
		return LoginResult{}, fmt.Errorf("login identity: %w", err)
	}
	session := Session{Account: account, CertSerial: cert.Serial, OpenedAt: now, Chain: nextChain}
	if err := g.OpenSession(session); err != nil {
		return LoginResult{}, err
	}
	rec, err := g.Audit(session, cert)
	if err != nil {
		return LoginResult{}, fmt.Errorf("login audit: %w", err)
	}
	return LoginResult{Session: session, Command: command, Chain: nextChain, Audit: rec}, nil
}
