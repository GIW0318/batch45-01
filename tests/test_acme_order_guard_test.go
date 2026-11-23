package task

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	task "gsb/batch45-safety-c-04"
)

func account() task.Account {
	return task.Account{ID: "acct-1", KeyID: "key-1", Thumbprint: "thumb-1"}
}

func newOrder(t *testing.T, g *task.AcmeOrderGuard) task.Order {
	t.Helper()
	order, err := g.CreateOrder(account(), []string{"svc.example"})
	if err != nil {
		t.Fatalf("CreateOrder returned unexpected error: %v", err)
	}
	if order.ID == "" {
		t.Fatalf("CreateOrder returned an empty order id")
	}
	return order
}

func authorize(t *testing.T, g *task.AcmeOrderGuard, order task.Order) {
	t.Helper()
	ka, err := g.IssueChallenge(order.ID, account().ID, "http-01")
	if err != nil {
		t.Fatalf("IssueChallenge returned unexpected error: %v", err)
	}
	if ka.Token == "" || ka.Thumbprint != account().Thumbprint {
		t.Fatalf("token must be bound to the account thumbprint, got %+v", ka)
	}
	if err := g.ValidateChallenge(order.ID, ka.Token, task.ChallengeProbe{HostHeader: "svc.example"}); err != nil {
		t.Fatalf("ValidateChallenge returned unexpected error: %v", err)
	}
}

func TestChallengeTokenBoundToAccount(t *testing.T) {
	g := task.NewAcmeOrderGuard()
	order := newOrder(t, g)
	_, err := g.IssueChallenge(order.ID, "acct-2", "http-01")
	if !errors.Is(err, task.ErrAccountMismatch) {
		t.Fatalf("a token must not be minted for another account, got %v", err)
	}
}

func TestChallengeTokenReplayRejected(t *testing.T) {
	g := task.NewAcmeOrderGuard()
	order := newOrder(t, g)
	ka, err := g.IssueChallenge(order.ID, account().ID, "http-01")
	if err != nil {
		t.Fatalf("IssueChallenge returned unexpected error: %v", err)
	}
	if err := g.ValidateChallenge(order.ID, ka.Token, task.ChallengeProbe{HostHeader: "svc.example"}); err != nil {
		t.Fatalf("ValidateChallenge returned unexpected error: %v", err)
	}
	if err := g.ValidateChallenge(order.ID, ka.Token, task.ChallengeProbe{HostHeader: "svc.example"}); !errors.Is(err, task.ErrChallengeReplay) {
		t.Fatalf("a validated token must not be replayable, got %v", err)
	}
}

func TestNonceIsSingleUse(t *testing.T) {
	g := task.NewAcmeOrderGuard()
	if err := g.UseNonce("nonce-1"); err != nil {
		t.Fatalf("UseNonce returned unexpected error: %v", err)
	}
	if err := g.UseNonce("nonce-1"); !errors.Is(err, task.ErrNonceReplay) {
		t.Fatalf("a nonce must be consumable exactly once, got %v", err)
	}
}

func TestFinalizeWithoutAuthorizationRejected(t *testing.T) {
	g := task.NewAcmeOrderGuard()
	order := newOrder(t, g)
	if _, err := g.Finalize(order.ID); !errors.Is(err, task.ErrOrderState) {
		t.Fatalf("the state machine must not skip authorization, got %v", err)
	}
}

func TestHTTP01HostHeaderValidated(t *testing.T) {
	g := task.NewAcmeOrderGuard()
	order := newOrder(t, g)
	ka, err := g.IssueChallenge(order.ID, account().ID, "http-01")
	if err != nil {
		t.Fatalf("IssueChallenge returned unexpected error: %v", err)
	}
	err = g.ValidateChallenge(order.ID, ka.Token, task.ChallengeProbe{HostHeader: "evil.example"})
	if !errors.Is(err, task.ErrHostMismatch) {
		t.Fatalf("the http-01 Host header must match the order domain, got %v", err)
	}
}

func TestDNS01TXTOwnerValidated(t *testing.T) {
	g := task.NewAcmeOrderGuard()
	order := newOrder(t, g)
	ka, err := g.IssueChallenge(order.ID, account().ID, "dns-01")
	if err != nil {
		t.Fatalf("IssueChallenge returned unexpected error: %v", err)
	}
	err = g.ValidateChallenge(order.ID, ka.Token, task.ChallengeProbe{
		TXTName:  "_acme-challenge.other.example",
		TXTValue: ka.Value,
	})
	if !errors.Is(err, task.ErrTXTOwnerMismatch) {
		t.Fatalf("the TXT record must be owned by the order domain, got %v", err)
	}
	err = g.ValidateChallenge(order.ID, ka.Token, task.ChallengeProbe{
		TXTName:  "_acme-challenge.svc.example",
		TXTValue: ka.Value,
	})
	if err != nil {
		t.Fatalf("a matching TXT owner must validate, got %v", err)
	}
}

func TestRetryBudgetCapped(t *testing.T) {
	g := task.NewAcmeOrderGuard()
	delay, err := g.RetryDelay(0)
	if err != nil {
		t.Fatalf("RetryDelay returned unexpected error: %v", err)
	}
	if delay <= 0 {
		t.Fatalf("the first retry must have a positive delay, got %v", delay)
	}
	if _, err := g.RetryDelay(5); !errors.Is(err, task.ErrRetryExhausted) {
		t.Fatalf("retries must be capped so the CA is not flooded, got %v", err)
	}
}

func TestRotateKeyStalesOldOrders(t *testing.T) {
	g := task.NewAcmeOrderGuard()
	order := newOrder(t, g)
	if err := g.RotateAccountKey(account().ID, "key-2"); err != nil {
		t.Fatalf("RotateAccountKey returned unexpected error: %v", err)
	}
	if _, err := g.Finalize(order.ID); !errors.Is(err, task.ErrOrderStale) {
		t.Fatalf("an order of a rotated key must not issue a certificate, got %v", err)
	}
}

func TestRenewalFailureFallsBackWithAlert(t *testing.T) {
	g := task.NewAcmeOrderGuard()
	now := time.Unix(1700000000, 0)
	if err := g.StoreCert(task.CertRef{ID: "cert-old", Domain: "old.example", IssuedAt: now.Add(-80 * 24 * time.Hour), NotAfter: now.Add(24 * time.Hour)}); err != nil {
		t.Fatalf("StoreCert returned unexpected error: %v", err)
	}
	if err := g.SetIssuerAvailable(false); err != nil {
		t.Fatalf("SetIssuerAvailable returned unexpected error: %v", err)
	}
	res, err := g.Renew("old.example", 1)
	if err != nil {
		t.Fatalf("Renew returned unexpected error: %v", err)
	}
	if !res.FallbackUsed {
		t.Fatalf("a failed renewal must fall back to the old certificate, got %+v", res)
	}
	if !res.Alerted {
		t.Fatalf("a silent fallback must raise an alert, got %+v", res)
	}
}

func TestConcurrentReplicasIssueOneCert(t *testing.T) {
	g := task.NewAcmeOrderGuard()
	if err := g.SetIssuerAvailable(true); err != nil {
		t.Fatalf("SetIssuerAvailable returned unexpected error: %v", err)
	}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		ids  []string
		errs []error
	)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := g.Renew("svc.example", 5)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			ids = append(ids, res.Cert.ID)
		}()
	}
	wg.Wait()
	if len(errs) != 0 {
		t.Fatalf("Renew returned unexpected errors: %v", errs)
	}
	if len(ids) != 5 {
		t.Fatalf("all replicas must get a result, got %d", len(ids))
	}
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("replicas must share one certificate, got %v", ids)
		}
	}
}

func TestRevokeClearsCache(t *testing.T) {
	g := task.NewAcmeOrderGuard()
	cert := task.CertRef{ID: "cert-1", Domain: "svc.example", NotAfter: time.Unix(1800000000, 0)}
	if err := g.StoreCert(cert); err != nil {
		t.Fatalf("StoreCert returned unexpected error: %v", err)
	}
	if _, ok := g.Cached("svc.example"); !ok {
		t.Fatalf("a stored certificate must be cached")
	}
	if err := g.Revoke(cert.ID); err != nil {
		t.Fatalf("Revoke returned unexpected error: %v", err)
	}
	if _, ok := g.Cached("svc.example"); ok {
		t.Fatalf("a revoked certificate must be evicted from the cache")
	}
}

func TestSealedKeyHasNoPlaintext(t *testing.T) {
	g := task.NewAcmeOrderGuard()
	raw := []byte("-----BEGIN PRIVATE KEY-----\nverysecretkeymaterial\n-----END PRIVATE KEY-----")
	sealed, err := g.SealKey(raw)
	if err != nil {
		t.Fatalf("SealKey returned unexpected error: %v", err)
	}
	rendered := sealed.String()
	if strings.Contains(rendered, "BEGIN") || strings.Contains(rendered, "verysecretkeymaterial") {
		t.Fatalf("the sealed key must not expose plaintext, got %q", rendered)
	}
	if rendered == "" {
		t.Fatalf("the sealed key must render a redacted placeholder")
	}
}

func TestOrderCannotBeFinalizedTwice(t *testing.T) {
	g := task.NewAcmeOrderGuard()
	order := newOrder(t, g)
	authorize(t, g, order)
	if _, err := g.Finalize(order.ID); err != nil {
		t.Fatalf("Finalize returned unexpected error: %v", err)
	}
	if _, err := g.Finalize(order.ID); !errors.Is(err, task.ErrOrderState) {
		t.Fatalf("a valid order must not be finalized again, got %v", err)
	}
}

func TestRenewSuccessIssuesNewCert(t *testing.T) {
	g := task.NewAcmeOrderGuard()
	if err := g.SetIssuerAvailable(true); err != nil {
		t.Fatalf("SetIssuerAvailable returned unexpected error: %v", err)
	}
	res, err := g.Renew("new.example", 1)
	if err != nil {
		t.Fatalf("Renew returned unexpected error: %v", err)
	}
	if res.Cert.Domain != "new.example" {
		t.Fatalf("renewal must issue for the requested domain, got %+v", res)
	}
	if res.FallbackUsed || res.Alerted {
		t.Fatalf("a successful renewal must not fall back, got %+v", res)
	}
}

func TestOrderStatusAdvancesPendingReadyValid(t *testing.T) {
	g := task.NewAcmeOrderGuard()
	order := newOrder(t, g)
	status, err := g.OrderStatus(order.ID)
	if err != nil {
		t.Fatalf("OrderStatus returned unexpected error: %v", err)
	}
	if status != task.OrderPending {
		t.Fatalf("a new order must be pending, got %v", status)
	}
	authorize(t, g, order)
	status, err = g.OrderStatus(order.ID)
	if err != nil {
		t.Fatalf("OrderStatus returned unexpected error: %v", err)
	}
	if status != task.OrderReady {
		t.Fatalf("an authorized order must be ready, got %v", status)
	}
	if _, err := g.Finalize(order.ID); err != nil {
		t.Fatalf("Finalize returned unexpected error: %v", err)
	}
	status, err = g.OrderStatus(order.ID)
	if err != nil {
		t.Fatalf("OrderStatus returned unexpected error: %v", err)
	}
	if status != task.OrderValid {
		t.Fatalf("a finalized order must be valid, got %v", status)
	}
}
