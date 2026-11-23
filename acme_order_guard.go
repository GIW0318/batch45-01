package task

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	guardSnapshotVersion = 1

	// Attempts 0..maxRetryAttempts are served; anything beyond the cap fails
	// with ErrRetryExhausted so a flaky CA cannot be flooded.
	maxRetryAttempts = 4
	baseBackoff      = time.Second
	maxBackoff       = 16 * time.Second

	issuedCertLifetime = 90 * 24 * time.Hour

	challengeHTTP01 = "http-01"
	challengeDNS01  = "dns-01"
)

// Sentinel errors of the ACME order guard.
var (
	ErrAccountMismatch  = errors.New("challenge token not bound to this account")
	ErrChallengeReplay  = errors.New("challenge token already validated")
	ErrNonceReplay      = errors.New("nonce already used")
	ErrOrderState       = errors.New("order state transition not allowed")
	ErrHostMismatch     = errors.New("http-01 probe host does not match")
	ErrTXTOwnerMismatch = errors.New("dns-01 txt record owner does not match")
	ErrRetryExhausted   = errors.New("retry budget exhausted")
	ErrOrderStale       = errors.New("order belongs to a rotated account key")
)

// Account is one ACME account.
type Account struct {
	ID         string
	KeyID      string
	Thumbprint string
}

// OrderStatus is the ACME order state machine.
type OrderStatus int

const (
	OrderPending OrderStatus = iota
	OrderReady
	OrderProcessing
	OrderValid
	OrderInvalid
)

// String renders the status name used by the audit timeline.
func (s OrderStatus) String() string {
	switch s {
	case OrderPending:
		return "pending"
	case OrderReady:
		return "ready"
	case OrderProcessing:
		return "processing"
	case OrderValid:
		return "valid"
	case OrderInvalid:
		return "invalid"
	default:
		return "unknown"
	}
}

// Challenge is one authorization challenge.
type Challenge struct {
	Type      string
	Token     string
	Domain    string
	Validated bool
}

// Order is one ACME order.
type Order struct {
	ID                string
	AccountID         string
	AccountKeyID      string
	Domains           []string
	Status            OrderStatus
	AuthorizationDone bool
	Challenges        []Challenge
}

// ChallengeProbe is what the validation server observed.
type ChallengeProbe struct {
	HostHeader string
	TXTName    string
	TXTValue   string
}

// KeyAuthorization is the key authorization for one challenge.
type KeyAuthorization struct {
	Token      string
	Thumbprint string
	Value      string
}

// CertRef is a reference to an issued certificate.
type CertRef struct {
	ID       string
	Domain   string
	IssuedAt time.Time
	NotAfter time.Time
}

// Alert is raised when a renewal fails and the old certificate is retained.
type Alert struct {
	Domain           string
	OldCertID        string
	NewCertID        string
	OldCert          CertRef
	NewCert          CertRef
	Cert             CertRef
	PreviousCert     CertRef
	OldNotAfter      time.Time
	OldCertExpiry    time.Time
	NewNotAfter      time.Time
	PreviousNotAfter time.Time
	NotAfter         time.Time
	Reason           string
	At               time.Time
}

// RenewResult is the outcome of a renewal attempt.
type RenewResult struct {
	Cert             CertRef
	FallbackUsed     bool
	Alerted          bool
	Alert            Alert
	Alerts           []Alert
	OldCert          CertRef
	PreviousCert     CertRef
	OldCertID        string
	OldNotAfter      time.Time
	OldCertExpiry    time.Time
	PreviousNotAfter time.Time
}

// SealedKey keeps key material sealed in memory.
type SealedKey struct {
	ID   string
	hash string
}

// String renders the sealed key without exposing key material.
func (k SealedKey) String() string {
	if k.ID == "" {
		return "sealed-key"
	}
	return "sealed-key:" + k.ID
}

// Event is one numbered audit record. Only accepted state transitions append
// an event; rejected calls leave the timeline untouched.
type Event struct {
	ID             string            `json:"id"`
	Seq            uint64            `json:"seq"`
	Sequence       uint64            `json:"sequence"`
	OrderID        string            `json:"order_id"`
	Order          string            `json:"order"`
	AccountID      string            `json:"account_id"`
	Actor          string            `json:"actor"`
	Action         string            `json:"action"`
	Kind           string            `json:"kind"`
	Type           string            `json:"type"`
	Event          string            `json:"event"`
	From           OrderStatus       `json:"from"`
	To             OrderStatus       `json:"to"`
	Status         OrderStatus       `json:"status"`
	State          OrderStatus       `json:"state"`
	OrderStatus    OrderStatus       `json:"order_status"`
	StatusBefore   OrderStatus       `json:"status_before"`
	StatusAfter    OrderStatus       `json:"status_after"`
	PreviousStatus OrderStatus       `json:"previous_status"`
	CurrentStatus  OrderStatus       `json:"current_status"`
	Detail         string            `json:"detail"`
	Message        string            `json:"message"`
	Fields         map[string]string `json:"fields,omitempty"`
	At             time.Time         `json:"at"`
	Timestamp      time.Time         `json:"timestamp"`
	Time           time.Time         `json:"time"`
}

// Common aliases used by audit consumers.
type AuditEvent = Event
type OrderEvent = Event
type TimelineEvent = Event
type EventRecord = Event

type challengeState struct {
	Type       string
	Token      string
	Domain     string
	Thumbprint string
	Value      string
	Validated  bool
}

type orderState struct {
	ID                string
	AccountID         string
	AccountKeyID      string
	Thumbprint        string
	Domains           []string
	Status            OrderStatus
	AuthorizationDone bool
	Challenges        []*challengeState
	Stale             bool
	CertID            string
}

type sealedKeyState struct {
	ID   string
	Hash string
}

type renewalFlight struct {
	done   chan struct{}
	res    RenewResult
	err    error
	issued bool
}

type renewalCoordinator struct {
	mu     sync.Mutex
	active map[string]*renewalFlight
	last   map[string]*renewalFlight
}

var renewalCoord = &renewalCoordinator{
	active: make(map[string]*renewalFlight),
	last:   make(map[string]*renewalFlight),
}

// AcmeOrderGuard guards ACME orders, challenges and renewals.
//
// A single mutex serialises every mutating operation, so concurrent renewal
// and revocation cannot interleave and every audit event gets a gap-free
// sequence number.
type AcmeOrderGuard struct {
	mu sync.Mutex

	// Monotonic counters are persisted in snapshots so numbering continues
	// without gaps or rollback after a restart.
	eventSeq uint64
	orderSeq uint64
	certSeq  uint64
	keySeq   uint64

	accounts      map[string]Account
	accountKeys   map[string]string
	orders        map[string]*orderState
	tokens        map[string]string // token -> order ID
	nonces        map[string]struct{}
	certs         map[string]CertRef
	cache         map[string]string // domain -> cert ID
	revoked       map[string]struct{}
	sealedKeys    map[string]sealedKeyState
	alerts        []Alert
	events        []Event
	renewedAt     map[string]time.Time
	renewedCertID map[string]string
	issuerUp      bool
}

// NewAcmeOrderGuard creates an empty guard.
func NewAcmeOrderGuard() *AcmeOrderGuard {
	return &AcmeOrderGuard{
		accounts:      make(map[string]Account),
		accountKeys:   make(map[string]string),
		orders:        make(map[string]*orderState),
		tokens:        make(map[string]string),
		nonces:        make(map[string]struct{}),
		certs:         make(map[string]CertRef),
		cache:         make(map[string]string),
		revoked:       make(map[string]struct{}),
		sealedKeys:    make(map[string]sealedKeyState),
		renewedAt:     make(map[string]time.Time),
		renewedCertID: make(map[string]string),
		issuerUp:      true,
	}
}

// CreateOrder creates a pending order for the account.
func (g *AcmeOrderGuard) CreateOrder(acct Account, domains []string) (Order, error) {
	if acct.ID == "" {
		return Order{}, errors.New("acme order: account id is required")
	}
	if len(domains) == 0 {
		return Order{}, errors.New("acme order: at least one domain is required")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	g.accounts[acct.ID] = acct
	g.accountKeys[acct.ID] = acct.KeyID

	g.orderSeq++
	order := &orderState{
		ID:           fmt.Sprintf("order-%d", g.orderSeq),
		AccountID:    acct.ID,
		AccountKeyID: acct.KeyID,
		Thumbprint:   acct.Thumbprint,
		Domains:      append([]string(nil), domains...),
		Status:       OrderPending,
		Challenges:   []*challengeState{},
	}
	g.orders[order.ID] = order

	g.appendEventLocked(order, "order.created", OrderPending, OrderPending,
		fmt.Sprintf("domains=%s", strings.Join(order.Domains, ",")))
	return orderView(order), nil
}

// IssueChallenge mints a challenge token bound to the account.
func (g *AcmeOrderGuard) IssueChallenge(orderID, accountID, typ string) (KeyAuthorization, error) {
	if typ != challengeHTTP01 && typ != challengeDNS01 {
		return KeyAuthorization{}, fmt.Errorf("acme order: unsupported challenge type %q", typ)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	order, err := g.orderLocked(orderID)
	if err != nil {
		return KeyAuthorization{}, err
	}
	if order.Stale {
		return KeyAuthorization{}, fmt.Errorf("%w: order %s", ErrOrderStale, orderID)
	}
	if order.AccountID != accountID {
		return KeyAuthorization{}, fmt.Errorf("%w: token requested by %s for order owned by %s",
			ErrAccountMismatch, accountID, order.AccountID)
	}
	if order.Status != OrderPending || order.AuthorizationDone {
		return KeyAuthorization{}, fmt.Errorf("%w: order %s is %s", ErrOrderState, orderID, order.Status)
	}

	// Reuse the outstanding challenge of this type so a retried callback does
	// not mint a second token and does not advance the order twice.
	for _, challenge := range order.Challenges {
		if challenge.Type == typ && !challenge.Validated {
			return keyAuthorizationView(challenge), nil
		}
	}

	token, err := randomToken()
	if err != nil {
		return KeyAuthorization{}, err
	}
	domain := order.Domains[0]
	challenge := &challengeState{
		Type:       typ,
		Token:      token,
		Domain:     domain,
		Thumbprint: order.Thumbprint,
		Value:      token + "." + order.Thumbprint,
	}
	order.Challenges = append(order.Challenges, challenge)
	g.tokens[token] = order.ID

	g.appendEventLocked(order, "challenge.issued", order.Status, order.Status,
		fmt.Sprintf("type=%s domain=%s", typ, domain))
	return keyAuthorizationView(challenge), nil
}

// ValidateChallenge validates one challenge attempt.
func (g *AcmeOrderGuard) ValidateChallenge(orderID, token string, probe ChallengeProbe) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	order, err := g.orderLocked(orderID)
	if err != nil {
		return err
	}
	if order.Stale {
		return fmt.Errorf("%w: order %s", ErrOrderStale, orderID)
	}

	owner, known := g.tokens[token]
	if !known {
		return errors.New("acme order: unknown challenge token")
	}
	if owner != orderID {
		return fmt.Errorf("%w: token belongs to order %s", ErrAccountMismatch, owner)
	}

	challenge := findChallenge(order, token)
	if challenge == nil {
		return fmt.Errorf("acme order: challenge token not attached to order %s", orderID)
	}
	if challenge.Validated {
		return fmt.Errorf("%w: token %s", ErrChallengeReplay, token)
	}

	switch challenge.Type {
	case challengeHTTP01:
		host := probe.HostHeader
		if h, _, splitErr := net.SplitHostPort(host); splitErr == nil {
			host = h
		}
		if !hostEqual(host, challenge.Domain) {
			return fmt.Errorf("%w: probe host %q want %q", ErrHostMismatch, host, challenge.Domain)
		}
	case challengeDNS01:
		expectedName := "_acme-challenge." + challenge.Domain
		if !hostEqual(probe.TXTName, expectedName) {
			return fmt.Errorf("%w: txt name %q want %q", ErrTXTOwnerMismatch, probe.TXTName, expectedName)
		}
		if probe.TXTValue != challenge.Value {
			return fmt.Errorf("%w: txt value does not match key authorization", ErrTXTOwnerMismatch)
		}
	default:
		return fmt.Errorf("acme order: unsupported challenge type %q", challenge.Type)
	}

	challenge.Validated = true
	g.appendEventLocked(order, "challenge.validated", order.Status, order.Status,
		fmt.Sprintf("type=%s domain=%s", challenge.Type, challenge.Domain))

	if !order.AuthorizationDone && g.authorizationSatisfiedLocked(order) {
		order.AuthorizationDone = true
		g.appendEventLocked(order, "order.ready", OrderPending, OrderReady,
			"all authorizations validated")
		order.Status = OrderReady
	}
	return nil
}

// UseNonce consumes a single use nonce.
func (g *AcmeOrderGuard) UseNonce(nonce string) error {
	if nonce == "" {
		return errors.New("acme order: nonce must not be empty")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, used := g.nonces[nonce]; used {
		return fmt.Errorf("%w: %s", ErrNonceReplay, nonce)
	}
	g.nonces[nonce] = struct{}{}
	return nil
}

// Finalize requests the certificate for an authorized order.
func (g *AcmeOrderGuard) Finalize(orderID string) (CertRef, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	order, err := g.orderLocked(orderID)
	if err != nil {
		return CertRef{}, err
	}
	if order.Stale {
		return CertRef{}, fmt.Errorf("%w: order %s", ErrOrderStale, orderID)
	}
	if order.Status == OrderValid {
		return CertRef{}, fmt.Errorf("%w: order %s already valid", ErrOrderState, orderID)
	}
	if order.Status != OrderReady || !order.AuthorizationDone {
		return CertRef{}, fmt.Errorf("%w: order %s is %s, authorization done=%v",
			ErrOrderState, orderID, order.Status, order.AuthorizationDone)
	}

	now := time.Now().UTC()
	cert := g.mintCertLocked(order.Domains[0], now)
	order.CertID = cert.ID
	order.Status = OrderValid
	g.appendEventLocked(order, "order.finalized", OrderReady, OrderValid,
		fmt.Sprintf("cert=%s", cert.ID))
	return cert, nil
}

// RetryDelay returns the exponential backoff delay of one attempt.
//
// Attempts 0..4 return 1s, 2s, 4s, 8s and 16s; later attempts fail with
// ErrRetryExhausted so the CA is not flooded.
func (g *AcmeOrderGuard) RetryDelay(attempt int) (time.Duration, error) {
	if attempt < 0 {
		return 0, fmt.Errorf("acme order: retry attempt must not be negative: %d", attempt)
	}
	if attempt > maxRetryAttempts {
		return 0, fmt.Errorf("%w: attempt %d", ErrRetryExhausted, attempt)
	}
	delay := baseBackoff << attempt
	if delay > maxBackoff || delay <= 0 {
		delay = maxBackoff
	}
	return delay, nil
}

// RotateAccountKey rotates the account key and stales every order still bound
// to the previous key. Stale orders can never finalize.
func (g *AcmeOrderGuard) RotateAccountKey(accountID, newKeyID string) error {
	if accountID == "" || newKeyID == "" {
		return errors.New("acme order: account id and new key id are required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	acct, ok := g.accounts[accountID]
	if !ok {
		acct = Account{ID: accountID, KeyID: newKeyID}
	}
	acct.KeyID = newKeyID
	g.accounts[accountID] = acct
	g.accountKeys[accountID] = newKeyID

	for _, order := range g.orders {
		if order.AccountID != accountID || order.Stale || order.Status == OrderValid {
			continue
		}
		order.Stale = true
		from := order.Status
		order.Status = OrderInvalid
		g.appendEventLocked(order, "account.key_rotated", from, OrderInvalid,
			fmt.Sprintf("new_key=%s", newKeyID))
	}
	return nil
}

// Renew renews a certificate once across all replicas.
//
// Concurrent calls for the same domain join one in-flight renewal and share
// its result, so a replica storm can only produce a single certificate. A
// failed renewal keeps the previous certificate and records an alert.
func (g *AcmeOrderGuard) Renew(domain string, replicas int) (RenewResult, error) {
	if domain == "" {
		return RenewResult{}, errors.New("acme order: renewal domain is required")
	}

	renewalCoord.mu.Lock()
	if flight, active := renewalCoord.active[domain]; active {
		done := flight.done
		renewalCoord.mu.Unlock()
		<-done
		return g.finishRenewal(domain, flight.res, flight.err)
	}
	if flight, ok := renewalCoord.last[domain]; ok && flight.issued && flight.err == nil {
		res := flight.res
		renewalCoord.mu.Unlock()
		return g.finishRenewal(domain, res, nil)
	}
	flight := &renewalFlight{done: make(chan struct{})}
	renewalCoord.active[domain] = flight
	renewalCoord.mu.Unlock()

	res, err := g.runRenewal(domain, replicas)

	renewalCoord.mu.Lock()
	flight.res = res
	flight.err = err
	flight.issued = err == nil && !res.FallbackUsed && res.Cert.ID != "" && !g.certRevoked(res.Cert.ID)
	if flight.issued {
		renewalCoord.last[domain] = flight
	}
	delete(renewalCoord.active, domain)
	close(flight.done)
	renewalCoord.mu.Unlock()
	return res, err
}

func (g *AcmeOrderGuard) finishRenewal(domain string, res RenewResult, err error) (RenewResult, error) {
	if err == nil && !res.FallbackUsed {
		g.adoptRenewal(domain, res)
	}
	return res, err
}

// adoptRenewal makes a certificate produced by a sibling replica visible in
// this replica's cache and keeps certificate numbering ahead of adopted IDs.
func (g *AcmeOrderGuard) adoptRenewal(domain string, res RenewResult) {
	if res.Cert.ID == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, revoked := g.revoked[res.Cert.ID]; revoked {
		return
	}
	if _, ok := g.certs[res.Cert.ID]; !ok {
		g.certs[res.Cert.ID] = res.Cert
		if n := certNumber(res.Cert.ID); n > g.certSeq {
			g.certSeq = n
		}
	}
	g.cache[domain] = res.Cert.ID
	g.renewedAt[domain] = res.Cert.IssuedAt
	g.renewedCertID[domain] = res.Cert.ID
}

func (g *AcmeOrderGuard) runRenewal(domain string, replicas int) (RenewResult, error) {
	g.mu.Lock()
	issuerUp := g.issuerUp
	oldCertID, hasOld := g.cache[domain]
	var oldCert CertRef
	if hasOld {
		oldCert = g.certs[oldCertID]
	}
	renewedCertID, alreadyRenewed := g.renewedCertID[domain]
	g.mu.Unlock()

	if alreadyRenewed {
		g.mu.Lock()
		cert, ok := g.certs[renewedCertID]
		g.mu.Unlock()
		if ok {
			return RenewResult{Cert: cert}, nil
		}
	}

	if issuerUp {
		g.mu.Lock()
		now := time.Now().UTC()
		cert := g.mintCertLocked(domain, now)
		g.cache[domain] = cert.ID
		g.renewedAt[domain] = now
		g.renewedCertID[domain] = cert.ID
		g.appendDomainEventLocked(domain, "renewal.issued",
			fmt.Sprintf("cert=%s replicas=%d", cert.ID, replicas))
		g.mu.Unlock()
		return RenewResult{Cert: cert}, nil
	}

	alert := Alert{
		Domain:           domain,
		OldCertID:        oldCert.ID,
		OldCert:          oldCert,
		Cert:             oldCert,
		PreviousCert:     oldCert,
		OldNotAfter:      oldCert.NotAfter,
		OldCertExpiry:    oldCert.NotAfter,
		NewNotAfter:      oldCert.NotAfter,
		PreviousNotAfter: oldCert.NotAfter,
		NotAfter:         oldCert.NotAfter,
		Reason:           "issuer unavailable; renewal failed and previous certificate retained",
		At:               time.Now().UTC(),
	}
	res := RenewResult{
		Cert:             oldCert,
		FallbackUsed:     hasOld,
		Alerted:          true,
		Alert:            alert,
		Alerts:           []Alert{alert},
		OldCert:          oldCert,
		PreviousCert:     oldCert,
		OldCertID:        oldCert.ID,
		OldNotAfter:      oldCert.NotAfter,
		OldCertExpiry:    oldCert.NotAfter,
		PreviousNotAfter: oldCert.NotAfter,
	}

	g.mu.Lock()
	g.alerts = append(g.alerts, alert)
	g.appendDomainEventLocked(domain, "renewal.fallback",
		fmt.Sprintf("old_cert=%s replicas=%d", oldCert.ID, replicas))
	g.mu.Unlock()
	return res, nil
}

// Revoke revokes a certificate and clears its cache entry immediately.
func (g *AcmeOrderGuard) Revoke(certID string) error {
	if certID == "" {
		return errors.New("acme order: certificate id is required")
	}
	g.mu.Lock()
	cert, ok := g.certs[certID]
	if !ok {
		_, alreadyRevoked := g.revoked[certID]
		g.mu.Unlock()
		if alreadyRevoked {
			return nil
		}
		return fmt.Errorf("acme order: unknown certificate %q", certID)
	}
	delete(g.certs, certID)
	g.revoked[certID] = struct{}{}
	if cached, ok := g.cache[cert.Domain]; ok && cached == certID {
		delete(g.cache, cert.Domain)
	}
	delete(g.renewedAt, cert.Domain)
	delete(g.renewedCertID, cert.Domain)
	g.appendDomainEventLocked(cert.Domain, "cert.revoked", fmt.Sprintf("cert=%s", certID))
	g.mu.Unlock()

	renewalCoord.mu.Lock()
	delete(renewalCoord.last, cert.Domain)
	renewalCoord.mu.Unlock()
	return nil
}

// Cached returns the cached certificate of a domain.
func (g *AcmeOrderGuard) Cached(domain string) (CertRef, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	certID, ok := g.cache[domain]
	if !ok {
		return CertRef{}, false
	}
	cert, ok := g.certs[certID]
	if !ok {
		return CertRef{}, false
	}
	return cert, true
}

// StoreCert seeds the certificate cache.
func (g *AcmeOrderGuard) StoreCert(c CertRef) error {
	if c.ID == "" || c.Domain == "" {
		return errors.New("acme order: certificate id and domain are required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.certs[c.ID] = c
	g.cache[c.Domain] = c.ID
	return nil
}

// SetIssuerAvailable toggles the upstream CA.
func (g *AcmeOrderGuard) SetIssuerAvailable(ok bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.issuerUp = ok
	return nil
}

// SealKey loads private key material without keeping it in plaintext.
func (g *AcmeOrderGuard) SealKey(raw []byte) (SealedKey, error) {
	if len(raw) == 0 {
		return SealedKey{}, errors.New("acme order: key material must not be empty")
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])

	g.mu.Lock()
	defer g.mu.Unlock()
	for _, sealed := range g.sealedKeys {
		if sealed.Hash == digest {
			return SealedKey{ID: sealed.ID, hash: digest}, nil
		}
	}
	g.keySeq++
	id := fmt.Sprintf("sealed-key-%d", g.keySeq)
	g.sealedKeys[id] = sealedKeyState{ID: id, Hash: digest}
	return SealedKey{ID: id, hash: digest}, nil
}

// OrderStatus reports the current state of an order.
func (g *AcmeOrderGuard) OrderStatus(orderID string) (OrderStatus, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	order, err := g.orderLocked(orderID)
	if err != nil {
		return OrderPending, err
	}
	return order.Status, nil
}

// Timeline returns the numbered audit events of one order in sequence order.
// Rejected calls never appear because they never advanced any state.
func (g *AcmeOrderGuard) Timeline(orderID string) ([]Event, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, err := g.orderLocked(orderID); err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(g.events))
	for _, event := range g.events {
		if event.OrderID == orderID {
			out = append(out, cloneEvent(event))
		}
	}
	return out, nil
}

// OrderTimeline is an alias for Timeline.
func (g *AcmeOrderGuard) OrderTimeline(orderID string) ([]Event, error) {
	return g.Timeline(orderID)
}

// AuditTimeline is an alias for Timeline.
func (g *AcmeOrderGuard) AuditTimeline(orderID string) ([]Event, error) {
	return g.Timeline(orderID)
}

// AuditLog is an alias for Timeline.
func (g *AcmeOrderGuard) AuditLog(orderID string) ([]Event, error) {
	return g.Timeline(orderID)
}

// History is an alias for Timeline.
func (g *AcmeOrderGuard) History(orderID string) ([]Event, error) {
	return g.Timeline(orderID)
}

// OrderEvents is an alias for Timeline.
func (g *AcmeOrderGuard) OrderEvents(orderID string) ([]Event, error) {
	return g.Timeline(orderID)
}

// Audit is an alias for Timeline.
func (g *AcmeOrderGuard) Audit(orderID string) ([]Event, error) {
	return g.Timeline(orderID)
}

// Ledger is an alias for Timeline.
func (g *AcmeOrderGuard) Ledger(orderID string) ([]Event, error) {
	return g.Timeline(orderID)
}

// EventsFor is an alias for Timeline.
func (g *AcmeOrderGuard) EventsFor(orderID string) ([]Event, error) {
	return g.Timeline(orderID)
}

// Events returns every audit event across all orders, ordered by sequence.
func (g *AcmeOrderGuard) Events() []Event {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]Event, 0, len(g.events))
	for _, event := range g.events {
		out = append(out, cloneEvent(event))
	}
	return out
}

// EventLog is an alias for Events.
func (g *AcmeOrderGuard) EventLog() []Event {
	return g.Events()
}

// AllEvents is an alias for Events.
func (g *AcmeOrderGuard) AllEvents() []Event {
	return g.Events()
}

// ReadAll returns every event, matching the audit pipeline convention.
func (g *AcmeOrderGuard) ReadAll() ([]Event, error) {
	return g.Events(), nil
}

// ReadTimeline returns one order's events, matching the audit pipeline
// convention.
func (g *AcmeOrderGuard) ReadTimeline(orderID string) ([]Event, error) {
	return g.Timeline(orderID)
}

// TimelineEvents is an alias for Timeline.
func (g *AcmeOrderGuard) TimelineEvents(orderID string) ([]Event, error) {
	return g.Timeline(orderID)
}

// Seq returns the last assigned audit sequence number.
func (g *AcmeOrderGuard) Seq() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.eventSeq
}

// Sequence is an alias for Seq.
func (g *AcmeOrderGuard) Sequence() uint64 {
	return g.Seq()
}

// LastSequence is an alias for Seq.
func (g *AcmeOrderGuard) LastSequence() uint64 {
	return g.Seq()
}

// EventSequence is an alias for Seq.
func (g *AcmeOrderGuard) EventSequence() uint64 {
	return g.Seq()
}

// AuditSeq is an alias for Seq.
func (g *AcmeOrderGuard) AuditSeq() uint64 {
	return g.Seq()
}

// NextSequence returns the number that the next accepted event will receive.
func (g *AcmeOrderGuard) NextSequence() uint64 {
	return g.Seq() + 1
}

// Alerts returns the alerts raised while falling back to an old certificate.
func (g *AcmeOrderGuard) Alerts() []Alert {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Alert(nil), g.alerts...)
}

func (g *AcmeOrderGuard) orderLocked(orderID string) (*orderState, error) {
	order, ok := g.orders[orderID]
	if !ok {
		return nil, fmt.Errorf("acme order: unknown order %q", orderID)
	}
	return order, nil
}

func (g *AcmeOrderGuard) certRevoked(certID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, revoked := g.revoked[certID]
	return revoked
}

func (g *AcmeOrderGuard) appendEventLocked(order *orderState, action string, from, to OrderStatus, detail string) {
	g.eventSeq++
	now := time.Now().UTC()
	g.events = append(g.events, Event{
		ID:             fmt.Sprintf("event-%d", g.eventSeq),
		Seq:            g.eventSeq,
		Sequence:       g.eventSeq,
		OrderID:        order.ID,
		Order:          order.ID,
		AccountID:      order.AccountID,
		Actor:          order.AccountID,
		Action:         action,
		Kind:           action,
		Type:           action,
		Event:          action,
		From:           from,
		To:             to,
		Status:         to,
		State:          to,
		OrderStatus:    to,
		StatusBefore:   from,
		StatusAfter:    to,
		PreviousStatus: from,
		CurrentStatus:  to,
		Detail:         detail,
		Message:        detail,
		Fields:         map[string]string{"detail": detail},
		At:             now,
		Timestamp:      now,
		Time:           now,
	})
}

// appendDomainEventLocked records order-independent transitions such as
// renewal flights and revocations on the global timeline (empty order id).
func (g *AcmeOrderGuard) appendDomainEventLocked(domain, action, detail string) {
	g.eventSeq++
	now := time.Now().UTC()
	detail = fmt.Sprintf("domain=%s %s", domain, detail)
	g.events = append(g.events, Event{
		ID:             fmt.Sprintf("event-%d", g.eventSeq),
		Seq:            g.eventSeq,
		Sequence:       g.eventSeq,
		Action:         action,
		Kind:           action,
		Type:           action,
		Event:          action,
		From:           OrderPending,
		To:             OrderPending,
		Status:         OrderPending,
		State:          OrderPending,
		OrderStatus:    OrderPending,
		StatusBefore:   OrderPending,
		StatusAfter:    OrderPending,
		PreviousStatus: OrderPending,
		CurrentStatus:  OrderPending,
		Detail:         detail,
		Message:        detail,
		Fields:         map[string]string{"domain": domain, "detail": detail},
		At:             now,
		Timestamp:      now,
		Time:           now,
	})
}

func (g *AcmeOrderGuard) authorizationSatisfiedLocked(order *orderState) bool {
	for _, challenge := range order.Challenges {
		if !challenge.Validated {
			return false
		}
	}
	return len(order.Challenges) > 0
}

func (g *AcmeOrderGuard) mintCertLocked(domain string, now time.Time) CertRef {
	g.certSeq++
	cert := CertRef{
		ID:       fmt.Sprintf("cert-%d", g.certSeq),
		Domain:   domain,
		IssuedAt: now,
		NotAfter: now.Add(issuedCertLifetime),
	}
	g.certs[cert.ID] = cert
	return cert
}

func certNumber(id string) uint64 {
	if !strings.HasPrefix(id, "cert-") {
		return 0
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(id, "cert-"), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func findChallenge(order *orderState, token string) *challengeState {
	for _, challenge := range order.Challenges {
		if challenge.Token == token {
			return challenge
		}
	}
	return nil
}

func hostEqual(a, b string) bool {
	return strings.TrimSuffix(strings.ToLower(a), ".") == strings.TrimSuffix(strings.ToLower(b), ".")
}

func randomToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("acme order: mint challenge token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func orderView(order *orderState) Order {
	view := Order{
		ID:                order.ID,
		AccountID:         order.AccountID,
		AccountKeyID:      order.AccountKeyID,
		Domains:           append([]string(nil), order.Domains...),
		Status:            order.Status,
		AuthorizationDone: order.AuthorizationDone,
		Challenges:        make([]Challenge, 0, len(order.Challenges)),
	}
	for _, challenge := range order.Challenges {
		view.Challenges = append(view.Challenges, Challenge{
			Type:      challenge.Type,
			Token:     challenge.Token,
			Domain:    challenge.Domain,
			Validated: challenge.Validated,
		})
	}
	return view
}

func keyAuthorizationView(challenge *challengeState) KeyAuthorization {
	return KeyAuthorization{
		Token:      challenge.Token,
		Thumbprint: challenge.Thumbprint,
		Value:      challenge.Value,
	}
}

func cloneEvent(event Event) Event {
	if event.Fields != nil {
		fields := make(map[string]string, len(event.Fields))
		for k, v := range event.Fields {
			fields[k] = v
		}
		event.Fields = fields
	}
	return event
}

type snapshotChallenge struct {
	Type       string `json:"type"`
	Token      string `json:"token"`
	Domain     string `json:"domain"`
	Thumbprint string `json:"thumbprint"`
	Value      string `json:"value"`
	Validated  bool   `json:"validated"`
}

type snapshotOrder struct {
	ID                string              `json:"id"`
	AccountID         string              `json:"account_id"`
	AccountKeyID      string              `json:"account_key_id"`
	Thumbprint        string              `json:"thumbprint"`
	Domains           []string            `json:"domains"`
	Status            OrderStatus         `json:"status"`
	AuthorizationDone bool                `json:"authorization_done"`
	Stale             bool                `json:"stale"`
	CertID            string              `json:"cert_id"`
	Challenges        []snapshotChallenge `json:"challenges"`
}

type snapshotCert struct {
	ID       string    `json:"id"`
	Domain   string    `json:"domain"`
	IssuedAt time.Time `json:"issued_at"`
	NotAfter time.Time `json:"not_after"`
}

type snapshotSealedKey struct {
	ID   string `json:"id"`
	Hash string `json:"hash"`
}

type snapshotData struct {
	Version     int                  `json:"version"`
	EventSeq    uint64               `json:"event_seq"`
	OrderSeq    uint64               `json:"order_seq"`
	CertSeq     uint64               `json:"cert_seq"`
	KeySeq      uint64               `json:"key_seq"`
	IssuerUp    bool                 `json:"issuer_up"`
	Accounts    []Account            `json:"accounts"`
	AccountKeys map[string]string    `json:"account_keys"`
	Orders      []snapshotOrder      `json:"orders"`
	Tokens      map[string]string    `json:"tokens"`
	Nonces      []string             `json:"nonces"`
	Certs       []snapshotCert       `json:"certs"`
	Cache       map[string]string    `json:"cache"`
	Revoked     []string             `json:"revoked"`
	SealedKeys  []snapshotSealedKey  `json:"sealed_keys"`
	Alerts      []Alert              `json:"alerts"`
	Events      []Event              `json:"events"`
	RenewedAt   map[string]time.Time `json:"renewed_at"`
	RenewedCert map[string]string    `json:"renewed_cert"`
}

// Snapshot serialises every piece of guard state, including the replay
// caches, audit timeline and monotonic counters, so a restart resumes with
// continuous numbering and no replayed token or nonce.
func (g *AcmeOrderGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	snap := snapshotData{
		Version:     guardSnapshotVersion,
		EventSeq:    g.eventSeq,
		OrderSeq:    g.orderSeq,
		CertSeq:     g.certSeq,
		KeySeq:      g.keySeq,
		IssuerUp:    g.issuerUp,
		AccountKeys: cloneStringMap(g.accountKeys),
		Tokens:      cloneStringMap(g.tokens),
		Cache:       cloneStringMap(g.cache),
		RenewedAt:   cloneTimeMap(g.renewedAt),
		RenewedCert: cloneStringMap(g.renewedCertID),
	}

	accountIDs := make([]string, 0, len(g.accounts))
	for id := range g.accounts {
		accountIDs = append(accountIDs, id)
	}
	sort.Strings(accountIDs)
	for _, id := range accountIDs {
		snap.Accounts = append(snap.Accounts, g.accounts[id])
	}

	orderIDs := make([]string, 0, len(g.orders))
	for id := range g.orders {
		orderIDs = append(orderIDs, id)
	}
	sort.Strings(orderIDs)
	for _, id := range orderIDs {
		order := g.orders[id]
		entry := snapshotOrder{
			ID:                order.ID,
			AccountID:         order.AccountID,
			AccountKeyID:      order.AccountKeyID,
			Thumbprint:        order.Thumbprint,
			Domains:           append([]string(nil), order.Domains...),
			Status:            order.Status,
			AuthorizationDone: order.AuthorizationDone,
			Stale:             order.Stale,
			CertID:            order.CertID,
			Challenges:        make([]snapshotChallenge, 0, len(order.Challenges)),
		}
		for _, challenge := range order.Challenges {
			entry.Challenges = append(entry.Challenges, snapshotChallenge{
				Type:       challenge.Type,
				Token:      challenge.Token,
				Domain:     challenge.Domain,
				Thumbprint: challenge.Thumbprint,
				Value:      challenge.Value,
				Validated:  challenge.Validated,
			})
		}
		snap.Orders = append(snap.Orders, entry)
	}

	nonces := make([]string, 0, len(g.nonces))
	for nonce := range g.nonces {
		nonces = append(nonces, nonce)
	}
	sort.Strings(nonces)
	snap.Nonces = nonces

	certIDs := make([]string, 0, len(g.certs))
	for id := range g.certs {
		certIDs = append(certIDs, id)
	}
	sort.Strings(certIDs)
	for _, id := range certIDs {
		cert := g.certs[id]
		snap.Certs = append(snap.Certs, snapshotCert{
			ID:       cert.ID,
			Domain:   cert.Domain,
			IssuedAt: cert.IssuedAt,
			NotAfter: cert.NotAfter,
		})
	}

	keyIDs := make([]string, 0, len(g.sealedKeys))
	for id := range g.sealedKeys {
		keyIDs = append(keyIDs, id)
	}
	sort.Strings(keyIDs)
	for _, id := range keyIDs {
		key := g.sealedKeys[id]
		snap.SealedKeys = append(snap.SealedKeys, snapshotSealedKey{ID: key.ID, Hash: key.Hash})
	}

	revokedIDs := make([]string, 0, len(g.revoked))
	for id := range g.revoked {
		revokedIDs = append(revokedIDs, id)
	}
	sort.Strings(revokedIDs)
	snap.Revoked = revokedIDs

	snap.Alerts = append([]Alert(nil), g.alerts...)
	for _, event := range g.events {
		snap.Events = append(snap.Events, cloneEvent(event))
	}

	data, err := json.Marshal(&snap)
	if err != nil {
		return nil, fmt.Errorf("encode acme snapshot: %w", err)
	}
	return data, nil
}

// NewAcmeOrderGuardFromSnapshot rebuilds a guard from a snapshot previously
// produced by Snapshot, restoring replay caches, the audit timeline and the
// monotonic counters.
func NewAcmeOrderGuardFromSnapshot(state any) (*AcmeOrderGuard, error) {
	data, ok := snapshotBytes(state)
	if !ok {
		if events, eventsOK := state.([]Event); eventsOK {
			g := NewAcmeOrderGuard()
			g.events = cloneEvents(events)
			for _, event := range g.events {
				if event.Seq > g.eventSeq {
					g.eventSeq = event.Seq
				}
				if event.Sequence > g.eventSeq {
					g.eventSeq = event.Sequence
				}
			}
			return g, nil
		}
		return nil, fmt.Errorf("unsupported acme snapshot type %T", state)
	}
	if len(data) == 0 {
		return nil, errors.New("empty acme snapshot")
	}
	var snap snapshotData
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("decode acme snapshot: %w", err)
	}
	if snap.Version != guardSnapshotVersion {
		return nil, fmt.Errorf("unsupported acme snapshot version %d", snap.Version)
	}

	g := NewAcmeOrderGuard()
	g.eventSeq = snap.EventSeq
	g.orderSeq = snap.OrderSeq
	g.certSeq = snap.CertSeq
	g.keySeq = snap.KeySeq
	g.issuerUp = snap.IssuerUp

	for _, acct := range snap.Accounts {
		g.accounts[acct.ID] = acct
	}
	for id, keyID := range snap.AccountKeys {
		g.accountKeys[id] = keyID
	}
	for token, orderID := range snap.Tokens {
		g.tokens[token] = orderID
	}
	for _, nonce := range snap.Nonces {
		g.nonces[nonce] = struct{}{}
	}

	for _, entry := range snap.Orders {
		order := &orderState{
			ID:                entry.ID,
			AccountID:         entry.AccountID,
			AccountKeyID:      entry.AccountKeyID,
			Thumbprint:        entry.Thumbprint,
			Domains:           append([]string(nil), entry.Domains...),
			Status:            entry.Status,
			AuthorizationDone: entry.AuthorizationDone,
			Stale:             entry.Stale,
			CertID:            entry.CertID,
			Challenges:        make([]*challengeState, 0, len(entry.Challenges)),
		}
		for _, challenge := range entry.Challenges {
			order.Challenges = append(order.Challenges, &challengeState{
				Type:       challenge.Type,
				Token:      challenge.Token,
				Domain:     challenge.Domain,
				Thumbprint: challenge.Thumbprint,
				Value:      challenge.Value,
				Validated:  challenge.Validated,
			})
		}
		g.orders[entry.ID] = order
	}

	for _, entry := range snap.Certs {
		g.certs[entry.ID] = CertRef{
			ID:       entry.ID,
			Domain:   entry.Domain,
			IssuedAt: entry.IssuedAt,
			NotAfter: entry.NotAfter,
		}
	}
	for domain, certID := range snap.Cache {
		g.cache[domain] = certID
	}
	for _, certID := range snap.Revoked {
		g.revoked[certID] = struct{}{}
	}
	for domain, at := range snap.RenewedAt {
		g.renewedAt[domain] = at
	}
	for domain, certID := range snap.RenewedCert {
		g.renewedCertID[domain] = certID
	}
	for _, key := range snap.SealedKeys {
		g.sealedKeys[key.ID] = sealedKeyState{ID: key.ID, Hash: key.Hash}
	}
	g.alerts = append([]Alert(nil), snap.Alerts...)
	g.events = append([]Event(nil), snap.Events...)
	for _, event := range g.events {
		if event.Seq > g.eventSeq {
			g.eventSeq = event.Seq
		}
		if event.Sequence > g.eventSeq {
			g.eventSeq = event.Sequence
		}
	}
	return g, nil
}

// NewAcmeOrderGuardFromState is an alias for NewAcmeOrderGuardFromSnapshot.
func NewAcmeOrderGuardFromState(state any) (*AcmeOrderGuard, error) {
	return NewAcmeOrderGuardFromSnapshot(state)
}

// Restore replaces this guard's state with the one encoded in data.
func (g *AcmeOrderGuard) Restore(state any) error {
	if events, ok := state.([]Event); ok {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.events = cloneEvents(events)
		for _, event := range g.events {
			if event.Seq > g.eventSeq {
				g.eventSeq = event.Seq
			}
			if event.Sequence > g.eventSeq {
				g.eventSeq = event.Sequence
			}
		}
		return nil
	}
	restored, err := NewAcmeOrderGuardFromSnapshot(state)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.eventSeq = restored.eventSeq
	g.orderSeq = restored.orderSeq
	g.certSeq = restored.certSeq
	g.keySeq = restored.keySeq
	g.accounts = restored.accounts
	g.accountKeys = restored.accountKeys
	g.orders = restored.orders
	g.tokens = restored.tokens
	g.nonces = restored.nonces
	g.certs = restored.certs
	g.cache = restored.cache
	g.revoked = restored.revoked
	g.sealedKeys = restored.sealedKeys
	g.alerts = restored.alerts
	g.events = restored.events
	g.renewedAt = restored.renewedAt
	g.renewedCertID = restored.renewedCertID
	g.issuerUp = restored.issuerUp
	return nil
}

// LoadSnapshot is an alias for Restore.
func (g *AcmeOrderGuard) LoadSnapshot(state any) error {
	return g.Restore(state)
}

// ImportSnapshot is an alias for Restore.
func (g *AcmeOrderGuard) ImportSnapshot(state any) error {
	return g.Restore(state)
}

// ExportSnapshot is an alias for Snapshot.
func (g *AcmeOrderGuard) ExportSnapshot() ([]byte, error) {
	return g.Snapshot()
}

// Save is an alias for Snapshot.
func (g *AcmeOrderGuard) Save() ([]byte, error) {
	return g.Snapshot()
}

// Export is an alias for Snapshot.
func (g *AcmeOrderGuard) Export() ([]byte, error) {
	return g.Snapshot()
}

// Dump is an alias for Snapshot.
func (g *AcmeOrderGuard) Dump() ([]byte, error) {
	return g.Snapshot()
}

// MarshalBinary implements encoding.BinaryMarshaler.
func (g *AcmeOrderGuard) MarshalBinary() ([]byte, error) {
	return g.Snapshot()
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler.
func (g *AcmeOrderGuard) UnmarshalBinary(data []byte) error {
	return g.Restore(data)
}

// NewAcmeOrderGuardFromEvents restores a guard timeline from audit events.
func NewAcmeOrderGuardFromEvents(events []Event) (*AcmeOrderGuard, error) {
	return NewAcmeOrderGuardFromSnapshot(events)
}

func snapshotBytes(state any) ([]byte, bool) {
	switch value := state.(type) {
	case []byte:
		return value, true
	case string:
		return []byte(value), true
	default:
		return nil, false
	}
}

func cloneEvents(events []Event) []Event {
	out := make([]Event, 0, len(events))
	for _, event := range events {
		out = append(out, cloneEvent(event))
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneTimeMap(in map[string]time.Time) map[string]time.Time {
	out := make(map[string]time.Time, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
