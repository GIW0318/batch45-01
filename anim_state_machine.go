package task

import (
	"errors"
	"sync"
)

// maxQueueLen caps the event and input queues; overflow drops the oldest entry.
const maxQueueLen = 64

var errUnknownState = errors.New("unknown state")

// Transition is one edge of the state graph.
type Transition struct {
	From         string
	To           string
	Condition    string
	BlendSeconds float64
}

type animEvent struct {
	at   float64
	name string
}

type bufferedInput struct {
	cond  string
	frame int
	ttl   int
}

// AnimStateMachine drives animation states and their blends.
type AnimStateMachine struct {
	mu sync.Mutex

	states      map[string]float64
	transitions []Transition

	current      string
	blendFrom    string
	blending     bool
	progress     float64
	blendSeconds float64

	transitionCount int
	clipTime        float64

	events []animEvent

	interruptStart float64
	interruptEnd   float64

	inputs []bufferedInput
	frame  int

	rootMotion [2]float64
	colliders  map[string][2]float64
	emitted    map[string]struct{}
}

// NewAnimStateMachine creates an empty state machine.
func NewAnimStateMachine() *AnimStateMachine {
	return &AnimStateMachine{
		states:    make(map[string]float64),
		colliders: make(map[string][2]float64),
		emitted:   make(map[string]struct{}),
	}
}

// AddState registers a state with its authored clip duration.
func (s *AnimStateMachine) AddState(name string, duration float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[name] = duration
	return nil
}

// AddTransition registers a condition triggered edge between two states.
func (s *AnimStateMachine) AddTransition(from, to, condition string, blendSeconds float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.states[from]; !ok {
		return errUnknownState
	}
	if _, ok := s.states[to]; !ok {
		return errUnknownState
	}
	s.transitions = append(s.transitions, Transition{
		From:         from,
		To:           to,
		Condition:    condition,
		BlendSeconds: blendSeconds,
	})
	return nil
}

// SetState switches immediately to the given state.
func (s *AnimStateMachine) SetState(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.states[name]; !ok {
		return errUnknownState
	}
	s.current = name
	s.blendFrom = ""
	s.blending = false
	s.progress = 1
	s.rootMotion = [2]float64{}
	return nil
}

// CurrentState returns the active state name.
func (s *AnimStateMachine) CurrentState() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// TransitionProgress returns the blend progress in [0, 1] of the running transition.
func (s *AnimStateMachine) TransitionProgress() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.progress
}

// Update advances the running transition using dt scaled by animSpeed.
func (s *AnimStateMachine) Update(dt, animSpeed float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.blending {
		return nil
	}
	if s.blendSeconds <= 0 {
		s.progress = 1
	} else {
		s.progress += dt * animSpeed / s.blendSeconds
		if s.progress > 1 {
			s.progress = 1
		}
	}
	if s.progress >= 1 {
		s.blending = false
		s.blendFrom = ""
	}
	return nil
}

// Fire evaluates all transitions leaving the current state for the condition.
func (s *AnimStateMachine) Fire(condition string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, tr := range s.transitions {
		if tr.From != s.current || tr.Condition != condition {
			continue
		}
		s.blendFrom = s.current
		s.current = tr.To
		s.blending = true
		s.progress = 0
		s.blendSeconds = tr.BlendSeconds
		s.transitionCount++
		s.rootMotion = [2]float64{}
		return nil
	}
	return nil
}

// TransitionCount returns how many state changes happened so far.
func (s *AnimStateMachine) TransitionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transitionCount
}

// SelectCondition picks one condition deterministically when several are valid.
func (s *AnimStateMachine) SelectCondition(cands []string) (string, error) {
	if len(cands) == 0 {
		return "", errors.New("no conditions to select from")
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if c < best {
			best = c
		}
	}
	return best, nil
}

// AdvanceAnim advances the clip clock and returns the delta in animation time.
func (s *AnimStateMachine) AdvanceAnim(dt, animScale float64) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	delta := dt * animScale
	s.clipTime += delta
	return delta
}

// AddEvent schedules an animation event at the given clip time.
func (s *AnimStateMachine) AddEvent(at float64, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pushEvent(animEvent{at: at, name: name})
	return nil
}

func (s *AnimStateMachine) pushEvent(ev animEvent) {
	if len(s.events) >= maxQueueLen {
		copy(s.events, s.events[1:])
		s.events = s.events[:maxQueueLen-1]
	}
	s.events = append(s.events, ev)
}

// DrainEvents returns all pending animation events and clears the queue.
func (s *AnimStateMachine) DrainEvents() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, len(s.events))
	for i, ev := range s.events {
		names[i] = ev.name
	}
	s.events = nil
	return names
}

// SetInterruptWindow marks the clip window in which a new input may cancel the state.
func (s *AnimStateMachine) SetInterruptWindow(start, end float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interruptStart = start
	s.interruptEnd = end
	return nil
}

// InInterruptWindow reports whether clip time t allows an interrupt.
func (s *AnimStateMachine) InInterruptWindow(t float64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return t >= s.interruptStart && t <= s.interruptEnd
}

// BufferInput stores a combo input for at most ttlFrames frames.
func (s *AnimStateMachine) BufferInput(cond string, ttlFrames int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.inputs) >= maxQueueLen {
		copy(s.inputs, s.inputs[1:])
		s.inputs = s.inputs[:maxQueueLen-1]
	}
	s.inputs = append(s.inputs, bufferedInput{cond: cond, frame: s.frame, ttl: ttlFrames})
}

// ConsumeBuffer returns a buffered input that is still valid at frame.
func (s *AnimStateMachine) ConsumeBuffer(frame int) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if frame > s.frame {
		s.frame = frame
	}
	for len(s.inputs) > 0 {
		head := s.inputs[0]
		s.inputs = s.inputs[1:]
		if frame-head.frame < head.ttl {
			return head.cond, true
		}
	}
	return "", false
}

// NormalizeLayerWeights scales layer weights so that they sum to 1.
func (s *AnimStateMachine) NormalizeLayerWeights(w []float32) []float32 {
	out := make([]float32, len(w))
	var sum float32
	for _, v := range w {
		sum += v
	}
	if sum <= 0 {
		if len(w) == 0 {
			return out
		}
		uniform := float32(1) / float32(len(w))
		for i := range out {
			out[i] = uniform
		}
		return out
	}
	for i, v := range w {
		out[i] = v / sum
	}
	return out
}

// AddRootMotion accumulates root motion for the current frame.
func (s *AnimStateMachine) AddRootMotion(x, y float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rootMotion[0] += x
	s.rootMotion[1] += y
}

// ConsumeRootMotion returns the accumulated root motion and clears it.
func (s *AnimStateMachine) ConsumeRootMotion() [2]float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	motion := s.rootMotion
	s.rootMotion = [2]float64{}
	return motion
}

// SetCollider sets the collider half extents authored for a state.
func (s *AnimStateMachine) SetCollider(state string, w, h float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.colliders[state] = [2]float64{w, h}
	return nil
}

// ColliderAt returns the collider blended at the given transition progress.
func (s *AnimStateMachine) ColliderAt(progress float64) (float64, float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	from := s.blendFrom
	if from == "" {
		from = s.current
	}
	fromCol, fromOK := s.colliders[from]
	toCol, toOK := s.colliders[s.current]
	switch {
	case fromOK && toOK:
		return fromCol[0] + (toCol[0]-fromCol[0])*progress,
			fromCol[1] + (toCol[1]-fromCol[1])*progress
	case toOK:
		return toCol[0], toCol[1]
	case fromOK:
		return fromCol[0], fromCol[1]
	}
	return 0, 0
}

// Restart re-initialises playback without dropping the current state.
func (s *AnimStateMachine) Restart() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clipTime = 0
	s.blending = false
	s.blendFrom = ""
	s.progress = 1
	s.rootMotion = [2]float64{}
	return nil
}

// EmitOnce emits an event once per key and reports whether it was emitted.
func (s *AnimStateMachine) EmitOnce(key, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.emitted[key]; ok {
		return false
	}
	s.emitted[key] = struct{}{}
	s.pushEvent(animEvent{name: name})
	return true
}
