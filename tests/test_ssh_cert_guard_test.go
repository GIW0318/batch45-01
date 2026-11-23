package task

import (
	"net/netip"
	"testing"

	. "gsb/batch45-safety-c-10"
)

func TestAuthorizeDeniesUnmappedPrincipal(t *testing.T) {
	g := &SshCertGuard{}
	cert := Cert{Serial: 1, Principals: []string{"deploy"}, Type: CertTypeUser}
	got, err := g.Authorize(cert, "root")
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("certificate has no principal for root, want deny, got %v", got)
	}
}

func TestAuthorizeAllowsMappedPrincipal(t *testing.T) {
	g := &SshCertGuard{}
	cert := Cert{Serial: 1, Principals: []string{"deploy", "ops"}, Type: CertTypeUser}
	got, err := g.Authorize(cert, "ops")
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("certificate principal matches account, want allow, got %v", got)
	}
}

func TestEnforceForceCommandOverridesRequest(t *testing.T) {
	g := &SshCertGuard{}
	cert := Cert{Serial: 7, Type: CertTypeUser, ForceCommand: "/usr/local/bin/backup.sh"}
	got, err := g.EnforceForceCommand(cert, "/bin/bash")
	if err != nil {
		t.Fatalf("EnforceForceCommand: %v", err)
	}
	if got != "/usr/local/bin/backup.sh" {
		t.Fatalf("force-command must win over the requested command, got %q", got)
	}
}

func TestCheckSourceAddressDeniesOutsideRange(t *testing.T) {
	g := &SshCertGuard{}
	cert := Cert{Serial: 1, Type: CertTypeUser, SourceAddress: "10.10.0.0/16"}
	got, err := g.CheckSourceAddress(cert, netip.MustParseAddr("192.0.2.10"))
	if err != nil {
		t.Fatalf("CheckSourceAddress: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("source address outside the certificate restriction, want deny, got %v", got)
	}
}

func TestCheckSourceAddressAllowsInsideRange(t *testing.T) {
	g := &SshCertGuard{}
	cert := Cert{Serial: 1, Type: CertTypeUser, SourceAddress: "10.10.0.0/16"}
	got, err := g.CheckSourceAddress(cert, netip.MustParseAddr("10.10.3.7"))
	if err != nil {
		t.Fatalf("CheckSourceAddress: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("source address inside the certificate restriction, want allow, got %v", got)
	}
}

func TestCheckValidityDeniesExpiredCert(t *testing.T) {
	g := &SshCertGuard{}
	cert := Cert{Serial: 1, Type: CertTypeUser, ValidAfter: 100, ValidBefore: 200}
	got, err := g.CheckValidity(cert, 250)
	if err != nil {
		t.Fatalf("CheckValidity: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("certificate expired at 200, want deny at 250, got %v", got)
	}
}

func TestCheckValidityAllowsInsideWindow(t *testing.T) {
	g := &SshCertGuard{}
	cert := Cert{Serial: 1, Type: CertTypeUser, ValidAfter: 100, ValidBefore: 200}
	got, err := g.CheckValidity(cert, 150)
	if err != nil {
		t.Fatalf("CheckValidity: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("certificate valid at 150, want allow, got %v", got)
	}
}

func TestCheckKeyBindingDeniesMismatchedKey(t *testing.T) {
	g := &SshCertGuard{}
	cert := Cert{Serial: 1, Type: CertTypeUser, KeyMatches: false}
	got, err := g.CheckKeyBinding(cert)
	if err != nil {
		t.Fatalf("CheckKeyBinding: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("certificate not bound to the presented key, want deny, got %v", got)
	}
}

func TestCheckExtensionsDeniesUnknownExtension(t *testing.T) {
	g := &SshCertGuard{}
	cert := Cert{Serial: 1, Type: CertTypeUser, Extensions: []string{"permit-pty", "permit-X11-forwarding"}}
	got, err := g.CheckExtensions(cert, []string{"permit-pty"})
	if err != nil {
		t.Fatalf("CheckExtensions: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("permit-X11-forwarding is not allow listed, want deny, got %v", got)
	}
}

func TestCheckCertTypeDeniesHostCertForUserAuth(t *testing.T) {
	g := &SshCertGuard{}
	cert := Cert{Serial: 1, Type: CertTypeHost}
	got, err := g.CheckCertType(cert, CertTypeUser)
	if err != nil {
		t.Fatalf("CheckCertType: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("host certificate used for user authentication, want deny, got %v", got)
	}
}

func TestCheckRevocationDeniesRevokedSerial(t *testing.T) {
	g := &SshCertGuard{}
	crl := map[uint64]bool{4242: true}
	got, err := g.CheckRevocation(4242, crl)
	if err != nil {
		t.Fatalf("CheckRevocation: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("certificate serial 4242 is revoked, want deny, got %v", got)
	}
}

func TestAuditRecordsCertSerial(t *testing.T) {
	g := &SshCertGuard{}
	s := Session{Account: "ops", CertSerial: 4242, OpenedAt: 100}
	cert := Cert{Serial: 4242, Type: CertTypeUser, Principals: []string{"ops"}}
	rec, err := g.Audit(s, cert)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if rec.CertSerial != 4242 {
		t.Fatalf("audit record must carry the certificate serial, got %d", rec.CertSerial)
	}
}

func TestPropagateIdentityAppendsCertIdentity(t *testing.T) {
	g := &SshCertGuard{}
	cert := Cert{Serial: 11, Type: CertTypeUser, Principals: []string{"ops"}}
	chain, err := g.PropagateIdentity([]string{"jump-1"}, cert)
	if err != nil {
		t.Fatalf("PropagateIdentity: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("multi-hop chain must carry the certificate identity, got %v", chain)
	}
	if chain[1] != "ops" {
		t.Fatalf("appended identity must be the certificate principal, got %q", chain[1])
	}
}

func TestCheckSessionAliveDeniesAfterCertExpiry(t *testing.T) {
	g := &SshCertGuard{}
	s := Session{Account: "ops", CertSerial: 5, OpenedAt: 150}
	cert := Cert{Serial: 5, Type: CertTypeUser, ValidAfter: 100, ValidBefore: 200}
	got, err := g.CheckSessionAlive(s, cert, 260)
	if err != nil {
		t.Fatalf("CheckSessionAlive: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("certificate expired during the session, want deny, got %v", got)
	}
}

func TestCheckCAAcceptedDeniesRotatedOutCA(t *testing.T) {
	g := &SshCertGuard{}
	got, err := g.CheckCAAccepted("ca-old", []string{"ca-new"})
	if err != nil {
		t.Fatalf("CheckCAAccepted: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("certificate issued by the rotated-out CA, want deny, got %v", got)
	}
}
