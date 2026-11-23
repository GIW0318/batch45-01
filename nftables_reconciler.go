package task

import (
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Family is the nftables address family.
type Family string

const (
	FamilyIPv4 Family = "ip"
	FamilyIPv6 Family = "ipv6"
)

// Rule is one nftables rule.
type Rule struct {
	Chain   string
	Expr    string
	Comment string
	Family  Family
	SetRefs []string
}

// Set is one named set of the table.
type Set struct {
	Name     string
	Family   Family
	Timeout  time.Duration
	Elements []string
}

// Op is one operation of a batch.
type Op struct {
	Kind    string
	Table   string
	Payload string
}

// Batch is an atomic group of nftables operations.
type Batch struct {
	Ops []Op
}

// ReconcilerOptions configures a reconciler.
type ReconcilerOptions struct {
	Family   Family
	Table    string
	MaxBatch int
}

type tableSet struct {
	Name     string
	Family   Family
	Timeout  time.Duration
	Elements []string
}

type tableState struct {
	Present bool
	Family  Family
	Table   string
	Sets    map[string]tableSet
	Chains  map[string]bool
	Rules   map[string]Rule
}

// NftReconciler renders and applies nftables state.
type NftReconciler struct {
	mu      sync.RWMutex
	applyMu sync.Mutex

	opts ReconcilerOptions

	chains map[string]bool
	sets   map[string]tableSet
	rules  map[string]Rule

	counters map[string]uint64

	// hasDesired distinguishes an explicitly declared target from the fallback
	// view of a restored table.
	hasDesired  bool
	baseline    tableState
	hasBaseline bool

	snapshot    string
	hasSnapshot bool

	batchTargets map[*Batch]tableState
}

// NewNftReconciler returns a reconciler for one table.
func NewNftReconciler(opts ReconcilerOptions) *NftReconciler {
	if opts.Family == "" {
		opts.Family = FamilyIPv4
	}
	if opts.Table == "" {
		opts.Table = "filter"
	}
	if opts.MaxBatch <= 0 {
		opts.MaxBatch = 1024
	}
	return &NftReconciler{
		opts:         opts,
		chains:       make(map[string]bool),
		sets:         make(map[string]tableSet),
		rules:        make(map[string]Rule),
		counters:     make(map[string]uint64),
		batchTargets: make(map[*Batch]tableState),
	}
}

func newTableState(family Family, table string) tableState {
	return tableState{
		Present: true,
		Family:  family,
		Table:   table,
		Sets:    make(map[string]tableSet),
		Chains:  make(map[string]bool),
		Rules:   make(map[string]Rule),
	}
}

func cloneState(src tableState) tableState {
	dst := tableState{
		Present: src.Present,
		Family:  src.Family,
		Table:   src.Table,
		Sets:    make(map[string]tableSet, len(src.Sets)),
		Chains:  make(map[string]bool, len(src.Chains)),
		Rules:   make(map[string]Rule, len(src.Rules)),
	}
	for name, set := range src.Sets {
		set.Elements = append([]string(nil), set.Elements...)
		dst.Sets[name] = set
	}
	for name := range src.Chains {
		dst.Chains[name] = true
	}
	for key, rule := range src.Rules {
		rule.SetRefs = append([]string(nil), rule.SetRefs...)
		dst.Rules[key] = rule
	}
	return dst
}

func validIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '_', c == '-', c == '.', c == ':':
		default:
			return false
		}
	}
	return true
}

func validFamily(f Family) bool {
	return f == FamilyIPv4 || f == FamilyIPv6
}

func (r *NftReconciler) familyFor(f Family) (Family, error) {
	if f == "" {
		f = r.opts.Family
	}
	if !validFamily(f) {
		return "", fmt.Errorf("unsupported address family %q", f)
	}
	return f, nil
}

func (r *NftReconciler) checkTable() error {
	if !validFamily(r.opts.Family) {
		return fmt.Errorf("unsupported table family %q", r.opts.Family)
	}
	if !validIdent(r.opts.Table) {
		return fmt.Errorf("invalid table name %q", r.opts.Table)
	}
	return nil
}

// DeclareChain declares a chain in the table.
func (r *NftReconciler) DeclareChain(name string) error {
	if !validIdent(name) {
		return fmt.Errorf("invalid chain name %q", name)
	}
	if err := r.checkTable(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chains[name] = true
	r.hasDesired = true
	return nil
}

// DeclareSet declares a set in the table. Redeclaring a set replaces its
// declaration and element list.
func (r *NftReconciler) DeclareSet(s Set) error {
	if !validIdent(s.Name) {
		return fmt.Errorf("invalid set name %q", s.Name)
	}
	family, err := r.familyFor(s.Family)
	if err != nil {
		return err
	}
	if family != r.opts.Family {
		return fmt.Errorf("set %q family %q does not match table family %q", s.Name, family, r.opts.Family)
	}
	if s.Timeout < 0 {
		return fmt.Errorf("set %q has a negative timeout", s.Name)
	}
	elements, err := canonicalElements(s.Elements)
	if err != nil {
		return fmt.Errorf("set %q: %w", s.Name, err)
	}
	if err := r.checkTable(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sets[s.Name] = tableSet{
		Name:     s.Name,
		Family:   family,
		Timeout:  s.Timeout,
		Elements: elements,
	}
	r.hasDesired = true
	return nil
}

// AddRule adds a rule to a declared chain.
func (r *NftReconciler) AddRule(rule Rule) error {
	if !validIdent(rule.Chain) {
		return fmt.Errorf("invalid chain name %q", rule.Chain)
	}
	family, err := r.familyFor(rule.Family)
	if err != nil {
		return err
	}
	if family != r.opts.Family {
		return fmt.Errorf("rule family %q does not match table family %q", family, r.opts.Family)
	}
	expr, tokens, err := normalizeExpr(rule.Expr)
	if err != nil {
		return err
	}
	if expr == "" {
		return errors.New("rule expression is empty")
	}
	if err := validateComment(rule.Comment); err != nil {
		return err
	}
	if err := r.checkICMPGuard(tokens); err != nil {
		return err
	}
	if err := r.checkIPFamilyTokens(tokens); err != nil {
		return err
	}

	refs := make([]string, 0, len(rule.SetRefs))
	seenRefs := make(map[string]bool)
	for _, ref := range rule.SetRefs {
		ref = strings.TrimSpace(strings.TrimPrefix(ref, "@"))
		if ref == "" {
			return errors.New("empty set reference")
		}
		if !seenRefs[ref] {
			seenRefs[ref] = true
			refs = append(refs, ref)
		}
	}
	for _, tok := range tokens {
		for _, ref := range refsInToken(tok) {
			if !seenRefs[ref] {
				seenRefs[ref] = true
				refs = append(refs, ref)
			}
		}
	}
	sort.Strings(refs)

	if err := r.checkTable(); err != nil {
		return err
	}

	canonical := Rule{
		Chain:   rule.Chain,
		Expr:    expr,
		Comment: rule.Comment,
		Family:  family,
		SetRefs: refs,
	}
	key := ruleKey(canonical)

	r.mu.Lock()
	defer r.mu.Unlock()

	importChains := make([]string, 0)
	if !r.chains[rule.Chain] {
		if !r.hasBaseline || !r.baseline.Chains[rule.Chain] {
			return fmt.Errorf("rule references undeclared chain %q", rule.Chain)
		}
		importChains = append(importChains, rule.Chain)
	}
	importSets := make([]string, 0)
	for _, ref := range refs {
		if _, ok := r.sets[ref]; ok {
			continue
		}
		if r.hasBaseline {
			if _, ok := r.baseline.Sets[ref]; ok {
				importSets = append(importSets, ref)
				continue
			}
		}
		return fmt.Errorf("rule references undeclared set %q", ref)
	}
	for _, ref := range chainRefsInTokens(tokens) {
		if r.chains[ref] {
			continue
		}
		if r.hasBaseline && r.baseline.Chains[ref] {
			importChains = append(importChains, ref)
			continue
		}
		return fmt.Errorf("rule references undeclared chain %q", ref)
	}
	for _, name := range importChains {
		r.chains[name] = true
	}
	for _, name := range importSets {
		set := r.baseline.Sets[name]
		set.Elements = append([]string(nil), set.Elements...)
		r.sets[name] = set
	}
	r.rules[key] = canonical
	r.hasDesired = true
	return nil
}

func (r *NftReconciler) checkIPFamilyTokens(tokens []string) error {
	if r.opts.Family != FamilyIPv4 {
		return nil
	}
	for _, tok := range tokens {
		switch strings.ToLower(tok) {
		case "ip6", "ipv6", "icmp6", "icmpv6", "ipv6-icmp":
			return fmt.Errorf("IPv6 token %q cannot be used in an IPv4 table", tok)
		}
	}
	return nil
}

func (r *NftReconciler) checkICMPGuard(tokens []string) error {
	for i, tok := range tokens {
		if !isICMPProtocol(tok) {
			continue
		}
		accept := -1
		for j := i + 1; j < len(tokens); j++ {
			if strings.EqualFold(tokens[j], "accept") {
				accept = j
				break
			}
		}
		if accept < 0 {
			continue
		}
		typed := false
		for j := i + 1; j < accept; j++ {
			if strings.EqualFold(tokens[j], "type") || strings.EqualFold(tokens[j], "code") {
				typed = true
				break
			}
		}
		if !typed {
			return fmt.Errorf("blanket %s accept is not allowed", tok)
		}
	}
	return nil
}

func isICMPProtocol(tok string) bool {
	switch strings.ToLower(tok) {
	case "icmp", "icmp6", "icmpv6", "ipv6-icmp":
		return true
	default:
		return false
	}
}

func refsInToken(tok string) []string {
	var refs []string
	for {
		i := strings.IndexByte(tok, '@')
		if i < 0 {
			break
		}
		tok = tok[i+1:]
		j := 0
		for j < len(tok) && isRefChar(tok[j]) {
			j++
		}
		if j > 0 {
			refs = append(refs, tok[:j])
		}
		tok = tok[j:]
	}
	return refs
}

func isRefChar(b byte) bool {
	return b >= 'a' && b <= 'z' ||
		b >= 'A' && b <= 'Z' ||
		b >= '0' && b <= '9' ||
		b == '_' || b == '-' || b == '.' || b == ':'
}

func chainRefsInTokens(tokens []string) []string {
	var refs []string
	for i := 0; i+1 < len(tokens); i++ {
		op := strings.ToLower(tokens[i])
		if op != "jump" && op != "goto" {
			continue
		}
		next := tokens[i+1]
		if strings.HasPrefix(next, "@") || next == "" {
			continue
		}
		refs = append(refs, next)
	}
	return refs
}

func canonicalElements(elements []string) ([]string, error) {
	seen := make(map[string]bool, len(elements))
	out := make([]string, 0, len(elements))
	for _, element := range elements {
		if err := validateElement(element); err != nil {
			return nil, err
		}
		if !seen[element] {
			seen[element] = true
			out = append(out, element)
		}
	}
	sort.Strings(out)
	return out, nil
}

func validateElement(element string) error {
	if element == "" {
		return errors.New("empty set element")
	}
	for _, c := range element {
		if c <= ' ' || c == 0x7f {
			return fmt.Errorf("set element %q contains whitespace or a control character", element)
		}
		switch c {
		case '{', '}', '"', '\'', ',', ';', '#':
			return fmt.Errorf("set element %q contains an unsafe character", element)
		}
	}
	return nil
}

func validateComment(comment string) error {
	for _, c := range comment {
		if c < 0x20 || c == 0x7f {
			return errors.New("rule comment contains a control character or newline")
		}
	}
	return nil
}

func escapeComment(comment string) string {
	var b strings.Builder
	for _, c := range comment {
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

func normalizeExpr(expr string) (string, []string, error) {
	tokens, err := lexText(expr)
	if err != nil {
		return "", nil, err
	}
	var out []string
	for _, tok := range tokens {
		switch tok.kind {
		case tokEOF:
		case tokNewline, tokSemicolon:
			return "", nil, errors.New("rule expression contains a statement separator or newline")
		case tokCommentStart:
			return "", nil, errors.New("rule expression contains an unquoted comment")
		default:
			out = append(out, tokenText(tok))
		}
	}
	if len(out) == 0 {
		return "", nil, errors.New("rule expression is empty")
	}
	return strings.Join(out, " "), out, nil
}

func ruleKey(rule Rule) string {
	refs := append([]string(nil), rule.SetRefs...)
	sort.Strings(refs)
	return string(rule.Family) + "\x00" + rule.Chain + "\x00" + rule.Expr + "\x00" + rule.Comment + "\x00" + strings.Join(refs, ",")
}

func ruleLine(rule Rule) string {
	line := rule.Expr
	if rule.Comment != "" {
		line += ` comment "` + escapeComment(rule.Comment) + `"`
	}
	return line
}

func setType(family Family) string {
	if family == FamilyIPv6 {
		return "ipv6_addr"
	}
	return "ipv4_addr"
}

func sortedSetNames(sets map[string]tableSet) []string {
	names := make([]string, 0, len(sets))
	for name := range sets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedChainNames(chains map[string]bool) []string {
	names := make([]string, 0, len(chains))
	for name := range chains {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedRules(rules map[string]Rule) []Rule {
	out := make([]Rule, 0, len(rules))
	for _, rule := range rules {
		out = append(out, rule)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Chain != out[j].Chain {
			return out[i].Chain < out[j].Chain
		}
		return ruleKey(out[i]) < ruleKey(out[j])
	})
	return out
}

func renderState(state tableState, counters map[string]uint64) string {
	if !state.Present {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "table %s %s {\n", state.Family, state.Table)
	for _, name := range sortedSetNames(state.Sets) {
		set := state.Sets[name]
		fmt.Fprintf(&b, "\tset %s {\n", set.Name)
		fmt.Fprintf(&b, "\t\ttype %s\n", setType(set.Family))
		if set.Timeout > 0 {
			fmt.Fprintf(&b, "\t\ttimeout %dms\n", set.Timeout.Milliseconds())
		}
		if len(set.Elements) > 0 {
			fmt.Fprintf(&b, "\t\telements = { %s }\n", strings.Join(set.Elements, ", "))
		}
		b.WriteString("\t}\n")
	}
	for _, chain := range sortedChainNames(state.Chains) {
		fmt.Fprintf(&b, "\tchain %s {\n", chain)
		for _, rule := range sortedRules(state.Rules) {
			if rule.Chain != chain {
				continue
			}
			fmt.Fprintf(&b, "\t\t%s\n", ruleLine(rule))
		}
		b.WriteString("\t}\n")
	}
	if len(counters) > 0 {
		ids := make([]string, 0, len(counters))
		for id := range counters {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			encoded := base64.RawURLEncoding.EncodeToString([]byte(id))
			if encoded == "" {
				encoded = "."
			}
			fmt.Fprintf(&b, "\t# nftr-counter %s %d\n", encoded, counters[id])
		}
	}
	b.WriteString("}\n")
	return b.String()
}

// ElementBatch builds the batch that updates set elements in place.
func (r *NftReconciler) ElementBatch(setName string, add, remove []string) (*Batch, error) {
	if !validIdent(setName) {
		return nil, fmt.Errorf("invalid set name %q", setName)
	}
	addElements, err := canonicalElements(add)
	if err != nil {
		return nil, err
	}
	removeElements, err := canonicalElements(remove)
	if err != nil {
		return nil, err
	}
	if err := r.checkTable(); err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.sets[setName]; !ok && !r.hasDesired && r.hasBaseline {
		if _, ok := r.baseline.Sets[setName]; ok {
			r.adoptBaselineLocked()
		}
	}
	if _, ok := r.sets[setName]; !ok && r.hasBaseline {
		if base, ok := r.baseline.Sets[setName]; ok {
			base.Elements = append([]string(nil), base.Elements...)
			r.sets[setName] = base
		}
	}
	set, ok := r.sets[setName]
	if !ok {
		return nil, fmt.Errorf("element batch for undeclared set %q", setName)
	}

	ops := make([]Op, 0, len(addElements)+len(removeElements))
	for _, element := range removeElements {
		ops = append(ops, Op{
			Kind:    "delete_element",
			Table:   r.opts.Table,
			Payload: fmt.Sprintf("set %s delete %s", setName, element),
		})
	}
	for _, element := range addElements {
		ops = append(ops, Op{
			Kind:    "add_element",
			Table:   r.opts.Table,
			Payload: fmt.Sprintf("set %s add %s", setName, element),
		})
	}

	if len(ops) > 0 {
		current := make(map[string]bool, len(set.Elements))
		for _, element := range set.Elements {
			current[element] = true
		}
		for _, element := range removeElements {
			delete(current, element)
		}
		for _, element := range addElements {
			current[element] = true
		}
		set.Elements = set.Elements[:0]
		for element := range current {
			set.Elements = append(set.Elements, element)
		}
		sort.Strings(set.Elements)
		r.sets[setName] = set
		r.hasDesired = true
	}

	batch, err := r.finishBatchLocked(ops)
	if err != nil {
		return nil, err
	}
	r.batchTargets[batch] = r.desiredStateLocked()
	return batch, nil
}

func (r *NftReconciler) adoptBaselineLocked() {
	if !r.hasBaseline {
		return
	}
	for name, set := range r.baseline.Sets {
		set.Elements = append([]string(nil), set.Elements...)
		r.sets[name] = set
	}
	for name := range r.baseline.Chains {
		r.chains[name] = true
	}
	for key, rule := range r.baseline.Rules {
		rule.SetRefs = append([]string(nil), rule.SetRefs...)
		r.rules[key] = rule
	}
	r.hasDesired = true
}

// SetCounter records a packet counter for a rule.
func (r *NftReconciler) SetCounter(ruleID string, v uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters[ruleID] = v
	return nil
}

// Counters returns the current counters.
func (r *NftReconciler) Counters() map[string]uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]uint64, len(r.counters))
	for id, v := range r.counters {
		out[id] = v
	}
	return out
}

func (r *NftReconciler) desiredStateLocked() tableState {
	state := newTableState(r.opts.Family, r.opts.Table)
	for name, set := range r.sets {
		set.Elements = append([]string(nil), set.Elements...)
		state.Sets[name] = set
	}
	for name := range r.chains {
		state.Chains[name] = true
	}
	for key, rule := range r.rules {
		rule.SetRefs = append([]string(nil), rule.SetRefs...)
		state.Rules[key] = rule
	}
	return state
}

func (r *NftReconciler) currentTargetLocked() tableState {
	if r.hasDesired {
		return r.desiredStateLocked()
	}
	if r.hasBaseline {
		return cloneState(r.baseline)
	}
	return newTableState(r.opts.Family, r.opts.Table)
}

// Render produces the full table text.
func (r *NftReconciler) Render() (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := r.checkTable(); err != nil {
		return "", err
	}
	return renderState(r.currentTargetLocked(), r.counters), nil
}

// RenderBatch produces the atomic batch that installs the table.
func (r *NftReconciler) RenderBatch() (*Batch, error) {
	if err := r.checkTable(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	target := r.currentTargetLocked()
	from := tableState{}
	if r.hasBaseline {
		from = cloneState(r.baseline)
	}
	batch, err := r.diffBatchLocked(from, target, false)
	if err != nil {
		return nil, err
	}
	r.batchTargets[batch] = cloneState(target)
	return batch, nil
}

func (r *NftReconciler) diffBatchLocked(from, to tableState, allowDeleteTable bool) (*Batch, error) {
	ops := r.diffOps(from, to, allowDeleteTable)
	return r.finishBatchLocked(ops)
}

func (r *NftReconciler) diffOps(from, to tableState, allowDeleteTable bool) []Op {
	var ops []Op
	addTable := to.Present && (!from.Present || from.Family != to.Family || from.Table != to.Table)
	if addTable {
		ops = append(ops, Op{
			Kind:    "add_table",
			Table:   to.Table,
			Payload: fmt.Sprintf("table %s %s", to.Family, to.Table),
		})
	}

	sameTable := from.Present && to.Present && from.Family == to.Family && from.Table == to.Table
	if sameTable {
		ops = append(ops, r.deleteOps(from, to)...)
	}
	if to.Present {
		ops = append(ops, r.addOps(from, to, sameTable)...)
	}
	if !to.Present && from.Present && allowDeleteTable {
		ops = append(ops, r.deleteTableOps(from)...)
	}
	return ops
}

func (r *NftReconciler) deleteOps(from, to tableState) []Op {
	var ops []Op

	for _, key := range sortedRuleKeys(from.Rules) {
		if _, ok := to.Rules[key]; ok {
			continue
		}
		rule := from.Rules[key]
		ops = append(ops, Op{
			Kind:    "delete_rule",
			Table:   r.opts.Table,
			Payload: fmt.Sprintf("chain %s %s", rule.Chain, ruleLine(rule)),
		})
	}

	for _, name := range sortedSetNames(from.Sets) {
		old := from.Sets[name]
		current, ok := to.Sets[name]
		if ok && sameSetProperties(old, current) {
			continue
		}
		ops = append(ops, Op{
			Kind:    "delete_set",
			Table:   r.opts.Table,
			Payload: fmt.Sprintf("set %s", name),
		})
	}

	for _, name := range sortedChainNames(from.Chains) {
		if to.Chains[name] {
			continue
		}
		ops = append(ops, Op{
			Kind:    "delete_chain",
			Table:   r.opts.Table,
			Payload: fmt.Sprintf("chain %s %s", r.opts.Table, name),
		})
	}

	for _, name := range sortedSetNames(from.Sets) {
		old, ok := from.Sets[name]
		if !ok {
			continue
		}
		current, ok := to.Sets[name]
		if !ok || !sameSetProperties(old, current) {
			continue
		}
		newElements := elementSet(current.Elements)
		for _, element := range old.Elements {
			if !newElements[element] {
				ops = append(ops, Op{
					Kind:    "delete_element",
					Table:   r.opts.Table,
					Payload: fmt.Sprintf("set %s delete %s", name, element),
				})
			}
		}
	}
	return ops
}

func (r *NftReconciler) addOps(from, to tableState, sameTable bool) []Op {
	var ops []Op

	for _, name := range sortedSetNames(to.Sets) {
		current := to.Sets[name]
		old, ok := from.Sets[name]
		if sameTable && ok && sameSetProperties(old, current) {
			continue
		}
		ops = append(ops, Op{
			Kind:    "add_set",
			Table:   r.opts.Table,
			Payload: setPayload(current),
		})
	}

	for _, name := range sortedChainNames(to.Chains) {
		if sameTable && from.Chains[name] {
			continue
		}
		ops = append(ops, Op{
			Kind:    "add_chain",
			Table:   r.opts.Table,
			Payload: fmt.Sprintf("chain %s %s", r.opts.Table, name),
		})
	}

	for _, name := range sortedSetNames(to.Sets) {
		current := to.Sets[name]
		old, ok := from.Sets[name]
		if !sameTable || !ok || !sameSetProperties(old, current) {
			for _, element := range current.Elements {
				ops = append(ops, Op{
					Kind:    "add_element",
					Table:   r.opts.Table,
					Payload: fmt.Sprintf("set %s add %s", name, element),
				})
			}
			continue
		}
		oldElements := elementSet(old.Elements)
		for _, element := range current.Elements {
			if !oldElements[element] {
				ops = append(ops, Op{
					Kind:    "add_element",
					Table:   r.opts.Table,
					Payload: fmt.Sprintf("set %s add %s", name, element),
				})
			}
		}
	}

	for _, key := range sortedRuleKeys(to.Rules) {
		if sameTable {
			if _, ok := from.Rules[key]; ok {
				continue
			}
		}
		rule := to.Rules[key]
		ops = append(ops, Op{
			Kind:    "add_rule",
			Table:   r.opts.Table,
			Payload: fmt.Sprintf("chain %s %s", rule.Chain, ruleLine(rule)),
		})
	}
	return ops
}

func (r *NftReconciler) deleteTableOps(from tableState) []Op {
	var ops []Op
	for _, key := range sortedRuleKeys(from.Rules) {
		rule := from.Rules[key]
		ops = append(ops, Op{
			Kind:    "delete_rule",
			Table:   from.Table,
			Payload: fmt.Sprintf("chain %s %s", rule.Chain, ruleLine(rule)),
		})
	}
	for _, name := range sortedChainNames(from.Chains) {
		ops = append(ops, Op{
			Kind:    "delete_chain",
			Table:   from.Table,
			Payload: fmt.Sprintf("chain %s %s", from.Table, name),
		})
	}
	for _, name := range sortedSetNames(from.Sets) {
		ops = append(ops, Op{
			Kind:    "delete_set",
			Table:   from.Table,
			Payload: fmt.Sprintf("set %s", name),
		})
	}
	ops = append(ops, Op{
		Kind:    "delete_table",
		Table:   from.Table,
		Payload: fmt.Sprintf("table %s %s", from.Family, from.Table),
	})
	return ops
}

func sameSetProperties(a, b tableSet) bool {
	return a.Name == b.Name && a.Family == b.Family && a.Timeout == b.Timeout
}

func elementSet(elements []string) map[string]bool {
	out := make(map[string]bool, len(elements))
	for _, element := range elements {
		out[element] = true
	}
	return out
}

func setPayload(set tableSet) string {
	var b strings.Builder
	fmt.Fprintf(&b, "set %s { type %s", set.Name, setType(set.Family))
	if set.Timeout > 0 {
		fmt.Fprintf(&b, " timeout %dms", set.Timeout.Milliseconds())
	}
	b.WriteString(" }")
	return b.String()
}

func sortedRuleKeys(rules map[string]Rule) []string {
	keys := make([]string, 0, len(rules))
	for key := range rules {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := rules[keys[i]], rules[keys[j]]
		if a.Chain != b.Chain {
			return a.Chain < b.Chain
		}
		return keys[i] < keys[j]
	})
	return keys
}

func (r *NftReconciler) finishBatchLocked(ops []Op) (*Batch, error) {
	if len(ops) == 0 {
		return &Batch{}, nil
	}
	full := make([]Op, 0, len(ops)+2)
	full = append(full, Op{
		Kind:    "begin",
		Table:   r.opts.Table,
		Payload: fmt.Sprintf("table %s %s", r.opts.Family, r.opts.Table),
	})
	full = append(full, ops...)
	full = append(full, Op{
		Kind:    "commit",
		Table:   r.opts.Table,
		Payload: fmt.Sprintf("table %s %s", r.opts.Family, r.opts.Table),
	})
	if len(full) > r.opts.MaxBatch {
		return nil, fmt.Errorf("batch has %d operations, exceeds limit %d", len(full), r.opts.MaxBatch)
	}
	return &Batch{Ops: full}, nil
}

// Apply executes the batch, rolling back when it fails.
func (r *NftReconciler) Apply(b *Batch, exec func(*Batch) error) error {
	if b == nil {
		return errors.New("nil batch")
	}
	if exec == nil {
		return errors.New("nil batch executor")
	}
	if len(b.Ops) > r.opts.MaxBatch {
		return fmt.Errorf("batch has %d operations, exceeds limit %d", len(b.Ops), r.opts.MaxBatch)
	}

	r.applyMu.Lock()
	defer r.applyMu.Unlock()

	r.mu.RLock()
	beforeSnapshot := r.snapshotLocked()
	target, knownTarget := r.batchTargets[b]
	if !knownTarget {
		target = r.currentTargetLocked()
	} else {
		target = cloneState(target)
	}
	beforeState := cloneState(r.baseline)
	hadBaseline := r.hasBaseline
	r.mu.RUnlock()

	// A no-op preflight transaction catches unusable executors before the real
	// install. It contains no state-changing operations.
	if err := exec(&Batch{}); err != nil {
		r.mu.Lock()
		r.snapshot = beforeSnapshot
		r.hasSnapshot = true
		r.mu.Unlock()
		return err
	}

	if err := exec(b); err != nil {
		rollbackFrom := target
		rollbackTo := beforeState
		if !hadBaseline {
			rollbackTo = tableState{}
		}
		if rollback, buildErr := r.diffBatchLocked(rollbackFrom, rollbackTo, true); buildErr == nil && len(rollback.Ops) > 0 {
			_ = exec(rollback)
		}
		r.mu.Lock()
		r.snapshot = beforeSnapshot
		r.hasSnapshot = true
		r.mu.Unlock()
		return err
	}

	r.mu.Lock()
	r.baseline = cloneState(target)
	r.hasBaseline = true
	r.snapshot = renderState(target, r.counters)
	r.hasSnapshot = true
	if knownTarget {
		delete(r.batchTargets, b)
	}
	r.mu.Unlock()
	return nil
}

func (r *NftReconciler) snapshotLocked() string {
	if r.hasSnapshot {
		return r.snapshot
	}
	return renderState(r.currentTargetLocked(), r.counters)
}

// Snapshot returns the last applied table text.
func (r *NftReconciler) Snapshot() (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := r.checkTable(); err != nil {
		return "", err
	}
	return r.snapshotLocked(), nil
}

// Restore loads table text produced by Render. The restored table becomes the
// baseline for subsequent diffs. Explicitly declared state remains the desired
// target so a dirty kernel table is reconciled rather than merely appended to.
func (r *NftReconciler) Restore(text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("cannot restore an empty table")
	}
	state, counters, err := parseTable(text)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.baseline = state
	r.hasBaseline = true
	if counters != nil {
		r.counters = counters
	} else {
		r.counters = make(map[string]uint64)
	}
	r.snapshot = renderState(state, r.counters)
	r.hasSnapshot = true
	return nil
}

// ---------------------------------------------------------------------------
// Parser for the table format produced by Render (and common nft list output).

type tokenKind int

const (
	tokWord tokenKind = iota
	tokString
	tokLBrace
	tokRBrace
	tokComma
	tokSemicolon
	tokEquals
	tokNewline
	tokCommentStart
	tokEOF
)

type token struct {
	kind tokenKind
	val  string
}

func tokenText(tok token) string {
	switch tok.kind {
	case tokString:
		return `"` + escapeComment(tok.val) + `"`
	case tokLBrace:
		return "{"
	case tokRBrace:
		return "}"
	case tokComma:
		return ","
	case tokSemicolon:
		return ";"
	case tokEquals:
		return "="
	default:
		return tok.val
	}
}

func lexText(text string) ([]token, error) {
	var tokens []token
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == ' ' || c == '\t' || c == '\v' || c == '\f' || c == '\r':
			i++
		case c == '\n':
			tokens = append(tokens, token{kind: tokNewline})
			i++
		case c == '#':
			tokens = append(tokens, token{kind: tokCommentStart})
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case c == '"':
			start := i
			i++
			var value strings.Builder
			closed := false
			for i < len(text) {
				ch := text[i]
				if ch == '\\' {
					if i+1 >= len(text) {
						return nil, errors.New("unterminated escape in quoted string")
					}
					next := text[i+1]
					switch next {
					case 'n':
						value.WriteByte('\n')
					case 'r':
						value.WriteByte('\r')
					case 't':
						value.WriteByte('\t')
					default:
						value.WriteByte(next)
					}
					i += 2
					continue
				}
				if ch == '"' {
					i++
					closed = true
					break
				}
				value.WriteByte(ch)
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quoted string at byte %d", start)
			}
			tokens = append(tokens, token{kind: tokString, val: value.String()})
		case c == '{':
			tokens = append(tokens, token{kind: tokLBrace})
			i++
		case c == '}':
			tokens = append(tokens, token{kind: tokRBrace})
			i++
		case c == ',':
			tokens = append(tokens, token{kind: tokComma})
			i++
		case c == ';':
			tokens = append(tokens, token{kind: tokSemicolon})
			i++
		case c == '=':
			tokens = append(tokens, token{kind: tokEquals})
			i++
		default:
			start := i
			for i < len(text) {
				ch := text[i]
				if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' ||
					ch == '{' || ch == '}' || ch == ',' || ch == ';' || ch == '=' || ch == '#' || ch == '"' {
					break
				}
				i++
			}
			if i == start {
				return nil, fmt.Errorf("unexpected character %q", text[i])
			}
			tokens = append(tokens, token{kind: tokWord, val: text[start:i]})
		}
	}
	tokens = append(tokens, token{kind: tokEOF})
	return tokens, nil
}

type tableParser struct {
	tokens []token
	pos    int
}

func parseTable(text string) (tableState, map[string]uint64, error) {
	counters := parseCounterMarkers(text)
	tokens, err := lexText(text)
	if err != nil {
		return tableState{}, nil, err
	}
	p := &tableParser{tokens: tokens}
	p.skipSeparators()
	for !p.eof() {
		if p.peek().kind == tokWord && strings.EqualFold(p.peek().val, "table") {
			break
		}
		p.next()
		p.skipSeparators()
	}
	if p.eof() || !strings.EqualFold(p.peek().val, "table") {
		return tableState{}, nil, errors.New("restore text has no table declaration")
	}
	p.next()
	p.skipNewlines()
	if p.eof() || p.peek().kind != tokWord {
		return tableState{}, nil, errors.New("table declaration has no family")
	}
	family := Family(p.next().val)
	p.skipNewlines()
	if p.eof() || p.peek().kind != tokWord {
		return tableState{}, nil, errors.New("table declaration has no name")
	}
	table := p.next().val
	state := newTableState(family, table)

	p.skipSeparators()
	if p.eof() || p.peek().kind != tokLBrace {
		return state, counters, nil
	}
	p.next()
	if err := p.parseTableBody(&state); err != nil {
		return tableState{}, nil, err
	}
	return state, counters, nil
}

func parseCounterMarkers(text string) map[string]uint64 {
	counters := make(map[string]uint64)
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 4 || fields[0] != "#" || fields[1] != "nftr-counter" {
			continue
		}
		idText := fields[2]
		if idText == "." {
			idText = ""
		}
		idBytes, err := base64.RawURLEncoding.DecodeString(idText)
		if err != nil {
			continue
		}
		value, err := strconv.ParseUint(fields[3], 10, 64)
		if err != nil {
			continue
		}
		counters[string(idBytes)] = value
	}
	if len(counters) == 0 {
		return nil
	}
	return counters
}

func (p *tableParser) parseTableBody(state *tableState) error {
	for {
		p.skipSeparators()
		if p.eof() {
			return errors.New("unterminated table block")
		}
		tok := p.peek()
		if tok.kind == tokRBrace {
			p.next()
			return nil
		}
		if tok.kind != tokWord {
			p.skipLine()
			continue
		}
		switch strings.ToLower(tok.val) {
		case "set":
			p.next()
			if err := p.parseSet(state); err != nil {
				return err
			}
		case "chain":
			p.next()
			if err := p.parseChain(state); err != nil {
				return err
			}
		default:
			p.skipLine()
		}
	}
}

func (p *tableParser) parseSet(state *tableState) error {
	p.skipNewlines()
	if p.eof() || p.peek().kind != tokWord {
		return errors.New("set declaration has no name")
	}
	name := p.next().val
	set := tableSet{Name: name, Family: state.Family}
	p.skipNewlines()
	if !p.eof() && p.peek().kind == tokLBrace {
		p.next()
		for {
			p.skipSeparators()
			if p.eof() {
				return errors.New("unterminated set block")
			}
			tok := p.peek()
			if tok.kind == tokRBrace {
				p.next()
				break
			}
			if tok.kind != tokWord {
				p.next()
				continue
			}
			switch strings.ToLower(tok.val) {
			case "type":
				p.skipLine()
			case "timeout":
				p.next()
				p.skipNewlines()
				if !p.eof() && (p.peek().kind == tokWord || p.peek().kind == tokString) {
					if d, err := parseTimeout(p.next().val); err == nil {
						set.Timeout = d
					}
				}
				p.skipLine()
			case "elements":
				p.next()
				p.skipNewlines()
				if !p.eof() && p.peek().kind == tokEquals {
					p.next()
				}
				p.skipNewlines()
				if p.eof() || p.peek().kind != tokLBrace {
					p.skipLine()
					continue
				}
				p.next()
				for {
					p.skipSeparators()
					if p.eof() {
						return errors.New("unterminated elements list")
					}
					element := p.peek()
					if element.kind == tokRBrace {
						p.next()
						break
					}
					p.next()
					if element.kind == tokWord || element.kind == tokString {
						if err := validateElement(element.val); err == nil {
							set.Elements = append(set.Elements, element.val)
						}
					}
				}
			default:
				p.skipLine()
			}
		}
	}
	elements, err := canonicalElements(set.Elements)
	if err != nil {
		return err
	}
	set.Elements = elements
	state.Sets[name] = set
	return nil
}

func (p *tableParser) parseChain(state *tableState) error {
	p.skipNewlines()
	if p.eof() || p.peek().kind != tokWord {
		return errors.New("chain declaration has no name")
	}
	name := p.next().val
	state.Chains[name] = true
	p.skipNewlines()
	if p.eof() || p.peek().kind != tokLBrace {
		return nil
	}
	p.next()
	for {
		p.skipSeparators()
		if p.eof() {
			return errors.New("unterminated chain block")
		}
		if p.peek().kind == tokRBrace {
			p.next()
			return nil
		}
		ruleTokens := p.collectRule()
		if len(ruleTokens) == 0 {
			continue
		}
		rule, ok := ruleFromTokens(name, state.Family, ruleTokens)
		if !ok {
			continue
		}
		state.Rules[ruleKey(rule)] = rule
	}
}

func (p *tableParser) collectRule() []token {
	var out []token
	depth := 0
	for !p.eof() {
		tok := p.peek()
		switch tok.kind {
		case tokLBrace:
			depth++
			out = append(out, tok)
			p.next()
		case tokRBrace:
			if depth == 0 {
				return out
			}
			depth--
			out = append(out, tok)
			p.next()
		case tokNewline, tokSemicolon:
			if depth == 0 {
				p.next()
				return out
			}
			out = append(out, tok)
			p.next()
		case tokCommentStart:
			p.skipLine()
			if depth == 0 {
				return out
			}
		default:
			out = append(out, tok)
			p.next()
		}
	}
	return out
}

func ruleFromTokens(chain string, family Family, tokens []token) (Rule, bool) {
	comment := ""
	filtered := make([]token, 0, len(tokens))
	for i := 0; i < len(tokens); i++ {
		if tokens[i].kind == tokWord && strings.EqualFold(tokens[i].val, "comment") &&
			i+1 < len(tokens) && tokens[i+1].kind == tokString {
			comment = tokens[i+1].val
			i++
			continue
		}
		filtered = append(filtered, tokens[i])
	}
	var parts []string
	var words []string
	for _, tok := range filtered {
		if tok.kind == tokNewline || tok.kind == tokSemicolon {
			continue
		}
		text := tokenText(tok)
		if text == "" {
			continue
		}
		parts = append(parts, text)
		if tok.kind == tokWord || tok.kind == tokString {
			words = append(words, tok.val)
		}
	}
	expr := strings.Join(parts, " ")
	if expr == "" {
		return Rule{}, false
	}
	refs := make([]string, 0)
	seen := make(map[string]bool)
	for _, word := range words {
		for _, ref := range refsInToken(word) {
			if !seen[ref] {
				seen[ref] = true
				refs = append(refs, ref)
			}
		}
	}
	sort.Strings(refs)
	return Rule{
		Chain:   chain,
		Expr:    expr,
		Comment: comment,
		Family:  family,
		SetRefs: refs,
	}, true
}

func parseTimeout(s string) (time.Duration, error) {
	if s == "" {
		return 0, errors.New("empty timeout")
	}
	units := []struct {
		suffix string
		scale  time.Duration
	}{
		{"ms", time.Millisecond},
		{"ns", time.Nanosecond},
		{"us", time.Microsecond},
		{"µs", time.Microsecond},
		{"s", time.Second},
		{"m", time.Minute},
		{"h", time.Hour},
	}
	for _, unit := range units {
		if strings.HasSuffix(s, unit.suffix) {
			number := strings.TrimSuffix(s, unit.suffix)
			if number == "" {
				return 0, errors.New("timeout has no number")
			}
			n, err := strconv.ParseInt(number, 10, 64)
			if err != nil {
				return 0, err
			}
			if n < 0 {
				return 0, errors.New("negative timeout")
			}
			return time.Duration(n) * unit.scale, nil
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(n) * time.Second, nil
}

func (p *tableParser) skipSeparators() {
	for !p.eof() {
		kind := p.peek().kind
		if kind != tokNewline && kind != tokSemicolon && kind != tokComma {
			return
		}
		p.next()
	}
}

func (p *tableParser) skipNewlines() {
	for !p.eof() && p.peek().kind == tokNewline {
		p.next()
	}
}

func (p *tableParser) skipLine() {
	for !p.eof() {
		kind := p.peek().kind
		p.next()
		if kind == tokNewline || kind == tokSemicolon {
			return
		}
	}
}

func (p *tableParser) peek() token {
	if p.pos >= len(p.tokens) {
		return token{kind: tokEOF}
	}
	return p.tokens[p.pos]
}

func (p *tableParser) next() token {
	tok := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

func (p *tableParser) eof() bool {
	return p.pos >= len(p.tokens) || p.tokens[p.pos].kind == tokEOF
}
