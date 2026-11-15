// Package delay keeps the compressed stream in memory and replays it with a delay that can change
// while the output stays connected. Every jump lands on a video keyframe.
package delay

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/rechedev9/cliphub/clipdelay/internal/flv"
)

// ErrClosed is returned by Push after Close.
var ErrClosed = errors.New("delay: buffer closed")

// Options bound what the buffer keeps.
type Options struct {
	// Retain is how much history to keep behind the newest frame; it caps the largest delay.
	Retain time.Duration
	// MaxBytes is a hard cap on payload bytes; it wins over Retain and over the reader floor.
	MaxBytes int64
}

// Buffer is an append-only window of frames addressed by a monotonic sequence number.
type Buffer struct {
	mu       sync.Mutex
	opts     Options
	frames   []flv.Frame
	first    uint64   // sequence of frames[0]
	keys     []uint64 // sequences of video keyframes still in frames, ascending
	bytes    int64
	floor    uint64 // the reader still needs frames at or after this sequence
	wake     chan struct{}
	closed   bool
	meta     *flv.Frame
	videoHdr *flv.Frame
	audioHdr *flv.Frame
}

func NewBuffer(opts Options) *Buffer {
	return &Buffer{opts: opts, wake: make(chan struct{})}
}

// Push appends a frame and evicts what the limits no longer allow.
func (b *Buffer) Push(f flv.Frame) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrClosed
	}
	seq := b.first + uint64(len(b.frames))
	b.frames = append(b.frames, f)
	b.bytes += int64(len(f.Data))
	if f.Is(flv.FlagVideo) && f.Is(flv.FlagKey) {
		b.keys = append(b.keys, seq)
	}
	b.rememberHeader(f)
	b.evict()
	close(b.wake)
	b.wake = make(chan struct{})
	return nil
}

func (b *Buffer) rememberHeader(f flv.Frame) {
	if !f.Is(flv.FlagHeader) {
		return
	}
	c := f
	switch {
	case f.Is(flv.FlagMeta):
		b.meta = &c
	case f.Is(flv.FlagVideo):
		b.videoHdr = &c
	case f.Is(flv.FlagAudio):
		b.audioHdr = &c
	}
}

func (b *Buffer) evict() {
	newest := b.frames[len(b.frames)-1].Recv
	drop := 0
	for drop < len(b.frames)-1 {
		f := b.frames[drop]
		overBytes := b.opts.MaxBytes > 0 && b.bytes > b.opts.MaxBytes
		tooOld := newest-f.Recv > int64(b.opts.Retain)
		if !overBytes && (!tooOld || b.first+uint64(drop) >= b.floor) {
			break
		}
		b.bytes -= int64(len(f.Data))
		drop++
	}
	if drop == 0 {
		return
	}
	clear(b.frames[:drop])
	b.frames = b.frames[drop:]
	b.first += uint64(drop)
	k := sort.Search(len(b.keys), func(i int) bool { return b.keys[i] >= b.first })
	b.keys = b.keys[k:]
}

// Get returns the frame at seq while it is still buffered.
func (b *Buffer) Get(seq uint64) (flv.Frame, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if seq < b.first || seq >= b.first+uint64(len(b.frames)) {
		return flv.Frame{}, false
	}
	return b.frames[seq-b.first], true
}

// Bounds returns the first buffered sequence and the sequence the next Push gets.
func (b *Buffer) Bounds() (first, next uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.first, b.first + uint64(len(b.frames))
}

// KeyAtOrBefore returns the newest keyframe received at or before recv.
func (b *Buffer) KeyAtOrBefore(recv int64) (uint64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	i := sort.Search(len(b.keys), func(i int) bool { return b.frames[b.keys[i]-b.first].Recv > recv })
	if i == 0 {
		return 0, false
	}
	return b.keys[i-1], true
}

// OldestKey returns the oldest buffered keyframe.
func (b *Buffer) OldestKey() (uint64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.keys) == 0 {
		return 0, false
	}
	return b.keys[0], true
}

// headersBefore returns the metadata and sequence headers in effect at seq, in the order an output
// needs them: the newest of each kind buffered before seq, else the newest the buffer has seen.
func (b *Buffer) headersBefore(seq uint64) []flv.Frame {
	b.mu.Lock()
	defer b.mu.Unlock()
	found := map[uint8]flv.Frame{}
	end := 0
	if seq > b.first {
		end = int(min(seq-b.first, uint64(len(b.frames))))
	}
	for i := end - 1; i >= 0 && len(found) < 3; i-- {
		f := b.frames[i]
		if _, ok := found[flv.HeaderKind(f)]; f.Is(flv.FlagHeader) && !ok {
			found[flv.HeaderKind(f)] = f
		}
	}
	var out []flv.Frame
	for _, latest := range []*flv.Frame{b.meta, b.videoHdr, b.audioHdr} {
		if latest == nil {
			continue
		}
		if f, ok := found[flv.HeaderKind(*latest)]; ok {
			out = append(out, f)
		} else {
			out = append(out, *latest)
		}
	}
	return out
}

// SetFloor tells the buffer the reader still needs frames from seq on; only MaxBytes can evict them.
func (b *Buffer) SetFloor(seq uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.floor = seq
	if len(b.frames) > 0 {
		b.evict()
	}
}

// Wake returns a channel that closes on the next Push or on Close.
func (b *Buffer) Wake() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.wake
}

type Stats struct {
	Bytes   int64
	Covered time.Duration
}

func (b *Buffer) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := Stats{Bytes: b.bytes}
	if n := len(b.frames); n > 0 {
		s.Covered = time.Duration(b.frames[n-1].Recv - b.frames[0].Recv)
	}
	return s
}

// Close wakes every waiter; later pushes fail.
func (b *Buffer) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	close(b.wake)
}

func (b *Buffer) isClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}
