package delay

import (
	"sync"
	"time"

	"github.com/rechedev9/cliphub/clipdelay/internal/flv"
)

// Step is one result of Reader.Next.
type Step struct {
	Frame flv.Frame
	OK    bool
	// Discontinuity is set on the first media frame after a jump or a new ingest session.
	Discontinuity bool
	// Wait is how long until the next frame is due; zero with OK false means wait for the buffer.
	Wait time.Duration
	EOF  bool
}

const (
	PhaseFilling  = "filling"
	PhaseLive     = "live"
	PhaseDelayed  = "delayed"
	PhaseChanging = "changing"
	// PhaseReturning keeps the delay until viewers have seen the moment live was asked for.
	PhaseReturning = "returning"
)

// Status is what a control surface shows: the delay asked for and the delay viewers get.
type Status struct {
	Phase   string
	Target  time.Duration
	Applied time.Duration
	// ReadyIn is how long until a pending raise has enough history.
	ReadyIn time.Duration
	// Lag is how far behind the receive clock the last emitted frame is.
	Lag   time.Duration
	Jumps int
}

// Reader emits buffered frames once they are as old as the applied delay.
// A frame received at Recv is due at Recv + applied.
type Reader struct {
	buf *Buffer

	mu      sync.Mutex
	cursor  uint64
	started bool
	emitted bool
	applied time.Duration
	target  time.Duration
	pending bool
	// disc carries a jump or session start to the next media frame; headers and metadata skip it.
	disc       bool
	dropping   bool
	dropBefore uint32
	// headers go out before the frame at cursor: the ones in effect where the reader started or jumped.
	headers  []flv.Frame
	lastRecv int64
	jumps    int
	changed  chan struct{}
	// returning goes live once a frame received at or after returnAt has been emitted.
	returning bool
	returnAt  int64
}

func NewReader(b *Buffer) *Reader {
	return &Reader{buf: b, changed: make(chan struct{})}
}

// SetTarget asks for a new delay. It applies on the next Next call, at a keyframe, once the
// buffer holds enough history; until then the current delay stays.
func (r *Reader) SetTarget(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.returning = false
	r.setTarget(d)
}

// LiveWhenCaughtUp goes live once viewers have seen everything received up to now, so ending a
// match never skips its last minutes. A later SetTarget cancels it.
func (r *Reader) LiveWhenCaughtUp(now int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.applied == 0 && !r.pending {
		r.setTarget(0)
		return
	}
	r.returning, r.returnAt = true, now
	close(r.changed)
	r.changed = make(chan struct{})
}

func (r *Reader) setTarget(d time.Duration) {
	r.target = max(d, 0)
	r.pending = true
	close(r.changed)
	r.changed = make(chan struct{})
}

// Changed closes on the next SetTarget.
func (r *Reader) Changed() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.changed
}

// Next returns the frame due at now (monotonic nanoseconds), or how long to wait.
func (r *Reader) Next(now int64) Step {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started || r.pending {
		r.apply(now)
	}
	if !r.started {
		return Step{EOF: r.buf.isClosed()}
	}
	if h, ok := r.popHeader(); ok {
		return h
	}
	for {
		f, ok := r.buf.Get(r.cursor)
		if !ok {
			if r.recover(now) {
				if h, ok := r.popHeader(); ok {
					return h
				}
				continue
			}
			return Step{EOF: r.buf.isClosed()}
		}
		if due := f.Recv + int64(r.applied); due > now {
			return Step{Wait: time.Duration(due - now)}
		}
		r.cursor++
		r.buf.SetFloor(r.cursor)
		if f.Is(flv.FlagSessionStart) {
			r.disc, r.dropping = true, false
		}
		if f.Is(flv.FlagHeader) {
			return Step{Frame: f, OK: true}
		}
		// Audio that came in after the keyframe but samples from before it would play ahead of the picture.
		if r.dropping && f.Is(flv.FlagAudio) && f.Time < r.dropBefore {
			continue
		}
		step := Step{Frame: f, OK: true, Discontinuity: r.disc && r.emitted}
		r.disc, r.emitted, r.lastRecv = false, true, f.Recv
		if r.returning && f.Recv >= r.returnAt {
			r.returning = false
			r.setTarget(0)
		}
		return step
	}
}

func (r *Reader) popHeader() (Step, bool) {
	if len(r.headers) == 0 {
		return Step{}, false
	}
	h := r.headers[0]
	r.headers = r.headers[1:]
	return Step{Frame: h, OK: true}, true
}

// apply moves the cursor for a new target. Raising the delay replays from an older keyframe;
// lowering it skips ahead to a newer one. Small changes inside one GOP only re-pace.
func (r *Reader) apply(now int64) {
	cand, ok := r.buf.KeyAtOrBefore(now - int64(r.target))
	if !ok {
		if r.emitted {
			return // not enough history yet: keep the current delay
		}
		if cand, ok = r.buf.OldestKey(); !ok {
			return
		}
	}
	switch {
	case !r.started:
		r.started = true
		r.moveTo(cand)
	case r.target > r.applied && cand < r.cursor, r.target < r.applied && cand > r.cursor:
		r.jump(cand)
	}
	r.applied, r.pending = r.target, false
}

// recover handles a cursor the buffer evicted because MaxBytes held less than the delay. It
// resumes at the oldest kept keyframe and lowers the applied delay to what the buffer can hold,
// so the output keeps flowing instead of chasing frames that are evicted before they are due.
func (r *Reader) recover(now int64) bool {
	first, _ := r.buf.Bounds()
	if r.cursor >= first {
		return false
	}
	cand, ok := r.buf.OldestKey()
	if !ok {
		return false
	}
	f, _ := r.buf.Get(cand)
	r.applied = max(time.Duration(now-f.Recv), 0)
	r.pending = false
	r.jump(cand)
	return true
}

func (r *Reader) jump(seq uint64) {
	r.jumps++
	r.disc = true
	r.moveTo(seq)
}

func (r *Reader) moveTo(seq uint64) {
	r.cursor = seq
	r.headers = r.buf.headersBefore(seq)
	if f, ok := r.buf.Get(seq); ok {
		r.dropping, r.dropBefore = true, f.Time
	}
}

func (r *Reader) Status(now int64) Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := Status{Target: r.target, Applied: r.applied, Jumps: r.jumps}
	if r.emitted {
		s.Lag = time.Duration(now - r.lastRecv)
	}
	switch {
	case !r.emitted:
		s.Phase = PhaseFilling
	case r.returning:
		s.Phase = PhaseReturning
		s.ReadyIn = max(time.Duration(r.returnAt+int64(r.applied)-now), 0)
	case r.pending:
		s.Phase = PhaseChanging
		if k, ok := r.buf.OldestKey(); ok {
			if f, ok := r.buf.Get(k); ok {
				s.ReadyIn = max(time.Duration(f.Recv+int64(r.target)-now), 0)
			}
		}
	case r.applied == 0:
		s.Phase = PhaseLive
	default:
		s.Phase = PhaseDelayed
	}
	return s
}
