package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// Transition is one edge of the state graph.
type Transition struct {
	From         string
	To           string
	Condition    string
	BlendSeconds float64
}

// AnimStateMachine drives animation states and their blends.
type AnimStateMachine struct{}

// NewAnimStateMachine creates an empty state machine.
func NewAnimStateMachine() *AnimStateMachine {
	return &AnimStateMachine{}
}

// AddState registers a state with its authored clip duration.
func (s *AnimStateMachine) AddState(name string, duration float64) error {
	return errNotImplemented
}

// AddTransition registers a condition triggered edge between two states.
func (s *AnimStateMachine) AddTransition(from, to, condition string, blendSeconds float64) error {
	return errNotImplemented
}

// SetState switches immediately to the given state.
func (s *AnimStateMachine) SetState(name string) error {
	return errNotImplemented
}

// CurrentState returns the active state name.
func (s *AnimStateMachine) CurrentState() string {
	return ""
}

// TransitionProgress returns the blend progress in [0, 1] of the running transition.
func (s *AnimStateMachine) TransitionProgress() float64 {
	return 0
}

// Update advances the running transition using dt scaled by animSpeed.
func (s *AnimStateMachine) Update(dt, animSpeed float64) error {
	return errNotImplemented
}

// Fire evaluates all transitions leaving the current state for the condition.
func (s *AnimStateMachine) Fire(condition string) error {
	return errNotImplemented
}

// TransitionCount returns how many state changes happened so far.
func (s *AnimStateMachine) TransitionCount() int {
	return 0
}

// SelectCondition picks one condition deterministically when several are valid.
func (s *AnimStateMachine) SelectCondition(cands []string) (string, error) {
	return "", errNotImplemented
}

// AdvanceAnim advances the clip clock and returns the delta in animation time.
func (s *AnimStateMachine) AdvanceAnim(dt, animScale float64) float64 {
	return 0
}

// AddEvent schedules an animation event at the given clip time.
func (s *AnimStateMachine) AddEvent(at float64, name string) error {
	return errNotImplemented
}

// DrainEvents returns all pending animation events and clears the queue.
func (s *AnimStateMachine) DrainEvents() []string {
	return nil
}

// SetInterruptWindow marks the clip window in which a new input may cancel the state.
func (s *AnimStateMachine) SetInterruptWindow(start, end float64) error {
	return errNotImplemented
}

// InInterruptWindow reports whether clip time t allows an interrupt.
func (s *AnimStateMachine) InInterruptWindow(t float64) bool {
	return false
}

// BufferInput stores a combo input for at most ttlFrames frames.
func (s *AnimStateMachine) BufferInput(cond string, ttlFrames int) {
}

// ConsumeBuffer returns a buffered input that is still valid at frame.
func (s *AnimStateMachine) ConsumeBuffer(frame int) (string, bool) {
	return "", false
}

// NormalizeLayerWeights scales layer weights so that they sum to 1.
func (s *AnimStateMachine) NormalizeLayerWeights(w []float32) []float32 {
	return nil
}

// AddRootMotion accumulates root motion for the current frame.
func (s *AnimStateMachine) AddRootMotion(x, y float64) {
}

// ConsumeRootMotion returns the accumulated root motion and clears it.
func (s *AnimStateMachine) ConsumeRootMotion() [2]float64 {
	return [2]float64{}
}

// SetCollider sets the collider half extents authored for a state.
func (s *AnimStateMachine) SetCollider(state string, w, h float64) error {
	return errNotImplemented
}

// ColliderAt returns the collider blended at the given transition progress.
func (s *AnimStateMachine) ColliderAt(progress float64) (float64, float64) {
	return 0, 0
}

// Restart re-initialises playback without dropping the current state.
func (s *AnimStateMachine) Restart() error {
	return errNotImplemented
}

// EmitOnce emits an event once per key and reports whether it was emitted.
func (s *AnimStateMachine) EmitOnce(key, name string) bool {
	return false
}
