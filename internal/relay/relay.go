// Package relay is the ClipDelay engine: OBS publishes to a local RTMP server, the stream waits in a
// delay buffer, and one publisher forwards it to the platform without re-encoding.
package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	rtmp "github.com/yutopp/go-rtmp"

	"github.com/rechedev9/clipdelay/internal/delay"
)

// MaxDelay is the largest delay a viewer can be behind.
const MaxDelay = 600 * time.Second

// Config sets up a relay for one OBS and one platform.
type Config struct {
	// Upstream is the platform ingest with the stream key: rtmp(s)://host/app/streamkey.
	Upstream string
	// IngestKey, when set, is the stream key OBS must publish with.
	IngestKey string
	Delay     time.Duration
	// Retain is how much history the buffer keeps; it caps how far a raise can jump back.
	Retain   time.Duration
	MaxBytes int64
	Logf     func(format string, args ...any)
}

// Status is a snapshot for a control surface.
type Status struct {
	Ingesting bool
	Reader    delay.Status
	Buffer    delay.Stats
	Upstream  PublisherStatus
}

// Relay owns the buffer, the ingest server and the publisher.
type Relay struct {
	cfg   Config
	up    upstream
	start time.Time

	mu        sync.Mutex
	buf       *delay.Buffer
	reader    *delay.Reader
	target    time.Duration
	ingesting bool
	// ingestEnded closes when OBS disconnects, so a publisher at the live edge can drain and stop.
	ingestEnded chan struct{}
	pub         *publisher
	conns       map[net.Conn]struct{}
	ctx         context.Context
	wg          sync.WaitGroup
}

func New(cfg Config) (*Relay, error) {
	up, err := parseUpstream(cfg.Upstream)
	if err != nil {
		return nil, err
	}
	if err := checkDelay(cfg.Delay); err != nil {
		return nil, err
	}
	if cfg.Retain < cfg.Delay {
		return nil, errors.New("relay: retain must cover the delay")
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	return &Relay{cfg: cfg, up: up, start: time.Now(), target: cfg.Delay, ingestEnded: make(chan struct{}), conns: map[net.Conn]struct{}{}}, nil
}

func checkDelay(d time.Duration) error {
	if d < 0 || d > MaxDelay {
		return fmt.Errorf("relay: delay must be between 0 and %s", MaxDelay)
	}
	return nil
}

// now is the relay's monotonic clock in nanoseconds; frames and the reader share it.
func (r *Relay) now() int64 { return int64(time.Since(r.start)) }

// Serve accepts OBS on ln until ctx ends, then closes every connection and waits for the publisher.
func (r *Relay) Serve(ctx context.Context, ln net.Listener) error {
	r.mu.Lock()
	r.ctx = ctx
	r.mu.Unlock()
	srv := rtmp.NewServer(&rtmp.ServerConfig{OnConnect: r.onConnect})
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, rtmp.ErrClosed) {
			r.shutdown()
			return err
		}
	}
	_ = srv.Close()
	r.shutdown()
	return nil
}

func (r *Relay) shutdown() {
	r.mu.Lock()
	for c := range r.conns {
		_ = c.Close()
	}
	if r.buf != nil {
		r.buf.Close()
	}
	r.mu.Unlock()
	r.wg.Wait()
}

// SetDelay changes the delay viewers get; it lands on the next keyframe that allows it.
func (r *Relay) SetDelay(d time.Duration) error {
	if err := checkDelay(d); err != nil {
		return err
	}
	if d > r.cfg.Retain {
		return fmt.Errorf("relay: delay %s is longer than the %s the buffer keeps", d, r.cfg.Retain)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.target = d
	if r.reader != nil {
		r.reader.SetTarget(d)
	}
	return nil
}

// EndMatch goes live once viewers have seen everything up to now, so they never miss the end.
func (r *Relay) EndMatch() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.target = 0
	if r.reader != nil {
		r.reader.LiveWhenCaughtUp(r.now())
	}
}

func (r *Relay) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := Status{Ingesting: r.ingesting, Reader: delay.Status{Phase: delay.PhaseFilling, Target: r.target}}
	if r.reader != nil {
		s.Reader = r.reader.Status(r.now())
	}
	if r.buf != nil {
		s.Buffer = r.buf.Stats()
	}
	if r.pub != nil {
		s.Upstream = r.pub.status()
	}
	return s
}

func (r *Relay) onConnect(c net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
	r.mu.Lock()
	r.conns[c] = struct{}{}
	r.mu.Unlock()
	h := &ingestHandler{relay: r, conn: c}
	return c, &rtmp.ConnConfig{
		Handler:          h,
		ReaderBufferSize: 64 * 1024,
		ControlState:     rtmp.StreamControlStateConfig{DefaultBandwidthWindowSize: 6 * 1024 * 1024 / 8},
	}
}

// beginIngest claims the single ingest slot. A new session after the previous one drained starts
// a fresh buffer; one that arrives while delayed frames are still going out joins the same buffer.
func (r *Relay) beginIngest(key string) (*delay.Buffer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cfg.IngestKey != "" && key != r.cfg.IngestKey {
		return nil, errors.New("relay: wrong ingest key")
	}
	if r.ingesting {
		return nil, errors.New("relay: another encoder is already publishing")
	}
	if r.ctx == nil || r.ctx.Err() != nil {
		return nil, errors.New("relay: not serving")
	}
	r.ingesting = true
	if r.pub == nil {
		r.buf = delay.NewBuffer(delay.Options{Retain: r.cfg.Retain, MaxBytes: r.cfg.MaxBytes})
		r.pub = newPublisher(r, r.up)
		r.wg.Add(1)
		go func(p *publisher, ctx context.Context) {
			defer r.wg.Done()
			p.run(ctx)
		}(r.pub, r.ctx)
	}
	r.cfg.Logf("ingest started, forwarding to %s", r.up)
	return r.buf, nil
}

func (r *Relay) endIngest(c net.Conn, claimed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.conns, c)
	if claimed {
		r.ingesting = false
		close(r.ingestEnded)
		r.ingestEnded = make(chan struct{})
		r.cfg.Logf("ingest stopped")
	}
}

// newReader gives the publisher a reader that starts at the current target. A reconnecting
// publisher gets a fresh one, so the new connection starts on a keyframe.
func (r *Relay) newReader() *delay.Reader {
	r.mu.Lock()
	defer r.mu.Unlock()
	rd := delay.NewReader(r.buf)
	rd.SetTarget(r.target)
	r.reader = rd
	return rd
}

// finishIfDrained ends the publisher when OBS is gone and every delayed frame went out.
func (r *Relay) finishIfDrained(p *publisher) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ingesting || r.pub != p {
		return false
	}
	// Release the buffer: an idle relay should not hold minutes of video until the next session.
	r.buf.Close()
	r.pub, r.reader, r.buf = nil, nil, nil
	r.cfg.Logf("delayed output drained, upstream closed")
	return true
}

func (r *Relay) ingestEndedCh() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ingestEnded
}

func (r *Relay) buffer() *delay.Buffer {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf
}
