package task

import (
	"strings"
	"sync"
	"testing"

	. "gsb/batch45-safety-c-11"
)

func TestMapRetvalMapsSuccessCode(t *testing.T) {
	g := &PamGuard{}
	if got := g.MapRetval(0); got != StatusSuccess {
		t.Fatalf("PAM_SUCCESS must map to success, got %v", got)
	}
}

func TestMapRetvalMapsAuthFailureCode(t *testing.T) {
	g := &PamGuard{}
	if got := g.MapRetval(7); got != StatusAuthErr {
		t.Fatalf("PAM_AUTH_ERR must map to auth failure, got %v", got)
	}
}

func TestHandlePromptAbortsOnNullResponse(t *testing.T) {
	g := &PamGuard{}
	got, err := g.HandlePrompt(nil)
	if err != nil {
		t.Fatalf("HandlePrompt: %v", err)
	}
	if got != StatusAbort {
		t.Fatalf("NULL conversation response must abort, got %v", got)
	}
}

func TestHandlePromptAcceptsResponse(t *testing.T) {
	g := &PamGuard{}
	resp := "hunter2"
	got, err := g.HandlePrompt(&resp)
	if err != nil {
		t.Fatalf("HandlePrompt: %v", err)
	}
	if got != StatusSuccess {
		t.Fatalf("valid conversation response, want success, got %v", got)
	}
}

func TestNextRetryStatusAbortsAtLimit(t *testing.T) {
	g := &PamGuard{}
	got, err := g.NextRetryStatus(3, 3)
	if err != nil {
		t.Fatalf("NextRetryStatus: %v", err)
	}
	if got != StatusAbort {
		t.Fatalf("retry limit reached, must abort, got %v", got)
	}
}

func TestNextRetryStatusContinuesBelowLimit(t *testing.T) {
	g := &PamGuard{}
	got, err := g.NextRetryStatus(1, 3)
	if err != nil {
		t.Fatalf("NextRetryStatus: %v", err)
	}
	if got != StatusIgnore {
		t.Fatalf("below retry limit, must ask for another attempt, got %v", got)
	}
}

func TestWipePasswordZeroesBuffer(t *testing.T) {
	g := &PamGuard{}
	buf := []byte("hunter2")
	if err := g.WipePassword(buf); err != nil {
		t.Fatalf("WipePassword: %v", err)
	}
	for i, b := range buf {
		if b != 0 {
			t.Fatalf("byte %d still holds plaintext %q", i, b)
		}
	}
}

func TestConcurrentAuthKeepsPerCallerState(t *testing.T) {
	st := &AuthState{}
	const callers = 8
	results := make([]PAMStatus, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			status, _ := st.Authenticate("user-"+string(rune('a'+i)), "pw-"+string(rune('a'+i)))
			results[i] = status
		}(i)
	}
	wg.Wait()
	for i, status := range results {
		if status != StatusSuccess {
			t.Fatalf("concurrent caller %d got %v, want independent success", i, status)
		}
	}
}

func TestOrderModulesPutsAuthBeforeSession(t *testing.T) {
	g := &PamGuard{}
	mods := []ModuleSpec{
		{Name: "sess", Stage: "session"},
		{Name: "acct", Stage: "account"},
		{Name: "authn", Stage: "auth"},
	}
	got, err := g.OrderModules(mods)
	if err != nil {
		t.Fatalf("OrderModules: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 ordered modules, got %d", len(got))
	}
	want := []string{"auth", "account", "session"}
	for i, stage := range want {
		if got[i].Stage != stage {
			t.Fatalf("stage %d must be %q, got %q", i, stage, got[i].Stage)
		}
	}
}

func TestCheckAccountExpiryDeniesExpired(t *testing.T) {
	g := &PamGuard{}
	got, err := g.CheckAccountExpiry(1000, 2000)
	if err != nil {
		t.Fatalf("CheckAccountExpiry: %v", err)
	}
	if got != StatusAccountExpired {
		t.Fatalf("account expired at 1000, want account-expired, got %v", got)
	}
}

func TestCloseSessionAfterFailureOnSessionError(t *testing.T) {
	g := &PamGuard{}
	mustClose, err := g.CloseSessionAfterFailure(true, StatusSessionErr)
	if err != nil {
		t.Fatalf("CloseSessionAfterFailure: %v", err)
	}
	if !mustClose {
		t.Fatalf("session opened then failed, must be closed")
	}
}

func TestSanitizeEnvDropsLDPath(t *testing.T) {
	g := &PamGuard{}
	got, err := g.SanitizeEnv([]string{"PATH=/usr/bin", "LD_LIBRARY_PATH=/tmp/evil", "LD_PRELOAD=/tmp/evil.so"})
	if err != nil {
		t.Fatalf("SanitizeEnv: %v", err)
	}
	if len(got) != 1 || got[0] != "PATH=/usr/bin" {
		t.Fatalf("loader hijack variables must be dropped, got %v", got)
	}
}

func TestRedactLogMasksPassword(t *testing.T) {
	g := &PamGuard{}
	got, err := g.RedactLog("login failed user=ops password=hunter2")
	if err != nil {
		t.Fatalf("RedactLog: %v", err)
	}
	if strings.Contains(got, "hunter2") {
		t.Fatalf("log still contains the plaintext password: %q", got)
	}
	if !strings.Contains(got, "***") {
		t.Fatalf("log must show a masked password, got %q", got)
	}
}

func TestCheckTimeoutReportsExpired(t *testing.T) {
	g := &PamGuard{}
	expired, err := g.CheckTimeout(100, 140, 30)
	if err != nil {
		t.Fatalf("CheckTimeout: %v", err)
	}
	if !expired {
		t.Fatalf("deadline 130 already passed at 140, want timeout")
	}
}

func TestResolveConfigDeniesOnParseFailure(t *testing.T) {
	g := &PamGuard{}
	got, err := g.ResolveConfig(false)
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if got != StatusAbort {
		t.Fatalf("unparsable config must deny, got %v", got)
	}
}
