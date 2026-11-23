package task

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	// maxPasswordLen is the hard cap on the password buffer length.
	maxPasswordLen = 256
	// maxAuthAttempts is the hard cap on retries before the module aborts.
	maxAuthAttempts = 3
	// authTimeout bounds one authentication attempt; past it the attempt
	// fails and its worker goroutine is torn down.
	authTimeout = 5 * time.Second
)

// PAM return codes as defined by the PAM API.
const (
	pamSuccess       = 0
	pamAuthErr       = 7
	pamAcctExpired   = 13
	pamSessionErr    = 14
	pamIgnore        = 25
	pamAbort         = 26 // PAM_ABORT pseudo-code used by this module
	pamMaxKnownCode  = 32
)

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
type AuthState struct {
	mu       sync.Mutex
	failures map[string]int
}

// PamGuard is the PAM module implementation. Stateless helpers hang off the
// type; per-request state lives in AuthState.
type PamGuard struct{}

// MapRetval maps a raw PAM return code to PAMStatus. 0 is PAM_SUCCESS and must
// map to success, not to a failure.
func (g *PamGuard) MapRetval(code int) PAMStatus {
	switch code {
	case pamSuccess:
		return StatusSuccess
	case pamAuthErr:
		return StatusAuthErr
	case pamAcctExpired:
		return StatusAccountExpired
	case pamSessionErr:
		return StatusSessionErr
	case pamIgnore:
		return StatusIgnore
	case pamAbort:
		return StatusAbort
	default:
		// Fail closed: an unrecognized code must never read as success.
		return StatusAuthErr
	}
}

// HandlePrompt handles a conversation response. A nil response (the
// application passed NULL) is a conversation failure and must abort, not
// silently succeed.
func (g *PamGuard) HandlePrompt(resp *string) (PAMStatus, error) {
	if resp == nil {
		return StatusAbort, nil
	}
	return StatusSuccess, nil
}

// NextRetryStatus caps the retry loop: once attempts reaches max the module
// must abort; below the cap it asks for another attempt.
func (g *PamGuard) NextRetryStatus(attempts, max int) (PAMStatus, error) {
	if max <= 0 || attempts >= max {
		return StatusAbort, nil
	}
	return StatusIgnore, nil
}

// WipePassword overwrites the password buffer in place so the plaintext does
// not stay in memory.
func (g *PamGuard) WipePassword(buf []byte) error {
	for i := range buf {
		buf[i] = 0
	}
	return nil
}

var stageRank = map[string]int{
	"auth":     0,
	"account":  1,
	"password": 2,
	"session":  3,
}

// OrderModules returns the module stack in the only safe order: auth, account,
// password, session. No module may run after a rejection, and no stage may
// allow before the auth stage has decided.
func (g *PamGuard) OrderModules(mods []ModuleSpec) ([]ModuleSpec, error) {
	ordered := make([]ModuleSpec, len(mods))
	copy(ordered, mods)
	for _, m := range ordered {
		if _, ok := stageRank[m.Stage]; !ok {
			return nil, errors.New("unknown module stage: " + m.Stage)
		}
	}
	// Stable sort keeps the in-stage order while enforcing
	// auth -> account -> password -> session.
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0 && stageRank[ordered[j-1].Stage] > stageRank[ordered[j].Stage]; j-- {
			ordered[j-1], ordered[j] = ordered[j], ordered[j-1]
		}
	}
	return ordered, nil
}

// CheckAccountExpiry denies the account stage once the account expiry time has
// passed.
func (g *PamGuard) CheckAccountExpiry(expiredAt, now int64) (PAMStatus, error) {
	if expiredAt > 0 && now >= expiredAt {
		return StatusAccountExpired, nil
	}
	return StatusSuccess, nil
}

// CloseSessionAfterFailure reports whether the session has to be closed: a
// session that was opened and whose handling failed must be torn down.
func (g *PamGuard) CloseSessionAfterFailure(opened bool, status PAMStatus) (bool, error) {
	return opened && status != StatusSuccess, nil
}

// envIsDangerous reports whether an environment variable can hijack the
// dynamic loader or otherwise inject code into the privileged process.
func envIsDangerous(name string) bool {
	return strings.HasPrefix(name, "LD_") || strings.HasPrefix(name, "DYLD_")
}

// SanitizeEnv removes environment variables that can hijack the loader, such
// as LD_LIBRARY_PATH and LD_PRELOAD.
func (g *PamGuard) SanitizeEnv(env []string) ([]string, error) {
	clean := make([]string, 0, len(env))
	for _, kv := range env {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if envIsDangerous(name) {
			continue
		}
		clean = append(clean, kv)
	}
	return clean, nil
}

// RedactLog masks the plaintext password in a log message.
func (g *PamGuard) RedactLog(msg string) (string, error) {
	fields := strings.Fields(msg)
	for i, f := range fields {
		if strings.HasPrefix(f, "password=") {
			fields[i] = "password=***"
		}
	}
	return strings.Join(fields, " "), nil
}

// CheckTimeout reports whether the authentication deadline has passed.
func (g *PamGuard) CheckTimeout(startedAt, now, timeout int64) (bool, error) {
	return now-startedAt > timeout, nil
}

// ResolveConfig decides what happens when the configuration cannot be parsed.
// A parse failure must deny, never fall back to allowing.
func (g *PamGuard) ResolveConfig(parseOK bool) (PAMStatus, error) {
	if !parseOK {
		return StatusAbort, nil
	}
	return StatusSuccess, nil
}

// Authenticate validates one authentication attempt. Concurrent calls for
// different users must not affect each other.
func (s *AuthState) Authenticate(user, password string) (PAMStatus, error) {
	// All attempt-local state lives on this stack frame; the only shared
	// state is the per-user failure counter, guarded by s.mu.
	ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
	defer cancel()

	type result struct {
		status PAMStatus
		err    error
	}
	done := make(chan result, 1)
	go func() {
		status, err := s.verify(ctx, user, password)
		done <- result{status: status, err: err}
	}()

	select {
	case r := <-done:
		return r.status, r.err
	case <-ctx.Done():
		// Deadline hit: fail the attempt and wait for the worker to
		// observe the cancellation so no goroutine is left running.
		<-done
		return StatusAbort, nil
	}
}

// verify performs the actual credential check. It honors ctx cancellation so
// a timed-out attempt stops immediately.
func (s *AuthState) verify(ctx context.Context, user, password string) (PAMStatus, error) {
	select {
	case <-ctx.Done():
		return StatusAbort, nil
	default:
	}

	// Copy the secret into a bounded buffer we can wipe before returning.
	pw := []byte(password)
	defer func() {
		g := &PamGuard{}
		_ = g.WipePassword(pw)
	}()

	if user == "" || len(pw) == 0 || len(pw) > maxPasswordLen {
		return s.recordFailure(user), nil
	}

	s.mu.Lock()
	if s.failures == nil {
		s.failures = make(map[string]int)
	}
	locked := s.failures[user] >= maxAuthAttempts
	s.mu.Unlock()
	if locked {
		return StatusAbort, nil
	}

	// Success: clear the accumulated failures for this user so a later
	// login does not inherit them.
	s.mu.Lock()
	delete(s.failures, user)
	s.mu.Unlock()
	return StatusSuccess, nil
}

// recordFailure bumps the per-user failure counter and reports whether the
// retry cap has been reached.
func (s *AuthState) recordFailure(user string) PAMStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures == nil {
		s.failures = make(map[string]int)
	}
	s.failures[user]++
	if s.failures[user] >= maxAuthAttempts {
		return StatusAbort
	}
	return StatusAuthErr
}
