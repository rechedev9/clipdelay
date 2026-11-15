# ClipDelay

A local RTMP relay for OBS with a delay that changes while the stream stays up. OBS encodes once and
publishes to `rtmp://127.0.0.1:1935/live`; ClipDelay keeps the compressed tags in memory and forwards
them to the platform. A delay change jumps to a video keyframe, and the publisher rewrites
timestamps so the platform sees one continuous stream. Nothing is decoded or re-encoded.

ClipDelay is its own product and its own Go module. It shares no code, checks or release with
ClipHub Studio or ClipMind; the root `go test ./...` does not enter this folder.

## Layout

- `cmd/clipdelay`: the CLI. `CLIPDELAY_UPSTREAM` holds the platform URL with the stream key, so the
  key stays out of flags, shell history and logs. Stdin takes `partida` (sets `-match-delay`, 3 min
  by default), `fin`, `30`, `90s`, `live`, `status`, `quit`.
- `internal/flv`: classifies tags (keyframe, sequence header, metadata), legacy and Enhanced RTMP.
- `internal/delay`: `Buffer` (frames by sequence, keyframe index, retention, byte cap) and `Reader`
  (a frame received at `Recv` is due at `Recv + applied`). The reader takes the clock as an argument.
- `internal/timeline`: maps source timestamps onto one output clock across jumps and OBS restarts.
- `internal/relay`: ingest server, publisher with reconnect, and the `Relay` that ties them together.

## Invariants

- Every jump lands on a video keyframe, and audio sampled before that keyframe is dropped.
- Every start and jump first emits the headers in effect at its keyframe, so a jump across an
  OBS restart never pairs one session's frames with the other's SPS/PPS.
- A raise past the buffered history stays pending at the current delay; the output never idles.
- When `MaxBytes` holds less than the delay, the reader lowers the applied delay to what the
  buffer holds and keeps the target; it never chases frames evicted before they are due.
- Output timestamps never go back per track, and audio and video share one offset.
- `fin` (`Reader.LiveWhenCaughtUp`) keeps the delay until viewers have seen the moment it was
  asked, then jumps live, so ending a match never skips its last minutes. `live` jumps at once.
- When OBS stops, the publisher sends what is still delayed, sends `deleteStream`, closes the
  upstream and releases the buffer.
- Logs and status never contain the stream key (`upstream.String` masks it).

## Check

- `go vet ./...` and `go test -race ./...` from this folder.
- `internal/relay/relay_test.go` runs a fake OBS and a fake platform on loopback. Its timing
  tolerances are wide on purpose; do not tighten them to make a run look better.
- A real-codec run (FFmpeg x264/AAC 1080p60 at 6 Mb/s into the relay, `ffmpeg -listen 1` as the
  platform) decoded with no errors or timestamp gaps through two partida/fin cycles. Process memory
  is about twice the buffered bytes, so budget `-max-mb` against that.

## Not built yet

Roadmap milestones 5 to 7: write deadlines (go-rtmp v0.0.7 fixes its own 5 s write timeout),
backpressure policy, a local control API with a token, OBS WebSocket setup, overlay, hotkeys, clips
and game automation. The reader reports five phases (filling, live, delayed, changing, returning),
not the roadmap's seven. The publisher does not send
`releaseStream`/`FCPublish`; check each platform with a real OBS run before relying on it.
go-rtmp waits for handshake replies without a deadline, so `connect` gives up after 15 s and
leaves that goroutine to close the connection if it ever returns.
