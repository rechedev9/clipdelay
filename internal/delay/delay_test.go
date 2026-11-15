package delay

import (
	"testing"
	"time"

	"github.com/rechedev9/clipdelay/internal/flv"
)

const ms = int64(time.Millisecond)

// video builds a video frame received at recvMs with source time recvMs.
func video(recvMs int64, key bool) flv.Frame {
	b0 := byte(0x27)
	flags := flv.FlagVideo
	if key {
		b0 = 0x17
		flags |= flv.FlagKey
	}
	return flv.Frame{Type: flv.TypeVideo, Flags: flags, Time: uint32(recvMs), Recv: recvMs * ms, Data: []byte{b0, 1, byte(recvMs / 100)}}
}

func audio(recvMs int64, id byte) flv.Frame {
	return flv.Frame{Type: flv.TypeAudio, Flags: flv.FlagAudio, Time: uint32(recvMs), Recv: recvMs * ms, Data: []byte{0xAF, 1, id}}
}

// span is input to feed: one video frame every 100 ms from fromMs to toMs (exclusive).
type span struct {
	b            *Buffer
	fromMs, toMs int64
}

// feed pushes a span with a keyframe each second.
func feed(t *testing.T, s span) {
	t.Helper()
	for at := s.fromMs; at < s.toMs; at += 100 {
		if err := s.b.Push(video(at, at%1000 == 0)); err != nil {
			t.Fatalf("push: %v", err)
		}
	}
}

// drain reads every frame due at nowMs.
func drain(r *Reader, nowMs int64) []Step {
	var out []Step
	for {
		s := r.Next(nowMs * ms)
		if !s.OK {
			return out
		}
		out = append(out, s)
	}
}

func recvMs(s Step) int64 { return s.Frame.Recv / ms }

func TestZeroDelayStartsAtFirstKeyframe(t *testing.T) {
	b := NewBuffer(Options{Retain: time.Minute, MaxBytes: 1 << 20})
	_ = b.Push(video(0, false))
	_ = b.Push(video(100, false))
	feed(t, span{b, 1000, 1300})
	r := NewReader(b)

	got := drain(r, 1300)
	if len(got) != 3 {
		t.Fatalf("emitted %d frames, want 3", len(got))
	}
	if !got[0].Frame.Is(flv.FlagKey) || recvMs(got[0]) != 1000 {
		t.Fatalf("first frame = %+v, want the keyframe at 1000 ms", got[0].Frame)
	}
}

func TestRaisingDelayJumpsBackToAKeyframeAndPacesByTheTarget(t *testing.T) {
	b := NewBuffer(Options{Retain: time.Minute, MaxBytes: 1 << 20})
	feed(t, span{b, 0, 10_000})
	r := NewReader(b)
	drain(r, 10_000)

	r.SetTarget(5500 * time.Millisecond)
	got := drain(r, 10_000)
	// now - target = 4500 ms; the keyframe at or before it is 4000 ms, due at 9500 ms.
	if len(got) == 0 || recvMs(got[0]) != 4000 || !got[0].Discontinuity {
		t.Fatalf("first frame after the change = %+v, want the 4000 ms keyframe flagged as a jump", got)
	}
	if last := recvMs(got[len(got)-1]); last != 4500 {
		t.Fatalf("last frame due at 10 s = %d ms, want 4500 ms", last)
	}
	if s := r.Next(10_000 * ms); s.OK || s.Wait != 100*time.Millisecond {
		t.Fatalf("next step = %+v, want a 100 ms wait", s)
	}
	if st := r.Status(10_000 * ms); st.Applied != 5500*time.Millisecond || st.Jumps != 1 {
		t.Fatalf("status = %+v, want 5.5 s applied after one jump", st)
	}
}

func TestRaisingDelayPastTheHistoryWaitsWithoutStallingTheOutput(t *testing.T) {
	b := NewBuffer(Options{Retain: time.Minute, MaxBytes: 1 << 20})
	feed(t, span{b, 0, 3000})
	r := NewReader(b)
	drain(r, 3000)

	r.SetTarget(5 * time.Second)
	if got := drain(r, 3000); len(got) != 0 {
		t.Fatalf("emitted %d frames before new input, want 0", len(got))
	}
	feed(t, span{b, 3000, 3100})
	if got := drain(r, 3100); len(got) != 1 || recvMs(got[0]) != 3000 || got[0].Discontinuity {
		t.Fatalf("while pending = %+v, want the live frame at the old delay", got)
	}
	if st := r.Status(3100 * ms); st.Phase != PhaseChanging || st.ReadyIn != 1900*time.Millisecond {
		t.Fatalf("status = %+v, want changing and ready in 1.9 s", st)
	}

	feed(t, span{b, 3100, 5100})
	got := drain(r, 5100)
	// At 5.1 s the newest keyframe at or before 0.1 s is the one at 0 ms.
	if len(got) == 0 || recvMs(got[0]) != 0 || !got[0].Discontinuity {
		t.Fatalf("after the history filled = %+v, want a jump to the 0 ms keyframe", got)
	}
}

func TestReturningToLiveSkipsTheBacklog(t *testing.T) {
	b := NewBuffer(Options{Retain: time.Minute, MaxBytes: 1 << 20})
	r := NewReader(b)
	r.SetTarget(30 * time.Second)
	feed(t, span{b, 0, 40_000})
	drain(r, 40_000)

	r.SetTarget(0)
	got := drain(r, 40_000)
	if len(got) != 10 || recvMs(got[0]) != 39_000 || !got[0].Discontinuity {
		t.Fatalf("back to live emitted %d frames from %d ms, want the last GOP from 39000 ms", len(got), recvMs(got[0]))
	}
	if st := r.Status(40_000 * ms); st.Phase != PhaseLive || st.Applied != 0 {
		t.Fatalf("status = %+v, want live", st)
	}
}

func TestStartingDelayedEmitsNothingUntilTheDelayHasPassed(t *testing.T) {
	b := NewBuffer(Options{Retain: time.Minute, MaxBytes: 1 << 20})
	r := NewReader(b)
	r.SetTarget(2 * time.Second)
	feed(t, span{b, 0, 1500})
	if s := r.Next(1500 * ms); s.OK || s.Wait != 500*time.Millisecond {
		t.Fatalf("step = %+v, want a 500 ms wait for the first frame", s)
	}
	if st := r.Status(1500 * ms); st.Phase != PhaseFilling {
		t.Fatalf("phase = %s, want filling", st.Phase)
	}
}

func TestAudioSampledBeforeTheJumpKeyframeIsDropped(t *testing.T) {
	b := NewBuffer(Options{Retain: time.Minute, MaxBytes: 1 << 20})
	_ = b.Push(video(0, true))
	_ = b.Push(video(1000, true))
	// Audio sampled at 990 ms arrives after the 1000 ms keyframe.
	late := audio(1001, 3)
	late.Time = 990
	_ = b.Push(late)
	_ = b.Push(audio(1020, 4))
	_ = b.Push(video(2000, true))
	r := NewReader(b)
	drain(r, 2000)

	r.SetTarget(time.Second)
	var ids []byte
	for _, s := range drain(r, 2100) {
		ids = append(ids, s.Frame.Data[2])
	}
	// The 1000 ms keyframe carries 10; the late audio 3 is dropped, the audio after it (4) plays.
	if string(ids) != string([]byte{10, 4}) {
		t.Fatalf("emitted ids %v, want the keyframe then the audio sampled after it", ids)
	}
}

func TestByteCapEvictsUnreadFramesAndTheReaderResumesAtAKeyframe(t *testing.T) {
	// Each frame is 3 bytes; 30 bytes hold one second of input.
	b := NewBuffer(Options{Retain: time.Minute, MaxBytes: 30})
	r := NewReader(b)
	r.SetTarget(10 * time.Second)
	feed(t, span{b, 0, 5000})
	if st := b.Stats(); st.Bytes > 30 {
		t.Fatalf("buffer holds %d bytes, cap is 30", st.Bytes)
	}

	got := drain(r, 15_000)
	if len(got) == 0 || !got[0].Frame.Is(flv.FlagKey) || recvMs(got[0]) != 4000 {
		t.Fatalf("after eviction = %+v, want the oldest kept keyframe at 4000 ms", got)
	}
}

func TestRetentionDropsHistoryTheReaderNoLongerNeeds(t *testing.T) {
	b := NewBuffer(Options{Retain: 2 * time.Second, MaxBytes: 1 << 20})
	r := NewReader(b)
	feed(t, span{b, 0, 10_000})
	drain(r, 10_000)
	if st := b.Stats(); st.Covered > 2*time.Second {
		t.Fatalf("buffer covers %s, want at most 2 s", st.Covered)
	}
}

func TestCloseEndsTheReaderOnceItReachesTheLiveEdge(t *testing.T) {
	b := NewBuffer(Options{Retain: time.Minute, MaxBytes: 1 << 20})
	r := NewReader(b)
	feed(t, span{b, 0, 200})
	b.Close()
	if got := drain(r, 200); len(got) != 2 {
		t.Fatalf("emitted %d frames, want the 2 buffered", len(got))
	}
	if s := r.Next(200 * ms); !s.EOF {
		t.Fatalf("step = %+v, want EOF", s)
	}
	select {
	case <-b.Wake():
	default:
		t.Fatal("Wake did not close with the buffer")
	}
}

func TestAByteCapBelowTheDelayLowersTheAppliedDelayInsteadOfStalling(t *testing.T) {
	// 30 bytes hold one second of input, far less than the 10 s asked for.
	b := NewBuffer(Options{Retain: time.Minute, MaxBytes: 30})
	r := NewReader(b)
	r.SetTarget(10 * time.Second)
	emitted := 0
	for at := int64(0); at < 20_000; at += 100 {
		feed(t, span{b, at, at + 100})
		emitted += len(drain(r, at))
	}
	if emitted < 150 {
		t.Fatalf("emitted %d of 200 frames while ingest kept going, want the output to keep flowing", emitted)
	}
	if st := r.Status(20_000 * ms); st.Applied > time.Second || st.Target != 10*time.Second {
		t.Fatalf("status = %+v, want about 1 s applied and the 10 s target kept", st)
	}
}

func videoHeader(recvMs int64, id byte) flv.Frame {
	return flv.Frame{Type: flv.TypeVideo, Flags: flv.FlagVideo | flv.FlagHeader, Time: uint32(recvMs), Recv: recvMs * ms, Data: []byte{0x17, 0, id}}
}

func TestAJumpSendsTheHeadersInEffectAtTheKeyframeItLandsOn(t *testing.T) {
	b := NewBuffer(Options{Retain: time.Minute, MaxBytes: 1 << 20})
	_ = b.Push(videoHeader(0, 1))
	feed(t, span{b, 0, 5000})
	// OBS restarts with new encoder settings.
	restart := videoHeader(5000, 2)
	restart.Flags |= flv.FlagSessionStart
	_ = b.Push(restart)
	feed(t, span{b, 5000, 10_000})
	r := NewReader(b)
	got := drain(r, 10_000)
	if len(got) < 2 || got[0].Frame.Data[2] != 2 || !got[1].Frame.Is(flv.FlagKey) {
		t.Fatalf("live start = %+v, want the second session's header then its keyframe", got[:min(2, len(got))])
	}

	r.SetTarget(7 * time.Second)
	got = drain(r, 10_000)
	if len(got) < 2 || got[0].Frame.Data[2] != 1 || recvMs(got[1]) != 3000 || !got[1].Discontinuity {
		t.Fatalf("after the jump back = %+v, want the first session's header then the 3000 ms keyframe", got[:min(2, len(got))])
	}
}

func TestEndingTheMatchKeepsTheDelayUntilViewersSawTheEndThenGoesLive(t *testing.T) {
	b := NewBuffer(Options{Retain: time.Minute, MaxBytes: 1 << 20})
	r := NewReader(b)
	r.SetTarget(30 * time.Second)
	feed(t, span{b, 0, 40_000})
	drain(r, 40_000)

	// The match ends at 40 s; viewers are 30 s behind, so they see the end at 70 s.
	r.LiveWhenCaughtUp(40_000 * ms)
	feed(t, span{b, 40_000, 60_000})
	got := drain(r, 60_000)
	if len(got) == 0 || recvMs(got[len(got)-1]) != 30_000 || r.Status(60_000*ms).Phase != PhaseReturning {
		t.Fatalf("at 60 s the last frame is %d ms in phase %s, want 30000 ms still delayed and returning",
			recvMs(got[len(got)-1]), r.Status(60_000*ms).Phase)
	}

	feed(t, span{b, 60_000, 70_100})
	got = drain(r, 70_100)
	var jumped *Step
	for i := range got {
		if got[i].Discontinuity {
			jumped = &got[i]
			break
		}
	}
	if jumped == nil || recvMs(*jumped) != 70_000 || !jumped.Frame.Is(flv.FlagKey) {
		t.Fatalf("after viewers saw 40 s, want a jump to the live keyframe at 70000 ms, got %+v", jumped)
	}
	for _, s := range got {
		if s.Discontinuity {
			break
		}
		if recvMs(s) > 40_000 {
			t.Fatalf("emitted %d ms of the delayed backlog after the end of the match", recvMs(s))
		}
	}
	if st := r.Status(70_100 * ms); st.Phase != PhaseLive || st.Target != 0 {
		t.Fatalf("status = %+v, want live", st)
	}
}

func TestSettingADelayCancelsAPendingReturnToLive(t *testing.T) {
	b := NewBuffer(Options{Retain: time.Minute, MaxBytes: 1 << 20})
	r := NewReader(b)
	r.SetTarget(10 * time.Second)
	feed(t, span{b, 0, 20_000})
	drain(r, 20_000)
	r.LiveWhenCaughtUp(20_000 * ms)
	r.SetTarget(10 * time.Second)
	feed(t, span{b, 20_000, 31_000})
	for _, s := range drain(r, 31_000) {
		if s.Discontinuity {
			t.Fatalf("jumped at %d ms after the return was cancelled", recvMs(s))
		}
	}
}
