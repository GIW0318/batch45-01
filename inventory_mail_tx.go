package task

import (
	"errors"
	"sync"
)

var (
	errMailNotFound      = errors.New("mail not found")
	errMailAlreadyGone   = errors.New("mail already claimed or expired")
	errInvalidAttachment = errors.New("invalid attachment")
	errStackOverflow     = errors.New("stack limit exceeded")
	errInvalidStack      = errors.New("invalid stack values")
	errBagFull           = errors.New("bag has no free slot")
	errTooManyInflight   = errors.New("too many in-flight claims for player")
	errTooManyReceipts   = errors.New("receipt limit reached")
	errCrossServerLocked = errors.New("character already holds a cross-server claim lock")
)

const (
	maxInflightClaimsPerPlayer = 1024
	maxStoredReceipts          = 100000
	mailPresent                = "present"
	mailDeleted                = "deleted"
)

// Attachment is one item stack attached to a mail.
type Attachment struct {
	ItemID string
	Count  int
	Bound  bool
}

// ClaimResult is the outcome of claiming a mail attachment.
type ClaimResult struct {
	MailDeleted bool
	Granted     []Attachment
}

type mailRecord struct {
	attachments []Attachment
	state       string
	seq         uint64
}

type itemStack struct {
	itemID string
	count  int
	bound  bool
}

type playerGate struct {
	mu      sync.Mutex
	cond    *sync.Cond
	next    uint64
	serving uint64
}

func newPlayerGate() *playerGate {
	gate := &playerGate{}
	gate.cond = sync.NewCond(&gate.mu)
	return gate
}

func (g *playerGate) acquire() {
	g.mu.Lock()
	ticket := g.next
	g.next++
	for ticket != g.serving {
		g.cond.Wait()
	}
	g.mu.Unlock()
}

func (g *playerGate) release() {
	g.mu.Lock()
	g.serving++
	g.cond.Broadcast()
	g.mu.Unlock()
}

var mailLedger = struct {
	mu       sync.Mutex
	receipts map[string]struct{}
	closed   map[string]struct{}
}{
	receipts: make(map[string]struct{}),
	closed:   make(map[string]struct{}),
}

func persistReceiptID(mailID string) bool {
	mailLedger.mu.Lock()
	defer mailLedger.mu.Unlock()

	if _, ok := mailLedger.receipts[mailID]; ok {
		mailLedger.closed[mailID] = struct{}{}
		return true
	}
	if len(mailLedger.receipts) >= maxStoredReceipts {
		return false
	}
	mailLedger.receipts[mailID] = struct{}{}
	mailLedger.closed[mailID] = struct{}{}
	return true
}

func receiptCount() int {
	mailLedger.mu.Lock()
	defer mailLedger.mu.Unlock()
	return len(mailLedger.receipts)
}

func markMailClosed(mailID string) {
	mailLedger.mu.Lock()
	defer mailLedger.mu.Unlock()
	if len(mailLedger.closed) < maxStoredReceipts {
		mailLedger.closed[mailID] = struct{}{}
	}
}

func mailWasClosed(mailID string) bool {
	mailLedger.mu.Lock()
	defer mailLedger.mu.Unlock()
	_, ok := mailLedger.closed[mailID]
	return ok
}

// MailInventoryTx claims mail attachments inside one inventory transaction.
type MailInventoryTx struct {
	mu         sync.Mutex
	slots      int
	stackLimit int
	used       int
	stacks     []itemStack
	mails      map[string]*mailRecord
	nextSeq    uint64

	gatesMu sync.Mutex
	gates   map[string]*playerGate

	inflightMu sync.Mutex
	inflight   map[string]int

	crossMu    sync.Mutex
	crossLocks map[string]struct{}

	receiptsMu sync.Mutex
	receipts   map[string]struct{}
}

// NewMailInventoryTx builds a mail inventory transaction with the given slot count.
func NewMailInventoryTx(slots int) *MailInventoryTx {
	if slots < 0 {
		slots = 0
	}
	return &MailInventoryTx{
		slots:      slots,
		stackLimit: slots,
		mails:      make(map[string]*mailRecord),
		gates:      make(map[string]*playerGate),
		inflight:   make(map[string]int),
		crossLocks: make(map[string]struct{}),
		receipts:   make(map[string]struct{}),
	}
}

// Deposit stores a mail with its attachments.
func (m *MailInventoryTx) Deposit(mailID string, atts []Attachment) {
	copied := append([]Attachment(nil), atts...)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.receiptsMu.Lock()
	_, alreadyClaimed := m.receipts[mailID]
	m.receiptsMu.Unlock()
	if alreadyClaimed {
		return
	}

	if record, ok := m.mails[mailID]; ok && record.state == mailPresent {
		return
	}
	m.nextSeq++
	m.mails[mailID] = &mailRecord{
		attachments: copied,
		state:       mailPresent,
		seq:         m.nextSeq,
	}
}

func (m *MailInventoryTx) gateFor(charID string) *playerGate {
	m.gatesMu.Lock()
	defer m.gatesMu.Unlock()

	gate, ok := m.gates[charID]
	if !ok {
		gate = newPlayerGate()
		m.gates[charID] = gate
	}
	return gate
}

func (m *MailInventoryTx) beginPlayerClaim(charID string) (func(), error) {
	m.inflightMu.Lock()
	if m.inflight[charID] >= maxInflightClaimsPerPlayer {
		m.inflightMu.Unlock()
		return nil, errTooManyInflight
	}
	m.inflight[charID]++
	m.inflightMu.Unlock()

	gate := m.gateFor(charID)
	gate.acquire()

	return func() {
		gate.release()
		m.inflightMu.Lock()
		if m.inflight[charID] > 0 {
			m.inflight[charID]--
		}
		if m.inflight[charID] == 0 {
			delete(m.inflight, charID)
		}
		m.inflightMu.Unlock()
	}, nil
}

func (m *MailInventoryTx) tryCrossLock(charID string) bool {
	m.crossMu.Lock()
	defer m.crossMu.Unlock()

	if _, ok := m.crossLocks[charID]; ok {
		return false
	}
	m.crossLocks[charID] = struct{}{}
	return true
}

func (m *MailInventoryTx) releaseCrossLock(charID string) {
	m.crossMu.Lock()
	delete(m.crossLocks, charID)
	m.crossMu.Unlock()
}

func (m *MailInventoryTx) recordReceipt(mailID string) bool {
	if !persistReceiptID(mailID) {
		return false
	}
	m.receiptsMu.Lock()
	m.receipts[mailID] = struct{}{}
	m.receiptsMu.Unlock()
	return true
}

func (m *MailInventoryTx) expandLocked(needed int) {
	if needed <= 0 {
		return
	}
	required := m.used + needed
	if required > m.slots {
		m.slots = required
	}
	if m.slots > m.stackLimit {
		m.stackLimit = m.slots
	}
}

// Claim moves the attachments into the bag and deletes the mail atomically.
func (m *MailInventoryTx) Claim(mailID, charID string, bagFree int) (ClaimResult, error) {
	if bagFree < 0 {
		return ClaimResult{}, errBagFull
	}

	releasePlayer, err := m.beginPlayerClaim(charID)
	if err != nil {
		return ClaimResult{}, err
	}
	defer releasePlayer()

	if !m.tryCrossLock(charID) {
		return ClaimResult{}, errCrossServerLocked
	}
	defer m.releaseCrossLock(charID)

	m.mu.Lock()
	defer m.mu.Unlock()

	record, ok := m.mails[mailID]
	if !ok {
		if mailWasClosed(mailID) {
			return ClaimResult{}, errMailAlreadyGone
		}
		return ClaimResult{}, errMailNotFound
	}
	if record.state != mailPresent {
		return ClaimResult{}, errMailAlreadyGone
	}

	m.receiptsMu.Lock()
	_, alreadyClaimed := m.receipts[mailID]
	m.receiptsMu.Unlock()
	if alreadyClaimed {
		return ClaimResult{}, errMailAlreadyGone
	}

	granted := append([]Attachment(nil), record.attachments...)
	for _, att := range granted {
		if att.Count <= 0 {
			return ClaimResult{}, errInvalidAttachment
		}
		if !m.StackValid(m.stackLimit, att.Count) {
			return ClaimResult{}, errStackOverflow
		}
	}

	needed := len(granted)
	if bagFree < needed {
		return ClaimResult{}, errBagFull
	}
	if needed > 0 && m.used > int(^uint(0)>>1)-needed {
		return ClaimResult{}, errBagFull
	}

	staged := make([]itemStack, len(m.stacks), len(m.stacks)+needed)
	copy(staged, m.stacks)
	for _, att := range granted {
		staged = append(staged, itemStack{
			itemID: att.ItemID,
			count:  att.Count,
			bound:  att.Bound,
		})
	}

	if !m.recordReceipt(mailID) {
		return ClaimResult{}, errTooManyReceipts
	}

	m.expandLocked(needed)
	m.stacks = staged
	m.used += needed
	record.state = mailDeleted
	record.attachments = nil

	return ClaimResult{
		MailDeleted: true,
		Granted:     granted,
	}, nil
}

// MailState is the lifecycle state of a mail: present or deleted.
func (m *MailInventoryTx) MailState(mailID string) string {
	m.mu.Lock()
	record, ok := m.mails[mailID]
	if ok {
		state := record.state
		m.mu.Unlock()
		return state
	}
	m.mu.Unlock()

	if mailWasClosed(mailID) {
		return mailDeleted
	}
	return ""
}

// ConcurrentClaim runs several claim attempts and reports how many succeeded.
func (m *MailInventoryTx) ConcurrentClaim(mailID string, workers int) int {
	if workers <= 0 {
		return 0
	}

	m.mu.Lock()
	bagFree := m.slots - m.used
	m.mu.Unlock()
	if bagFree < 0 {
		bagFree = 0
	}

	var (
		wg      sync.WaitGroup
		start   = make(chan struct{})
		success int
		result  sync.Mutex
	)

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			res, err := m.Claim(mailID, "concurrent-claim", bagFree)
			if err == nil && res.MailDeleted {
				result.Lock()
				success++
				result.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	return success
}

// Tradable reports whether an attachment may be traded.
func (m *MailInventoryTx) Tradable(att Attachment) bool {
	return !att.Bound
}

// MergeStack merges a stack without overflowing the stack limit.
func (m *MailInventoryTx) MergeStack(existing, incoming, limit int) (int, error) {
	if existing < 0 || incoming < 0 || limit < 0 {
		return 0, errInvalidStack
	}
	if existing > limit || incoming > limit-existing {
		return limit, errStackOverflow
	}
	return existing + incoming, nil
}

// ExpireReturn returns the attachments of an expired mail to the sender.
func (m *MailInventoryTx) ExpireReturn(mailID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	record, ok := m.mails[mailID]
	if !ok {
		return 0, errMailNotFound
	}
	if record.state != mailPresent {
		return 0, nil
	}

	returned := len(record.attachments)
	record.state = mailDeleted
	record.attachments = nil
	markMailClosed(mailID)
	return returned, nil
}

// CrossServerLock takes the cross server claim lock for a character.
func (m *MailInventoryTx) CrossServerLock(charID string) bool {
	return m.tryCrossLock(charID)
}

// StackValid reports whether a stack count respects the stack limit.
func (m *MailInventoryTx) StackValid(limit, count int) bool {
	return limit >= 0 && count >= 0 && count <= limit
}

// MarkReceipt records that a claim receipt was written.
func (m *MailInventoryTx) MarkReceipt(mailID string) {
	m.recordReceipt(mailID)
}

// PersistReceipts returns how many receipts were persisted.
func (m *MailInventoryTx) PersistReceipts() int {
	m.receiptsMu.Lock()
	local := len(m.receipts)
	m.receiptsMu.Unlock()
	if local > 0 {
		return local
	}
	return receiptCount()
}

// EnsureSlots makes sure the bag can hold the needed slots.
func (m *MailInventoryTx) EnsureSlots(needed int) (int, error) {
	if needed < 0 {
		return 0, errBagFull
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	free := m.slots - m.used
	if free < needed {
		if m.used > int(^uint(0)>>1)-needed {
			return 0, errBagFull
		}
		m.expandLocked(needed)
		free = m.slots - m.used
	}
	return free, nil
}

// ExpiredItemClaimable reports whether an attachment is still claimable.
func (m *MailInventoryTx) ExpiredItemClaimable(expiredAt, now int64) bool {
	return now < expiredAt
}

// RollbackState is the mail state after a rolled back claim transaction.
func (m *MailInventoryTx) RollbackState(mailID string) string {
	return m.MailState(mailID)
}

// InventoryCount is the number of an item currently in the bag.
func (m *MailInventoryTx) InventoryCount(itemID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	total := 0
	for _, stack := range m.stacks {
		if stack.itemID == itemID {
			total += stack.count
		}
	}
	return total
}

// MailCount is the number of mails currently stored.
func (m *MailInventoryTx) MailCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for _, record := range m.mails {
		if record.state == mailPresent {
			count++
		}
	}
	return count
}

// Apply writes one attachment into the bag and returns the slots used.
func (m *MailInventoryTx) Apply(att Attachment) (int, error) {
	if att.Count <= 0 {
		return 0, errInvalidAttachment
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.StackValid(m.stackLimit, att.Count) {
		return 0, errStackOverflow
	}
	if m.used >= m.slots {
		return 0, errBagFull
	}

	m.stacks = append(m.stacks, itemStack{
		itemID: att.ItemID,
		count:  att.Count,
		bound:  att.Bound,
	})
	m.used++
	return 1, nil
}
