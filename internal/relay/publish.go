package relay

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	rtmp "github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"

	"github.com/rechedev9/clipdelay/internal/delay"
	"github.com/rechedev9/clipdelay/internal/flv"
	"github.com/rechedev9/clipdelay/internal/timeline"
)

// Chunk stream IDs as OBS uses them for each kind of message.
const (
	csidData  = 5
	csidAudio = 4
	csidVideo = 6
	chunkSize = 4096

	dialTimeout      = 10 * time.Second
	handshakeTimeout = 15 * time.Second
	minBackoff       = time.Second
	maxBackoff       = 15 * time.Second
	stableSession    = 30 * time.Second
)

const (
	StateConnecting = "connecting"
	StateLive       = "live"
	StateRetrying   = "retrying"
)

// PublisherStatus reports the platform connection.
type PublisherStatus struct {
	State      string
	Reconnects int
	LastError  string
	BytesOut   int64
}

// publisher keeps one connection to the platform and feeds it from a delay reader.
type publisher struct {
	relay *Relay
	up    upstream

	mu sync.Mutex
	st PublisherStatus
}

func newPublisher(r *Relay, up upstream) *publisher {
	return &publisher{relay: r, up: up, st: PublisherStatus{State: StateConnecting}}
}

func (p *publisher) status() PublisherStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.st
}

// redact keeps a library error from echoing the stream key into logs or status.
func (p *publisher) redact(err error) string {
	return strings.ReplaceAll(err.Error(), p.up.key, "****")
}

func (p *publisher) setState(state string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.st.State = state
	if err != nil {
		p.st.LastError = p.redact(err)
	}
	if state == StateRetrying {
		p.st.Reconnects++
	}
}

// run publishes until the output drained after OBS stopped, or ctx ends. A dropped connection
// reconnects with backoff and resumes at a keyframe.
func (p *publisher) run(ctx context.Context) {
	backoff := minBackoff
	for ctx.Err() == nil {
		began := time.Now()
		err := p.session(ctx)
		if err == nil || ctx.Err() != nil {
			return
		}
		if time.Since(began) > stableSession {
			backoff = minBackoff
		}
		p.relay.cfg.Logf("upstream %s: %s", p.up, p.redact(err))
		p.setState(StateRetrying, err)
		if p.relay.finishIfDrained(p) {
			return // OBS is gone and the platform is unreachable: drop what is left
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// session runs one platform connection. It returns nil once the output drained.
func (p *publisher) session(ctx context.Context) error {
	client, stream, err := p.connect(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()
	p.setState(StateLive, nil)
	p.relay.cfg.Logf("upstream %s connected", p.up)
	out := &output{pub: p, stream: stream, tl: timeline.New(), sent: map[uint8][]byte{}}
	if err := out.pump(ctx, p.relay.newReader()); err != nil {
		return err
	}
	// Drained: end the stream the way an encoder does, so the platform does not see a dropped link.
	_ = client.DeleteStream(&rtmpmsg.NetStreamDeleteStream{StreamID: stream.StreamID()})
	return nil
}

type connected struct {
	client *rtmp.ClientConn
	stream *rtmp.Stream
	err    error
}

// connect dials and publishes within handshakeTimeout. go-rtmp waits for server replies with no
// deadline, so a platform that accepts TCP and then goes silent would otherwise block shutdown.
func (p *publisher) connect(ctx context.Context) (*rtmp.ClientConn, *rtmp.Stream, error) {
	done := make(chan connected, 1)
	go func() {
		client, err := p.dial()
		if err != nil {
			done <- connected{err: err}
			return
		}
		stream, err := p.open(client)
		done <- connected{client: client, stream: stream, err: err}
	}()
	select {
	case c := <-done:
		if c.err != nil && c.client != nil {
			_ = c.client.Close()
		}
		return c.client, c.stream, c.err
	case <-ctx.Done():
	case <-time.After(handshakeTimeout):
	}
	// The handshake goroutine may still finish; close what it opens then.
	go func() {
		if c := <-done; c.client != nil {
			_ = c.client.Close()
		}
	}()
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	return nil, nil, errors.New("upstream did not answer the RTMP handshake")
}

func (p *publisher) dial() (*rtmp.ClientConn, error) {
	cfg := &rtmp.ConnConfig{}
	d := &net.Dialer{Timeout: dialTimeout}
	if p.up.scheme == "rtmps" {
		host, _, _ := net.SplitHostPort(p.up.addr)
		return rtmp.DialWithTLSDialer(&tls.Dialer{NetDialer: d, Config: &tls.Config{ServerName: host}}, "rtmps", p.up.addr, cfg)
	}
	return rtmp.DialWithDialer(d, "rtmp", p.up.addr, cfg)
}

func (p *publisher) open(client *rtmp.ClientConn) (*rtmp.Stream, error) {
	if err := client.Connect(&rtmpmsg.NetConnectionConnect{Command: rtmpmsg.NetConnectionConnectCommand{
		App:      p.up.app,
		Type:     "nonprivate",
		FlashVer: "FMLE/3.0 (compatible; FMSc/1.0)",
		TCURL:    p.up.tcURL,
	}}); err != nil {
		return nil, err
	}
	stream, err := client.CreateStream(nil, chunkSize)
	if err != nil {
		return nil, err
	}
	if err := stream.Publish(&rtmpmsg.NetStreamPublish{PublishingName: p.up.key, PublishingType: "live"}); err != nil {
		return nil, err
	}
	return stream, nil
}

// output is the write side of one platform connection. The reader hands it the headers in effect
// before every start and jump; a header it already sent is skipped.
type output struct {
	pub    *publisher
	stream *rtmp.Stream
	tl     *timeline.Mapper
	sent   map[uint8][]byte
}

// pump writes frames as the reader releases them, until the output drained or ctx ends.
func (o *output) pump(ctx context.Context, rd *delay.Reader) error {
	r := o.pub.relay
	buf := r.buffer()
	// One timer for the whole session: the pump waits between almost every frame.
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for {
		wake, changed, ended := buf.Wake(), rd.Changed(), r.ingestEndedCh()
		step := rd.Next(r.now())
		switch {
		case step.OK:
			if err := o.forward(step); err != nil {
				return err
			}
			continue
		case step.EOF:
			return nil
		case step.Wait == 0 && r.finishIfDrained(o.pub):
			return nil
		}
		var due <-chan time.Time
		if step.Wait > 0 {
			timer.Reset(step.Wait)
			due = timer.C
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		case <-changed:
		case <-ended:
		case <-due:
		}
		timer.Stop()
	}
}

func (o *output) forward(step delay.Step) error {
	f := step.Frame
	if f.Is(flv.FlagHeader) {
		kind := flv.HeaderKind(f)
		if bytes.Equal(o.sent[kind], f.Data) {
			return nil
		}
		o.sent[kind] = f.Data
	}
	ts := uint32(0)
	if !f.Is(flv.FlagMeta) {
		s := timeline.Sample{Time: f.Time, Video: f.Is(flv.FlagVideo), Disc: step.Discontinuity}
		if s.Video {
			s.CTS = flv.CompositionTime(f.Data)
		}
		ts = o.tl.Map(s)
	}
	return o.write(f, ts)
}

func (o *output) write(f flv.Frame, ts uint32) error {
	var msg rtmpmsg.Message
	csid := csidVideo
	switch f.Type {
	case flv.TypeVideo:
		msg = &rtmpmsg.VideoMessage{Payload: bytes.NewReader(f.Data)}
	case flv.TypeAudio:
		msg, csid = &rtmpmsg.AudioMessage{Payload: bytes.NewReader(f.Data)}, csidAudio
	case flv.TypeScript:
		// The payload is the AMF after "@setDataFrame": "onMetaData" and its object.
		msg = &rtmpmsg.DataMessage{Name: "@setDataFrame", Encoding: rtmpmsg.EncodingTypeAMF0, Body: bytes.NewReader(f.Data)}
		csid = csidData
	default:
		return nil
	}
	if err := o.stream.Write(csid, ts, msg); err != nil {
		return err
	}
	o.pub.mu.Lock()
	o.pub.st.BytesOut += int64(len(f.Data))
	o.pub.mu.Unlock()
	return nil
}
