// Package flv classifies the compressed RTMP tags OBS sends, without decoding them.
package flv

// Tag types as RTMP message type IDs.
const (
	TypeAudio  uint8 = 8
	TypeVideo  uint8 = 9
	TypeScript uint8 = 18
)

// Flags describe what a tag is to the delay engine.
const (
	FlagAudio uint8 = 1 << iota
	FlagVideo
	FlagKey
	FlagHeader
	FlagMeta
	// FlagSessionStart marks the first tag of a new ingest session: source timestamps restart.
	FlagSessionStart
)

// Frame is one compressed tag kept in the delay buffer.
type Frame struct {
	Type  uint8
	Flags uint8
	// Time is the source timestamp in milliseconds.
	Time uint32
	// Recv is the monotonic receive instant in nanoseconds.
	Recv int64
	Data []byte
}

func (f Frame) Is(flag uint8) bool { return f.Flags&flag != 0 }

// HeaderKind tells metadata, video and audio headers apart; a newer one of a kind replaces the older.
func HeaderKind(f Frame) uint8 { return f.Flags & (FlagMeta | FlagAudio | FlagVideo) }

// Classify returns the flags of a tag body.
func Classify(typ uint8, data []byte) uint8 {
	switch typ {
	case TypeScript:
		return FlagMeta | FlagHeader
	case TypeAudio:
		return FlagAudio | audioFlags(data)
	case TypeVideo:
		return FlagVideo | videoFlags(data)
	}
	return 0
}

func audioFlags(data []byte) uint8 {
	if len(data) < 2 {
		return 0
	}
	format := data[0] >> 4
	switch format {
	case 10: // AAC: packet type 0 is the AudioSpecificConfig.
		if data[1] == 0 {
			return FlagHeader
		}
	case 9: // Enhanced RTMP audio: packet type 0 is SequenceStart.
		if data[0]&0x0F == 0 {
			return FlagHeader
		}
	}
	return 0
}

func videoFlags(data []byte) uint8 {
	if len(data) < 1 {
		return 0
	}
	b := data[0]
	if b&0x80 != 0 {
		return enhancedVideoFlags(b)
	}
	frameType := b >> 4
	codec := b & 0x0F
	// AVC (7) and the legacy HEVC id (12) carry the packet type in byte 1; 0 is the sequence header.
	if (codec == 7 || codec == 12) && len(data) > 1 && data[1] == 0 {
		return FlagHeader
	}
	if frameType == 1 {
		return FlagKey
	}
	return 0
}

// Enhanced RTMP: bit 7 set, frame type in bits 4-6, packet type in bits 0-3.
func enhancedVideoFlags(b byte) uint8 {
	frameType := (b >> 4) & 0x07
	switch b & 0x0F {
	case 0, 5: // SequenceStart, MPEG2TSSequenceStart
		return FlagHeader
	case 1, 3: // CodedFrames, CodedFramesX
		if frameType == 1 {
			return FlagKey
		}
	}
	return 0
}

// CompositionTime returns a video tag's presentation offset in ms: with B-frames a frame is shown
// later than it is decoded. Tags that carry no offset return 0.
func CompositionTime(data []byte) int32 {
	if len(data) < 1 {
		return 0
	}
	at := -1
	switch b := data[0]; {
	case b&0x80 != 0 && b&0x0F == 1: // Enhanced CodedFrames: the offset follows the FourCC.
		at = 5
	case b&0x80 == 0 && (b&0x0F == 7 || b&0x0F == 12) && len(data) > 1 && data[1] == 1:
		at = 2
	}
	if at < 0 || len(data) < at+3 {
		return 0
	}
	v := int32(data[at])<<16 | int32(data[at+1])<<8 | int32(data[at+2])
	return v << 8 >> 8 // sign-extend the 24-bit value
}
