package task

import (
	"sync"
	"testing"
	"time"

	. "gsb/batch45-safety-b-13"
)

func newXfrm(t *testing.T, now time.Time) *XfrmReconciler {
	t.Helper()
	x := NewXfrmReconciler(now)
	if x == nil {
		t.Fatalf("NewXfrmReconciler returned nil")
	}
	return x
}

func transportPolicy(name, src, dst string) Policy {
	return Policy{
		Name:      name,
		Src:       src,
		Dst:       dst,
		Proto:     "esp",
		Mode:      ModeTransport,
		Priority:  100,
		PFSGroup:  "ecp256",
		Algorithm: "aes-gcm-256",
	}
}

func TestAddTunnelIsAtomic(t *testing.T) {
	now := time.Unix(1700000000, 0)
	x := newXfrm(t, now)
	if _, err := x.AddTunnel(transportPolicy("t1", "10.0.0.0/8", "10.0.0.0/8")); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if _, err := x.AddTunnel(transportPolicy("t1", "192.168.0.0/16", "10.0.0.0/8")); err == nil {
		t.Fatalf("duplicate tunnel name was accepted")
	}
	if got := len(x.Policies()); got != 1 {
		t.Fatalf("policies = %d after failed duplicate, want 1", got)
	}
	if got := len(x.SAs()); got != 1 {
		t.Fatalf("SAs = %d after failed duplicate, want 1", got)
	}
	if x.Policies()[0].Src != "10.0.0.0/8" {
		t.Fatalf("policy overwritten by the failed add: %+v", x.Policies()[0])
	}
}

func TestCIDRIsNormalized(t *testing.T) {
	x := newXfrm(t, time.Unix(1700000000, 0))
	got, err := x.NormalizeCIDR("10.1.2.3/8")
	if err != nil {
		t.Fatalf("NormalizeCIDR v4: %v", err)
	}
	if got != "10.0.0.0/8" {
		t.Fatalf("normalized v4 = %q, want 10.0.0.0/8", got)
	}
	got, err = x.NormalizeCIDR("2001:db8::1/32")
	if err != nil {
		t.Fatalf("NormalizeCIDR v6: %v", err)
	}
	if got != "2001:db8::/32" {
		t.Fatalf("normalized v6 = %q, want 2001:db8::/32", got)
	}
	if _, err := x.AddTunnel(transportPolicy("t1", "10.1.2.3/8", "10.0.0.0/8")); err != nil {
		t.Fatalf("add with unnormalized cidr: %v", err)
	}
	if x.Policies()[0].Src != "10.0.0.0/8" {
		t.Fatalf("stored policy src = %q, not normalized", x.Policies()[0].Src)
	}
}

func TestMostSpecificPolicyWins(t *testing.T) {
	x := newXfrm(t, time.Unix(1700000000, 0))
	wide := transportPolicy("wide", "10.0.0.0/8", "0.0.0.0/0")
	narrow := transportPolicy("narrow", "10.1.0.0/16", "0.0.0.0/0")
	if _, err := x.AddTunnel(wide); err != nil {
		t.Fatalf("add wide: %v", err)
	}
	if _, err := x.AddTunnel(narrow); err != nil {
		t.Fatalf("add narrow: %v", err)
	}
	got, ok := x.Match("10.1.2.3", "8.8.8.8")
	if !ok {
		t.Fatalf("no policy matched")
	}
	if got.Name != "narrow" {
		t.Fatalf("matched %q, want narrow (longest prefix)", got.Name)
	}
}

func TestSPIConflictRejected(t *testing.T) {
	x := newXfrm(t, time.Unix(1700000000, 0))
	spi, err := x.AddTunnel(transportPolicy("t1", "10.0.0.0/8", "10.0.0.0/8"))
	if err != nil {
		t.Fatalf("add tunnel: %v", err)
	}
	conflict := SA{SPI: spi, PolicyName: "t2", Src: "10.0.0.0/8", Dst: "10.0.0.0/8"}
	if err := x.AddSA(conflict); err == nil {
		t.Fatalf("SPI conflict was accepted and overwrote the existing SA")
	}
	if got := len(x.SAs()); got != 1 {
		t.Fatalf("SAs = %d after conflict, want 1", got)
	}
	fresh := SA{SPI: spi + 1000, PolicyName: "t2", Src: "10.0.0.0/8", Dst: "10.0.0.0/8"}
	if err := x.AddSA(fresh); err != nil {
		t.Fatalf("add SA with free SPI: %v", err)
	}
	if got := len(x.SAs()); got != 2 {
		t.Fatalf("SAs = %d after adding a free SPI, want 2", got)
	}
}

func TestReplayWindowUpdatedOnExistingSA(t *testing.T) {
	x := newXfrm(t, time.Unix(1700000000, 0))
	spi, err := x.AddTunnel(transportPolicy("t1", "10.0.0.0/8", "10.0.0.0/8"))
	if err != nil {
		t.Fatalf("add tunnel: %v", err)
	}
	if err := x.SetReplayWindow(spi, 512); err != nil {
		t.Fatalf("set replay window: %v", err)
	}
	if got := x.SAs()[0].ReplayWindow; got != 512 {
		t.Fatalf("replay window = %d, want 512", got)
	}
	if err := x.SetReplayWindow(spi, 4096); err != nil {
		t.Fatalf("update replay window: %v", err)
	}
	if got := x.SAs()[0].ReplayWindow; got != 4096 {
		t.Fatalf("updated replay window = %d, want 4096", got)
	}
}

func TestDeletePolicyRemovesSA(t *testing.T) {
	x := newXfrm(t, time.Unix(1700000000, 0))
	if _, err := x.AddTunnel(transportPolicy("t1", "10.0.0.0/8", "10.0.0.0/8")); err != nil {
		t.Fatalf("add tunnel: %v", err)
	}
	if err := x.DeleteTunnel("t1"); err != nil {
		t.Fatalf("delete tunnel: %v", err)
	}
	if got := len(x.Policies()); got != 0 {
		t.Fatalf("policies = %d after delete, want 0", got)
	}
	if got := len(x.SAs()); got != 0 {
		t.Fatalf("SAs = %d after delete, want 0 (SA leaked)", got)
	}
	if err := x.DeleteTunnel("t1"); err == nil {
		t.Fatalf("deleting a missing tunnel reported success")
	}
}

func TestTunnelModeRequiresOuterAddress(t *testing.T) {
	x := newXfrm(t, time.Unix(1700000000, 0))
	bad := transportPolicy("t1", "10.0.0.0/8", "10.0.0.0/8")
	bad.Mode = ModeTunnel
	bad.TunnelSrc = "192.0.2.1"
	if _, err := x.AddTunnel(bad); err == nil {
		t.Fatalf("tunnel mode without outer destination was accepted")
	}
	good := bad
	good.TunnelDst = "192.0.2.2"
	if _, err := x.AddTunnel(good); err != nil {
		t.Fatalf("tunnel mode with both outer addresses: %v", err)
	}
	plain := transportPolicy("t2", "10.0.0.0/8", "10.0.0.0/8")
	if _, err := x.AddTunnel(plain); err != nil {
		t.Fatalf("transport mode without outer addresses: %v", err)
	}
}

func TestAEADCompliantCombination(t *testing.T) {
	x := newXfrm(t, time.Unix(1700000000, 0))
	if err := x.ValidateAlgorithm("aes-gcm-256", ""); err != nil {
		t.Fatalf("aes-gcm-256 alone rejected: %v", err)
	}
	if err := x.ValidateAlgorithm("aes-gcm-256", "hmac-sha256"); err == nil {
		t.Fatalf("AEAD with an extra auth algorithm was accepted")
	}
	if err := x.ValidateAlgorithm("aes-cbc-256", "hmac-sha256"); err != nil {
		t.Fatalf("aes-cbc-256 with hmac-sha256 rejected: %v", err)
	}
	if err := x.ValidateAlgorithm("aes-cbc-256", ""); err == nil {
		t.Fatalf("non-AEAD cipher without auth algorithm was accepted")
	}
	bad := transportPolicy("t1", "10.0.0.0/8", "10.0.0.0/8")
	bad.AuthAlg = "hmac-sha256"
	if _, err := x.AddTunnel(bad); err == nil {
		t.Fatalf("tunnel with a non-compliant AEAD combination was installed")
	}
}

func TestPFSGroupValidated(t *testing.T) {
	x := newXfrm(t, time.Unix(1700000000, 0))
	weak := transportPolicy("weak", "10.0.0.0/8", "10.0.0.0/8")
	weak.PFSGroup = "modp1024"
	if _, err := x.AddTunnel(weak); err == nil {
		t.Fatalf("weak PFS group modp1024 was accepted")
	}
	strong := transportPolicy("strong", "10.0.0.0/8", "10.0.0.0/8")
	strong.PFSGroup = "ecp384"
	if _, err := x.AddTunnel(strong); err != nil {
		t.Fatalf("PFS group ecp384 rejected: %v", err)
	}
	if got := x.Policies()[0].PFSGroup; got != "ecp384" {
		t.Fatalf("stored PFS group = %q, want ecp384", got)
	}
}

func TestExpiredSARekeyed(t *testing.T) {
	now := time.Unix(1700000000, 0)
	x := newXfrm(t, now)
	spi, err := x.AddTunnel(transportPolicy("t1", "10.0.0.0/8", "10.0.0.0/8"))
	if err != nil {
		t.Fatalf("add tunnel: %v", err)
	}
	newSPI, err := x.Rekey(spi, now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("rekey: %v", err)
	}
	if newSPI == spi {
		t.Fatalf("rekey reused SPI %d", spi)
	}
	if got := len(x.SAs()); got != 1 {
		t.Fatalf("SAs = %d after rekey, want 1", got)
	}
	sa := x.SAs()[0]
	if sa.SPI != newSPI || sa.State != SAStateActive {
		t.Fatalf("rekeyed SA = %+v", sa)
	}
	if !sa.ExpiresAt.After(now.Add(2 * time.Hour)) {
		t.Fatalf("rekeyed SA expires at %v, not extended", sa.ExpiresAt)
	}
}

func TestConcurrentAddSameTunnelSingleSA(t *testing.T) {
	x := newXfrm(t, time.Unix(1700000000, 0))
	var wg sync.WaitGroup
	errCh := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := x.AddTunnel(transportPolicy("t1", "10.0.0.0/8", "10.0.0.0/8"))
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent add: %v", err)
		}
	}
	if got := len(x.Policies()); got != 1 {
		t.Fatalf("policies = %d after concurrent adds, want 1", got)
	}
	if got := len(x.SAs()); got != 1 {
		t.Fatalf("SAs = %d after concurrent adds, want 1", got)
	}
}

func TestSnapshotNeverSeesHalfUpdate(t *testing.T) {
	x := newXfrm(t, time.Unix(1700000000, 0))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = x.AddTunnel(transportPolicy("t"+string(rune('a'+i)), "10.0.0.0/8", "10.0.0.0/8"))
		}(i)
	}
	for i := 0; i < 100; i++ {
		policies, sas := x.Snapshot()
		if len(policies) != len(sas) {
			t.Fatalf("snapshot saw %d policies and %d SAs", len(policies), len(sas))
		}
	}
	wg.Wait()
	policies, sas := x.Snapshot()
	if len(policies) != 20 || len(sas) != 20 {
		t.Fatalf("final snapshot %d policies/%d SAs, want 20/20", len(policies), len(sas))
	}
}

func TestReconcileAfterRestart(t *testing.T) {
	x := newXfrm(t, time.Unix(1700000000, 0))
	desired := []Policy{transportPolicy("t1", "10.0.0.0/8", "10.0.0.0/8")}
	desiredSA := []SA{{SPI: 77, PolicyName: "t1", Src: "10.0.0.0/8", Dst: "10.0.0.0/8", State: SAStateActive}}
	if err := x.Reconcile(desired, desiredSA); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := len(x.Policies()); got != 1 {
		t.Fatalf("policies = %d after reconcile, want 1", got)
	}
	if got := len(x.SAs()); got != 1 {
		t.Fatalf("SAs = %d after reconcile, want 1", got)
	}
	if x.SAs()[0].SPI != 77 {
		t.Fatalf("reconciled SPI = %d, want 77", x.SAs()[0].SPI)
	}
	if err := x.Reconcile(nil, nil); err != nil {
		t.Fatalf("reconcile empty: %v", err)
	}
	if got := len(x.Policies()) + len(x.SAs()); got != 0 {
		t.Fatalf("leftover state after empty reconcile: %d", got)
	}
}

func TestPoliciesOrderedByPriority(t *testing.T) {
	x := newXfrm(t, time.Unix(1700000000, 0))
	low := transportPolicy("low", "10.20.0.0/16", "10.0.0.0/8")
	low.Priority = 200
	high := transportPolicy("high", "10.10.0.0/16", "10.0.0.0/8")
	high.Priority = 100
	if _, err := x.AddTunnel(low); err != nil {
		t.Fatalf("add low priority: %v", err)
	}
	if _, err := x.AddTunnel(high); err != nil {
		t.Fatalf("add high priority: %v", err)
	}
	policies := x.Policies()
	if len(policies) != 2 {
		t.Fatalf("policies = %d, want 2", len(policies))
	}
	if policies[0].Priority != 100 || policies[1].Priority != 200 {
		t.Fatalf("policy order = %d,%d want 100,200", policies[0].Priority, policies[1].Priority)
	}
}

func TestExpiredListsOnlyLapsedSAs(t *testing.T) {
	now := time.Unix(1700000000, 0)
	x := newXfrm(t, now)
	if _, err := x.AddTunnel(transportPolicy("t1", "10.0.0.0/8", "10.0.0.0/8")); err != nil {
		t.Fatalf("add tunnel: %v", err)
	}
	if got := len(x.Expired(now.Add(30 * time.Minute))); got != 0 {
		t.Fatalf("expired = %d before lifetime end, want 0", got)
	}
	if got := len(x.Expired(now.Add(2 * time.Hour))); got != 1 {
		t.Fatalf("expired = %d after lifetime end, want 1", got)
	}
}
