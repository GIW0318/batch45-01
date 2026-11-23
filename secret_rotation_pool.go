package task

import (
	"errors"
	"fmt"
	"net/url"
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

	mu sync.Mutex
}

// SecretRotationPool rotates database passwords without breaking the pool. It
// holds no global state.
type SecretRotationPool struct{}

// CheckConnVersion verifies that a pooled connection was opened with the
// currently active password version.
func (g *SecretRotationPool) CheckConnVersion(connVersion, activeVersion int) (Verdict, error) {
	if connVersion < 0 || activeVersion < 0 {
		return VerdictDeny, nil
	}
	if connVersion == activeVersion {
		return VerdictAllow, nil
	}
	return VerdictDeny, nil
}

// RetireOldSecret decides whether the previous password may be removed from
// memory. It must stay available until the drain has finished.
func (g *SecretRotationPool) RetireOldSecret(drained bool) (Verdict, error) {
	if drained {
		return VerdictAllow, nil
	}
	return VerdictDeny, nil
}

// RebuildDSN builds the connection string used for reconnects. It has to carry
// the current password instead of the one captured when the pool was created.
func (g *SecretRotationPool) RebuildDSN(dsn, user, password string) (string, error) {
	if strings.TrimSpace(dsn) == "" {
		return "", errors.New("rebuild dsn: empty dsn")
	}
	if password == "" {
		return "", errors.New("rebuild dsn: empty password")
	}

	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", fmt.Errorf("rebuild dsn: %w", err)
		}
		if u.Scheme == "" {
			return "", errors.New("rebuild dsn: missing scheme")
		}
		if user == "" && u.User != nil {
			user = u.User.Username()
		}
		if user == "" {
			return "", errors.New("rebuild dsn: empty user")
		}
		u.User = url.UserPassword(user, password)
		return u.String(), nil
	}

	return rebuildKeyValueDSN(dsn, user, password)
}

// CheckReplicaConsistency verifies that every replica reports the same
// password version.
func (g *SecretRotationPool) CheckReplicaConsistency(versions []int) (Verdict, error) {
	if len(versions) == 0 {
		return VerdictDeny, nil
	}

	first := versions[0]
	if first < 0 {
		return VerdictDeny, nil
	}
	for _, version := range versions[1:] {
		if version != first {
			return VerdictDeny, nil
		}
	}
	return VerdictAllow, nil
}

// Rollback returns the secret the pool has to use after a rotation attempt. If
// the new secret failed validation the previous one is restored.
func (g *SecretRotationPool) Rollback(previous, current Secret, newValid bool) (Secret, error) {
	if newValid {
		return current, nil
	}
	return previous, nil
}

// LockSecret marks a secret as mlocked so it cannot be swapped out.
func (g *SecretRotationPool) LockSecret(s Secret, mlockOK bool) (Secret, error) {
	if s.Locked {
		return s, nil
	}
	if !mlockOK {
		return s, errors.New("lock secret: mlock failed")
	}
	s.Locked = true
	return s, nil
}

// RedactDSN removes the password from a DSN before it is logged.
func (g *SecretRotationPool) RedactDSN(dsn string) (string, error) {
	if strings.TrimSpace(dsn) == "" {
		return "", nil
	}

	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", fmt.Errorf("redact dsn: %w", err)
		}
		if u.User != nil {
			u.User = url.User(u.User.Username())
		}
		if u.RawQuery != "" {
			query := u.Query()
			if _, ok := query["password"]; ok {
				query.Set("password", "REDACTED")
				u.RawQuery = query.Encode()
			}
		}
		return u.String(), nil
	}

	return redactKeyValueDSN(dsn)
}

// ApplyPropagationDelay reports whether the switch may proceed. Every replica
// has to acknowledge the new password first.
func (g *SecretRotationPool) ApplyPropagationDelay(acked, total int) (Verdict, error) {
	if total <= 0 || acked != total {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// DrainPool returns the connections that may stay open, which are exactly the
// ones already using activeVersion.
func (g *SecretRotationPool) DrainPool(conns []Conn, activeVersion int) ([]Conn, error) {
	kept := make([]Conn, 0, len(conns))
	for _, conn := range conns {
		if conn.SecretVersion == activeVersion {
			kept = append(kept, conn)
		}
	}
	return kept, nil
}

// ValidateNewSecret decides whether the new password may be switched to.
func (g *SecretRotationPool) ValidateNewSecret(valid bool) (Verdict, error) {
	if valid {
		return VerdictAllow, nil
	}
	return VerdictDeny, nil
}

// LoadPersisted restores the newest persisted secret after a restart.
func (g *SecretRotationPool) LoadPersisted(saved []Secret) (Secret, error) {
	if len(saved) == 0 {
		return Secret{}, errors.New("load persisted: no saved secrets")
	}

	newest := saved[0]
	for _, secret := range saved[1:] {
		switch {
		case secret.Version > newest.Version:
			newest = secret
		case secret.Version == newest.Version && secret.Value != newest.Value:
			return Secret{}, fmt.Errorf("load persisted: conflicting values for version %d", newest.Version)
		}
	}
	return newest, nil
}

// Rotate installs a new password version. Concurrent calls with the same
// version must converge on a single new version; the returned value is the
// version in effect afterwards.
func (r *Rotator) Rotate(version int, value string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	current := r.Version
	if r.Active.Version > current {
		current = r.Active.Version
	}

	if version < 0 {
		return current, fmt.Errorf("rotate: invalid version %d", version)
	}
	if value == "" {
		return current, errors.New("rotate: empty secret")
	}

	if current == 0 && r.Active.Value == "" {
		r.Version = version
		r.Active = Secret{Version: version, Value: value}
		return version, nil
	}

	if version < current {
		return current, nil
	}
	if version == current {
		if r.Active.Version != version || r.Active.Value == "" {
			r.Active = Secret{Version: version, Value: value}
		}
		return current, nil
	}

	r.Version = version
	r.Active = Secret{Version: version, Value: value}
	return version, nil
}

func rebuildKeyValueDSN(dsn, user, password string) (string, error) {
	fields := strings.Fields(dsn)
	if len(fields) == 0 {
		return "", errors.New("rebuild dsn: empty dsn")
	}

	foundUser := false
	foundPassword := false
	for i, field := range fields {
		key, _, ok := strings.Cut(field, "=")
		if !ok {
			return "", fmt.Errorf("rebuild dsn: invalid field %q", field)
		}
		switch strings.ToLower(key) {
		case "user":
			if user == "" {
				user = strings.Trim(strings.TrimPrefix(field[len(key):], "="), "'\"")
				foundUser = true
				continue
			}
			fields[i] = key + "=" + quoteKeyValue(user)
			foundUser = true
		case "password":
			fields[i] = key + "=" + quoteKeyValue(password)
			foundPassword = true
		}
	}

	if user == "" {
		return "", errors.New("rebuild dsn: empty user")
	}
	if !foundUser {
		fields = append(fields, "user="+quoteKeyValue(user))
	}
	if !foundPassword {
		fields = append(fields, "password="+quoteKeyValue(password))
	}
	return strings.Join(fields, " "), nil
}

func redactKeyValueDSN(dsn string) (string, error) {
	fields := strings.Fields(dsn)
	if len(fields) == 0 {
		return "", nil
	}

	for i, field := range fields {
		key, _, ok := strings.Cut(field, "=")
		if !ok {
			return "", fmt.Errorf("redact dsn: invalid field %q", field)
		}
		if strings.EqualFold(key, "password") {
			fields[i] = key + "=REDACTED"
		}
	}
	return strings.Join(fields, " "), nil
}

func quoteKeyValue(value string) string {
	if value == "" {
		return "''"
	}
	for _, r := range value {
		if r == ' ' || r == '\t' || r == '\'' || r == '\\' {
			value = strings.ReplaceAll(value, `\`, `\\`)
			value = strings.ReplaceAll(value, `'`, `\'`)
			return "'" + value + "'"
		}
	}
	return value
}
