package task

import (
	"errors"
	"net/netip"
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

// SshCertGuard validates SSH certificate logins on the bastion. It holds no
// state of its own.
type SshCertGuard struct{}

// Authorize requires the certificate's principal list to contain the account
// that is being logged in to.
func (g *SshCertGuard) Authorize(cert Cert, account string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// EnforceForceCommand returns the effective command for the session: a
// certificate that carries a force-command option always overrides whatever
// the client asked for.
func (g *SshCertGuard) EnforceForceCommand(cert Cert, requested string) (string, error) {
	return "", errors.New("not implemented")
}

// CheckSourceAddress requires remote to be covered by the certificate's
// source-address restriction.
func (g *SshCertGuard) CheckSourceAddress(cert Cert, remote netip.Addr) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckValidity requires now to fall inside [ValidAfter, ValidBefore); an
// expired certificate is denied, not only a not-yet-valid one.
func (g *SshCertGuard) CheckValidity(cert Cert, now int64) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckKeyBinding requires the certificate to be bound to the presented key.
func (g *SshCertGuard) CheckKeyBinding(cert Cert) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckExtensions requires every certificate extension to be allow listed.
func (g *SshCertGuard) CheckExtensions(cert Cert, allowed []string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckCertType requires the certificate type to match the intended use.
func (g *SshCertGuard) CheckCertType(cert Cert, want CertType) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckRevocation consults the loaded revocation list.
func (g *SshCertGuard) CheckRevocation(serial uint64, crl map[uint64]bool) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// Audit builds the audit record for a successful login. The record must carry
// the certificate serial so the login can be traced back to the certificate.
func (g *SshCertGuard) Audit(s Session, cert Cert) (AuditRecord, error) {
	return AuditRecord{}, errors.New("not implemented")
}

// PropagateIdentity appends the certificate identity to the delegation chain
// so a multi-hop login keeps the original certificate identity.
func (g *SshCertGuard) PropagateIdentity(chain []string, cert Cert) ([]string, error) {
	return nil, errors.New("not implemented")
}

// CheckSessionAlive verifies that the session may continue: if the
// certificate expired after the session was opened, the session has to be
// disconnected.
func (g *SshCertGuard) CheckSessionAlive(s Session, cert Cert, now int64) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckCAAccepted requires the issuing CA to be in the currently trusted CA
// set; certificates from a rotated-out CA are refused.
func (g *SshCertGuard) CheckCAAccepted(issuer string, trusted []string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}
