package task

import "errors"

// PAMStatus mirrors the PAM return codes the module hands back to sshd. The
// zero value is "unknown" so an unimplemented path can never read as success.
type PAMStatus int

const (
	StatusUnknown PAMStatus = iota
	StatusSuccess
	StatusAuthErr
	StatusIgnore
	StatusAbort
	StatusSessionErr
	StatusAccountExpired
)

// ModuleSpec is one PAM module in the stack.
type ModuleSpec struct {
	Name  string
	Stage string // "auth", "account", "password" or "session"
}

// AuthState carries the per-module authentication state. Authenticate must be
// safe to call from several goroutines at once.
type AuthState struct{}

// PamGuard is the PAM module implementation. Stateless helpers hang off the
// type; per-request state lives in AuthState.
type PamGuard struct{}

// MapRetval maps a raw PAM return code to PAMStatus. 0 is PAM_SUCCESS and must
// map to success, not to a failure.
func (g *PamGuard) MapRetval(code int) PAMStatus {
	return StatusUnknown
}

// HandlePrompt handles a conversation response. A nil response (the
// application passed NULL) is a conversation failure and must abort, not
// silently succeed.
func (g *PamGuard) HandlePrompt(resp *string) (PAMStatus, error) {
	return StatusUnknown, errors.New("not implemented")
}

// NextRetryStatus caps the retry loop: once attempts reaches max the module
// must abort; below the cap it asks for another attempt.
func (g *PamGuard) NextRetryStatus(attempts, max int) (PAMStatus, error) {
	return StatusUnknown, errors.New("not implemented")
}

// WipePassword overwrites the password buffer in place so the plaintext does
// not stay in memory.
func (g *PamGuard) WipePassword(buf []byte) error {
	return errors.New("not implemented")
}

// OrderModules returns the module stack in the only safe order: auth, account,
// password, session. No module may run after a rejection, and no stage may
// allow before the auth stage has decided.
func (g *PamGuard) OrderModules(mods []ModuleSpec) ([]ModuleSpec, error) {
	return nil, errors.New("not implemented")
}

// CheckAccountExpiry denies the account stage once the account expiry time has
// passed.
func (g *PamGuard) CheckAccountExpiry(expiredAt, now int64) (PAMStatus, error) {
	return StatusUnknown, errors.New("not implemented")
}

// CloseSessionAfterFailure reports whether the session has to be closed: a
// session that was opened and whose handling failed must be torn down.
func (g *PamGuard) CloseSessionAfterFailure(opened bool, status PAMStatus) (bool, error) {
	return false, errors.New("not implemented")
}

// SanitizeEnv removes environment variables that can hijack the loader, such
// as LD_LIBRARY_PATH and LD_PRELOAD.
func (g *PamGuard) SanitizeEnv(env []string) ([]string, error) {
	return nil, errors.New("not implemented")
}

// RedactLog masks the plaintext password in a log message.
func (g *PamGuard) RedactLog(msg string) (string, error) {
	return "", errors.New("not implemented")
}

// CheckTimeout reports whether the authentication deadline has passed.
func (g *PamGuard) CheckTimeout(startedAt, now, timeout int64) (bool, error) {
	return false, errors.New("not implemented")
}

// ResolveConfig decides what happens when the configuration cannot be parsed.
// A parse failure must deny, never fall back to allowing.
func (g *PamGuard) ResolveConfig(parseOK bool) (PAMStatus, error) {
	return StatusUnknown, errors.New("not implemented")
}

// Authenticate validates one authentication attempt. Concurrent calls for
// different users must not affect each other.
func (s *AuthState) Authenticate(user, password string) (PAMStatus, error) {
	return StatusUnknown, errors.New("not implemented")
}
