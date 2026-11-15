// Package timeline rewrites source timestamps into one continuous output clock, so a delay jump
// or an OBS reconnect never sends the platform a timestamp that goes back or leaps ahead.
package timeline

const (
	defaultStepMs = 33
	// A step longer than this is a gap or a seek, not the frame cadence.
	maxStepMs = 250
	// A source timestamp further back than this without a jump flag is a source restart.
	jitterMs   = 250
	stepWindow = 300
)

// Sample is one tag to map: its source time, whether it is video, whether it follows a jump, and
// for video the composition offset (presentation time minus Time).
type Sample struct {
	Time  uint32
	Video bool
	Disc  bool
	CTS   int32
}

// Mapper maps source milliseconds to output milliseconds. Audio and video share one offset, so
// their relative timing survives every re-anchor.
type Mapper struct {
	started   bool
	offset    int64
	lastVideo int64
	lastAudio int64
	lastAny   int64
	// maxShown is the latest presentation time of any video frame sent.
	maxShown  int64
	lastSrc   int64
	prevVideo int64
	havePrev  bool
	stepSum   int64
	stepCount int64
}

func New() *Mapper {
	return &Mapper{}
}

// Map returns the output timestamp for a tag. Output never goes below zero or back on a track.
func (m *Mapper) Map(in Sample) uint32 {
	s := int64(in.Time)
	switch {
	case !m.started:
		m.started, m.offset = true, -s
	case in.Disc || s < m.lastSrc-jitterMs:
		next := m.lastAny + m.step()
		if in.Video {
			// The new segment's first frame must also be shown after every frame already sent.
			next = max(next, m.maxShown+m.step()-int64(in.CTS))
		}
		m.offset = next - s
		m.havePrev = false
	}
	last := &m.lastAudio
	if in.Video {
		last = &m.lastVideo
		m.learnStep(s)
	}
	out := max(s+m.offset, *last, 0)
	*last = out
	if in.Video {
		m.maxShown = max(m.maxShown, out+int64(in.CTS))
	}
	m.lastAny = max(m.lastAny, out)
	m.lastSrc = s
	return uint32(out)
}

func (m *Mapper) learnStep(s int64) {
	if m.havePrev {
		if d := s - m.prevVideo; d > 0 && d <= maxStepMs {
			if m.stepCount == stepWindow {
				m.stepSum, m.stepCount = m.stepSum/2, m.stepCount/2
			}
			m.stepSum += d
			m.stepCount++
		}
	}
	m.prevVideo, m.havePrev = s, true
}

func (m *Mapper) step() int64 {
	if m.stepCount == 0 {
		return defaultStepMs
	}
	return m.stepSum / m.stepCount
}
