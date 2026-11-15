package relay

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	rtmp "github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"

	"github.com/rechedev9/cliphub/clipdelay/internal/flv"
)

const (
	frameEvery = 40 * time.Millisecond
	gopFrames  = 5
)

type received struct {
	typ  uint8
	ts   uint32
	data []byte
	at   time.Time
}

// platform is a fake ingest that records what the relay publishes.
type platform struct {
	rtmp.DefaultHandler
	mu     sync.Mutex
	key    string
	got    []received
	closed chan struct{}
}

func (p *platform) OnPublish(_ *rtmp.StreamContext, _ uint32, cmd *rtmpmsg.NetStreamPublish) error {
	p.mu.Lock()
	p.key = cmd.PublishingName
	p.mu.Unlock()
	return nil
}

// record fills in the payload and arrival time of rec.
func (p *platform) record(rec received, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	rec.data, rec.at = data, time.Now()
	p.mu.Lock()
	p.got = append(p.got, rec)
	p.mu.Unlock()
	return nil
}

func (p *platform) OnSetDataFrame(ts uint32, d *rtmpmsg.NetStreamSetDataFrame) error {
	return p.record(received{typ: flv.TypeScript, ts: ts}, bytes.NewReader(d.Payload))
}
func (p *platform) OnAudio(ts uint32, r io.Reader) error {
	return p.record(received{typ: flv.TypeAudio, ts: ts}, r)
}
func (p *platform) OnVideo(ts uint32, r io.Reader) error {
	return p.record(received{typ: flv.TypeVideo, ts: ts}, r)
}
func (p *platform) OnClose() { close(p.closed) }

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

func startPlatform(t *testing.T) (*platform, string) {
	p := &platform{closed: make(chan struct{})}
	ln := listen(t)
	srv := rtmp.NewServer(&rtmp.ServerConfig{OnConnect: func(c net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
		return c, &rtmp.ConnConfig{Handler: p}
	}})
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return p, "rtmp://" + ln.Addr().String() + "/app/streamkey"
}

// obs publishes a synthetic stream: a video frame every 40 ms tagged with its index, a keyframe
// every 5 frames, and an audio frame between each pair. It records when each index was sent.
type obs struct {
	stream *rtmp.Stream
	client *rtmp.ClientConn
	sentAt []time.Time
}

func connectOBS(t *testing.T, addr string) *obs {
	t.Helper()
	client, err := rtmp.Dial("rtmp", addr, &rtmp.ConnConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(&rtmpmsg.NetConnectionConnect{Command: rtmpmsg.NetConnectionConnectCommand{App: "live", TCURL: "rtmp://" + addr + "/live"}}); err != nil {
		t.Fatal(err)
	}
	stream, err := client.CreateStream(nil, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Publish(&rtmpmsg.NetStreamPublish{PublishingName: "obs", PublishingType: "live"}); err != nil {
		t.Fatal(err)
	}
	o := &obs{stream: stream, client: client}
	must(t, stream.WriteDataMessage(csidData, 0, "@setDataFrame", &rtmpmsg.NetStreamSetDataFrame{AmfData: map[string]any{"width": 1280.0}}))
	must(t, stream.Write(csidVideo, 0, &rtmpmsg.VideoMessage{Payload: bytes.NewReader([]byte{0x17, 0, 0, 0, 0, 1, 0x64})}))
	must(t, stream.Write(csidAudio, 0, &rtmpmsg.AudioMessage{Payload: bytes.NewReader([]byte{0xAF, 0, 0x12, 0x10})}))
	return o
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (o *obs) send(i int) error {
	ts := uint32(i * int(frameEvery/time.Millisecond))
	b0 := byte(0x27)
	if i%gopFrames == 0 {
		b0 = 0x17
	}
	v := []byte{b0, 1, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(v[5:], uint16(i))
	o.sentAt = append(o.sentAt, time.Now())
	if err := o.stream.Write(csidVideo, ts, &rtmpmsg.VideoMessage{Payload: bytes.NewReader(v)}); err != nil {
		return err
	}
	a := []byte{0xAF, 1, 0, 0}
	binary.BigEndian.PutUint16(a[2:], uint16(i))
	return o.stream.Write(csidAudio, ts+20, &rtmpmsg.AudioMessage{Payload: bytes.NewReader(a)})
}

func videoIndex(r received) int { return int(binary.BigEndian.Uint16(r.data[5:7])) }

func TestRelayChangesTheDelayOnKeyframesWithoutDroppingTheUpstream(t *testing.T) {
	plat, upURL := startPlatform(t)
	rl, err := New(Config{Upstream: upURL, Retain: 10 * time.Second, MaxBytes: 1 << 24, Logf: t.Logf})
	must(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln := listen(t)
	served := make(chan error, 1)
	go func() { served <- rl.Serve(ctx, ln) }()

	o := connectOBS(t, ln.Addr().String())
	tick := time.NewTicker(frameEvery)
	for i := range 75 { // 3 s of input
		switch i {
		case 20:
			must(t, rl.SetDelay(600*time.Millisecond))
		case 50:
			must(t, rl.SetDelay(0))
		}
		must(t, o.send(i))
		<-tick.C
	}
	tick.Stop()
	_ = o.client.Close()

	select {
	case <-plat.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream was not closed after the output drained")
	}
	if st := rl.Status(); st.Buffer.Bytes != 0 {
		t.Fatalf("an idle relay still buffers %d bytes", st.Buffer.Bytes)
	}
	cancel()
	must(t, <-served)

	plat.mu.Lock()
	defer plat.mu.Unlock()
	if plat.key != "streamkey" {
		t.Fatalf("published as %q, want the upstream stream key", plat.key)
	}
	platformRun{got: plat.got, sentAt: o.sentAt}.check(t)
}

// platformRun is what the platform received and when the fake OBS sent each video index.
type platformRun struct {
	got    []received
	sentAt []time.Time
}

func (run platformRun) check(t *testing.T) {
	t.Helper()
	got, sentAt := run.got, run.sentAt
	if len(got) < 3 || got[0].typ != flv.TypeScript || got[1].typ != flv.TypeVideo || got[1].data[1] != 0 || got[2].typ != flv.TypeAudio || got[2].data[1] != 0 {
		t.Fatalf("the stream did not open with metadata and both sequence headers: %v", got[:min(3, len(got))])
	}
	var lastV, lastA uint32
	prev, back, ahead := -1, 0, 0
	var delays []time.Duration
	for _, r := range got[3:] {
		switch r.typ {
		case flv.TypeAudio:
			if r.ts < lastA {
				t.Fatalf("audio timestamp went back: %d after %d", r.ts, lastA)
			}
			lastA = r.ts
		case flv.TypeVideo:
			if r.ts < lastV {
				t.Fatalf("video timestamp went back: %d after %d", r.ts, lastV)
			}
			lastV = r.ts
			i := videoIndex(r)
			if prev >= 0 && i != prev+1 {
				if r.data[0] != 0x17 {
					t.Fatalf("jump from frame %d to %d landed on a dependent frame", prev, i)
				}
				if i <= prev {
					back++
				} else {
					ahead++
				}
			}
			if i >= 25 && i < 35 { // sent after the replay caught up, out before the return to live
				delays = append(delays, r.at.Sub(sentAt[i]))
			}
			prev = i
		}
	}
	if back == 0 || ahead == 0 {
		t.Fatalf("saw %d jumps back and %d ahead, want at least one of each", back, ahead)
	}
	for _, d := range delays {
		if d < 450*time.Millisecond || d > 800*time.Millisecond {
			t.Fatalf("frames sent while delayed arrived %s late, want about 600 ms", d)
		}
	}
	if len(delays) == 0 {
		t.Fatal("no frame from the delayed stretch reached the platform")
	}
}
