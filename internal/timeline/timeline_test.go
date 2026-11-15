package timeline

import "testing"

type in = Sample

func mapAll(m *Mapper, samples []in) []uint32 {
	out := make([]uint32, len(samples))
	for i, s := range samples {
		out[i] = m.Map(s)
	}
	return out
}

func equal(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestOutputStartsAtZeroAndKeepsSourceSpacing(t *testing.T) {
	got := mapAll(New(), []in{{5000, true, false, 0}, {5010, false, false, 0}, {5033, true, false, 0}})
	if want := []uint32{0, 10, 33}; !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAJumpBackContinuesOneVideoStepAfterTheLastOutput(t *testing.T) {
	got := mapAll(New(), []in{
		{10_000, true, false, 0}, {10_040, true, false, 0}, {10_080, true, false, 0},
		{4000, true, true, 0}, {4021, false, false, 0}, {4040, true, false, 0},
	})
	if want := []uint32{0, 40, 80, 120, 141, 160}; !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAJumpForwardDoesNotLeaveAGap(t *testing.T) {
	got := mapAll(New(), []in{{0, true, false, 0}, {33, true, false, 0}, {90_000, true, true, 0}, {90_033, true, false, 0}})
	if want := []uint32{0, 33, 66, 99}; !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAudioAheadOfVideoAnchorsTheJumpPastBothTracks(t *testing.T) {
	got := mapAll(New(), []in{{0, true, false, 0}, {33, true, false, 0}, {80, false, false, 0}, {500, true, true, 0}})
	// The last output is audio at 80; the new segment starts one video step (33) later.
	if want := []uint32{0, 33, 80, 113}; !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSmallBackwardJitterWithoutAJumpNeverMovesATrackBack(t *testing.T) {
	got := mapAll(New(), []in{{100, false, false, 0}, {123, false, false, 0}, {120, false, false, 0}, {146, false, false, 0}})
	if want := []uint32{0, 23, 23, 46}; !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestASourceRestartWithoutAJumpFlagStillContinues(t *testing.T) {
	// OBS reconnects and its clock restarts at 0; a backwards leap past the jitter window re-anchors.
	got := mapAll(New(), []in{{60_000, true, false, 0}, {60_033, true, false, 0}, {0, true, false, 0}, {33, true, false, 0}})
	if want := []uint32{0, 33, 66, 99}; !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTheStepIsEstimatedFromTheVideoCadence(t *testing.T) {
	m := New()
	for i := range 10 {
		m.Map(Sample{Time: uint32(i * 20), Video: true})
	}
	if got := m.Map(Sample{Time: 50_000, Video: true, Disc: true}); got != 200 {
		t.Fatalf("after a 50 fps run the jump maps to %d, want 180+20", got)
	}
}

func TestAudioSampledBeforeTheFirstVideoFrameStartsAtZero(t *testing.T) {
	got := mapAll(New(), []in{{1000, true, false, 0}, {990, false, false, 0}, {1010, false, false, 0}})
	if want := []uint32{0, 0, 10}; !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAJumpNeverPresentsTheKeyframeBeforeAFrameAlreadySent(t *testing.T) {
	// With B-frames a frame decodes before it is shown: its presentation time is Time + CTS.
	got := mapAll(New(), []in{
		{Time: 0, Video: true, CTS: 33}, {Time: 33, Video: true, CTS: 100},
		{Time: 5000, Video: true, Disc: true, CTS: 33},
	})
	// The last frame is shown at 133, so the keyframe (shown at out + 33) decodes at 133.
	if want := []uint32{0, 33, 133}; !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
