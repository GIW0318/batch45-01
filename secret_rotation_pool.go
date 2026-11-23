package task

import "errors"

// Verdict is the outcome of a policy check. The zero value is intentionally
// "unknown": an unimplemented check must never be read as a pass.
type Verdict int

const (
	VerdictUnknown Verdict = iota
	VerdictAllow
	VerdictDeny
)

// Secret is one version of the database password.
type Secret struct {
	Version int
	Value   string
	Locked  bool
}

// Conn is one pooled database connection.
type Conn struct {
	ID            int
	SecretVersion int
	DSN           string
}

// Rotator is the in-memory rotation state. Rotate must be safe to call from
// several goroutines at once.
type Rotator struct {
	Version int
	Active  Secret
}

// SecretRotationPool rotates database passwords without breaking the pool. It
// holds no global state.
type SecretRotationPool struct{}

// CheckConnVersion verifies that a pooled connection was opened with the
// currently active password version.
func (g *SecretRotationPool) CheckConnVersion(connVersion, activeVersion int) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// RetireOldSecret decides whether the previous password may be removed from
// memory. It must stay available until the drain has finished.
func (g *SecretRotationPool) RetireOldSecret(drained bool) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// RebuildDSN builds the connection string used for reconnects. It has to carry
// the current password instead of the one captured when the pool was created.
func (g *SecretRotationPool) RebuildDSN(dsn, user, password string) (string, error) {
	return "", errors.New("not implemented")
}

// CheckReplicaConsistency verifies that every replica reports the same
// password version.
func (g *SecretRotationPool) CheckReplicaConsistency(versions []int) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// Rollback returns the secret the pool has to use after a rotation attempt. If
// the new secret failed validation the previous one is restored.
func (g *SecretRotationPool) Rollback(previous, current Secret, newValid bool) (Secret, error) {
	return Secret{}, errors.New("not implemented")
}

// LockSecret marks a secret as mlocked so it cannot be swapped out.
func (g *SecretRotationPool) LockSecret(s Secret, mlockOK bool) (Secret, error) {
	return Secret{}, errors.New("not implemented")
}

// RedactDSN removes the password from a DSN before it is logged.
func (g *SecretRotationPool) RedactDSN(dsn string) (string, error) {
	return "", errors.New("not implemented")
}

// ApplyPropagationDelay reports whether the switch may proceed. Every replica
// has to acknowledge the new password first.
func (g *SecretRotationPool) ApplyPropagationDelay(acked, total int) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// DrainPool returns the connections that may stay open, which are exactly the
// ones already using activeVersion.
func (g *SecretRotationPool) DrainPool(conns []Conn, activeVersion int) ([]Conn, error) {
	return nil, errors.New("not implemented")
}

// ValidateNewSecret decides whether the new password may be switched to.
func (g *SecretRotationPool) ValidateNewSecret(valid bool) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// LoadPersisted restores the newest persisted secret after a restart.
func (g *SecretRotationPool) LoadPersisted(saved []Secret) (Secret, error) {
	return Secret{}, errors.New("not implemented")
}

// Rotate installs a new password version. Concurrent calls with the same
// version must converge on a single new version; the returned value is the
// version in effect afterwards.
func (r *Rotator) Rotate(version int, value string) (int, error) {
	return 0, errors.New("not implemented")
}
