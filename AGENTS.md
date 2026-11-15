# ClipDelay

A local RTMP relay for OBS with a delay that changes while the stream stays up, and the OBS plugin
that drives it. OBS encodes once and publishes to the relay; the relay keeps the compressed tags in
memory and forwards them to the platform. A delay change jumps to a video keyframe, and the publisher rewrites
timestamps so the platform sees one continuous stream. Nothing is decoded or re-encoded.

The user-facing docs are `README.md` and `docs/`. Keep them in step with the code: a change to a
control, a status message, the install layout or a limit updates them in the same commit.

## Layout

- `cmd/clipdelay`: the CLI. `CLIPDELAY_UPSTREAM` holds the platform URL with the stream key, so the
  key stays out of flags, shell history and logs. Stdin takes `partida` (sets `-match-delay`, 3 min
  by default), `fin`, `30`, `90s`, `live`, `status`, `json`, `quit`.
- `internal/flv`: classifies tags (keyframe, sequence header, metadata), legacy and Enhanced RTMP.
- `internal/delay`: `Buffer` (frames by sequence, keyframe index, retention, byte cap) and `Reader`
  (a frame received at `Recv` is due at `Recv + applied`). The reader takes the clock as an argument.
- `internal/timeline`: maps source timestamps onto one output clock across jumps and OBS restarts.
- `internal/relay`: ingest server, publisher with reconnect, and the `Relay` that ties them together.
- `obs-plugin/`: the OBS plugin (C++ and Qt, from obsproject/obs-plugintemplate, GPL-2.0 because it
  links libobs). It ships the Go relay as `data/clipdelay-engine.exe` and runs it with `-control`:
  commands go to stdin, `json` returns one status line, and stdin closing makes the engine exit
  once the delayed tail is sent. The switch saves the user's stream service and points OBS at the
  engine on `127.0.0.1:19355`; turning it off restores the service. Hotkeys and the dock buttons
  send `match`, `fin` and `live`.

## Invariants

- Every jump lands on a video keyframe, and audio sampled before that keyframe is dropped.
- Every start and jump first emits the headers in effect at its keyframe, so a jump across an
  OBS restart never pairs one session's frames with the other's SPS/PPS.
- A raise past the buffered history stays pending at the current delay; the output never idles.
- When `MaxBytes` holds less than the delay, the reader lowers the applied delay to what the
  buffer holds and keeps the target; it never chases frames evicted before they are due.
- Output timestamps never go back per track, and audio and video share one offset. A jump also
  starts after the latest presentation time sent (DTS plus the tag's composition offset), so a
  B-frame stream never shows the new keyframe before a frame already sent.
- `fin` (`Reader.LiveWhenCaughtUp`) keeps the delay until viewers have seen the moment it was
  asked, then jumps live, so ending a match never skips its last minutes. `live` jumps at once.
- When OBS stops, the publisher sends what is still delayed, sends `deleteStream`, closes the
  upstream and releases the buffer.
- Logs and status never contain the stream key (`upstream.String` masks it).

## Check

- `go vet ./...` and `go test -race ./...` from the repository root.
- `internal/relay/relay_test.go` runs a fake OBS and a fake platform on loopback. Its timing
  tolerances are wide on purpose; do not tighten them to make a run look better.
- A real-codec run (FFmpeg x264/AAC 1080p60 at 6 Mb/s into the relay, `ffmpeg -listen 1` as the
  platform) decoded with no errors or timestamp gaps through two partida/fin cycles. With the CLI's
  GC percent of 25, process memory is about 1.6 times the buffered bytes (125 MB for 77 MB).
- Plugin: `cmake --preset windows-x64` then `cmake --build build_x64 --config RelWithDebInfo` from
  `obs-plugin/` (Visual Studio 2022, CMake, Go). The first configure downloads the OBS sources,
  obs-deps and Qt into `obs-plugin/.deps`. `cmake --install build_x64 --prefix <dir>` lays out
  `clipdelay/bin/64bit` and `clipdelay/data` for `%ProgramData%\obs-studio\plugins`. Check it by
  hand in a portable OBS copy (`portable_mode.txt`, `--multi`), never the user's OBS profile.
- A portable OBS 32.2 run (x264 with B-frames, 1 min match delay) streamed through the plugin to
  `ffmpeg -listen 1`: two partida/fin cycles with hotkeys and buttons decoded with no errors, no
  presentation overlap at jumps and no gap over 200 ms, and the delayed tail drained after Stop.

## Not built yet

Roadmap milestones 5 to 7: write deadlines (go-rtmp v0.0.7 fixes its own 5 s write timeout),
backpressure policy, overlay, clips and game automation. The plugin does not handle OBS
multitrack (Enhanced Broadcasting) services, and it does not set the keyframe interval: with a
custom service OBS uses the encoder's own setting, so a delay change waits for the next keyframe.
The reader reports five phases (filling, live, delayed, changing, returning), not the roadmap's
seven. The publisher does not send `releaseStream`/`FCPublish`; check each platform with a real
OBS run before relying on it. go-rtmp waits for handshake replies without a deadline, so
`connect` gives up after 15 s and leaves that goroutine to close the connection if it ever returns.

## Rules

- Source files stay at or under 1000 lines.
- No CI on pushes or pull requests; run the checks above before a release. The one workflow is
  `release.yml`, on `v*` tags and manual runs.
- Release binaries come only from `release.yml`, never from a local build, so every zip carries a
  GitHub build attestation. Do not attach hand-built files to a release.
- `docs/agent-install.md` leads with building from source: agents often refuse to install a
  downloaded DLL. Keep its steps, file list and tool versions in step with `CMakeLists.txt`,
  `CMakePresets.json` and `buildspec.json`.
- Never write a stream key, token or `settings.json`/`service.json` contents into code, docs, logs
  or issues.
- User-facing text has no em or en dashes.

## Release

1. Bump `version` in `obs-plugin/buildspec.json` and the version examples in `README.md` and
   `docs/agent-install.md`.
2. Run the Go checks, build the plugin, and check it by hand in a portable OBS.
3. Optional dry run: `gh workflow run release.yml` builds the zip as a workflow artifact and
   publishes nothing.
4. Push the tag `v<version>`. `release.yml` checks that the tag matches `buildspec.json`, runs the
   Go checks, builds, zips `clipdelay` without the `.pdb` as `clipdelay-<version>-windows-x64.zip`,
   writes `SHA256SUMS.txt`, attests the zip and publishes the release.
5. Check it: `gh attestation verify clipdelay-<version>-windows-x64.zip --repo rechedev9/clipdelay`.
