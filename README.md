# ClipDelay ⏱️ Change your stream delay without going offline

[![Release](https://img.shields.io/github/v/release/rechedev9/clipdelay?style=flat-square)](https://github.com/rechedev9/clipdelay/releases)
[![OBS](https://img.shields.io/badge/OBS-31%2B-302e31?style=flat-square)](https://obsproject.com)
[![Platform](https://img.shields.io/badge/platform-Windows%20x64-0078d4?style=flat-square)](#install)
[![License](https://img.shields.io/badge/license-MIT%20%2B%20GPL--2.0-blue?style=flat-square)](#license)

ClipDelay is an OBS plugin that puts a delay between OBS and your platform and lets you change it
while you are live. Start a match: viewers fall three minutes behind. Match over: they catch up the
moment they see the end, and you talk to chat in real time. The stream never drops, and nothing is
re-encoded.

It is built for competitive players who stream tournament matches. OBS's own stream delay is fixed
for the whole session; ClipDelay's is a button.

## Install

Download `clipdelay-<version>-windows-x64.zip` from the
[latest release](https://github.com/rechedev9/clipdelay/releases/latest), close OBS, and extract the
zip into `C:\ProgramData\obs-studio\plugins\`:

```powershell
Expand-Archive clipdelay-0.1.0-windows-x64.zip -DestinationPath "$env:ProgramData\obs-studio\plugins"
```

You end up with `C:\ProgramData\obs-studio\plugins\clipdelay\`. Needs OBS 31 or newer on Windows
10 or 11 (x64).

**With an AI agent.** Point Claude Code, Codex or any coding agent at the
[agent install guide](docs/agent-install.md):

```text
Install ClipDelay for OBS following https://github.com/rechedev9/clipdelay/blob/main/docs/agent-install.md
```

## Quick start

1. Set up your platform in OBS as usual: **Settings > Stream**, with your stream key.
2. Open the panel: **Docks > ClipDelay**.
3. Tick **Use ClipDelay when streaming**. OBS now streams to ClipDelay, and ClipDelay to your
   platform. Untick it to put your original settings back.
4. **Start Streaming**. You are live with no delay.
5. Match starts: **Start match**. Match ends: **End match**.

Set **Settings > Output > Keyframe Interval** to `2` s. A delay change lands on a keyframe, so this
is the most it ever waits.

## Controls

| Control | What viewers see | Hotkey name |
| --- | --- | --- |
| **Start match** | The stream falls behind by the match delay (1 to 10 min, 3 by default) | `ClipDelay: start match` |
| **End match** | The delay stays until they see the moment you pressed it, then they are live | `ClipDelay: end match` |
| **Go live now** | Live at once; what was still delayed is skipped | `ClipDelay: go live now` |
| **Stop Streaming** (OBS) | The delayed tail plays out, then the stream ends cleanly | |

Bind the hotkeys in **Settings > Hotkeys**. They work while the game is fullscreen.

The panel tells you where you are: `Live, no delay`, `Delay 3:00`,
`Building up the 3:00 delay: 1:12 left` (right after you go live there is no history to delay yet),
`Delay 3:00, going live once viewers see the end`, and the platform connection.

## How it works

```text
 OBS ──RTMP──► ClipDelay engine (127.0.0.1:19355) ──RTMP/RTMPS──► Twitch, YouTube, ...
                 │ keeps the encoded stream in memory
                 └ forwards each frame at receive time + delay
```

- **No re-encoding.** The engine keeps OBS's encoded packets and sends them later, so it costs
  2 to 3% of one CPU core. Memory is about 1.6 times the delayed video: roughly 190 MB for
  3 minutes at 6 Mb/s.
- **Jumps on keyframes.** Raising the delay jumps back to an earlier keyframe; lowering it jumps
  ahead. Codec headers are re-sent at every jump.
- **One continuous timeline.** Timestamps are rewritten so the platform sees one stream that never
  goes back, including with B-frames and across an OBS encoder restart.
- **Your key stays in OBS.** The switch saves your stream service and restores it when you turn
  ClipDelay off. The engine gets the key through its environment, never the command line, and logs
  print it as `****`.
- **Survives a dropped upload.** If the platform connection drops, the engine reconnects with
  backoff and resumes on a keyframe.

The engine is a small Go program (`cmd/clipdelay`) that also runs on its own from a terminal. See
[docs/cli.md](docs/cli.md).

## Status

`0.1.0` is the first public release. It has been checked end to end with OBS 32.2 (x264 with
B-frames) streaming through the plugin to a local RTMP server: delay changes, end of match, drain on
stop, restarts. Test with your own platform before a tournament, for example with Twitch's
`?bandwidthtest=true` key suffix, which goes through ingest without going live.

Known limits:

- Windows only.
- OBS multitrack streaming (Twitch Enhanced Broadcasting) is not supported.
- A reconnect to the platform after a real network cut has not been tested yet.
- The binaries are not code-signed.

## Build from source

Needs Visual Studio 2022 (C++ workload), CMake 3.28+, and Go 1.26.

```powershell
cd obs-plugin
cmake --preset windows-x64          # first run downloads OBS sources, obs-deps and Qt into .deps
cmake --build build_x64 --config RelWithDebInfo
cmake --install build_x64 --config RelWithDebInfo --prefix "$env:ProgramData\obs-studio\plugins"
```

The build also compiles the Go engine and ships it as `data/clipdelay-engine.exe`. Engine tests:

```powershell
go vet ./...
go test -race ./...
```

## Docs

- [Agent install guide](docs/agent-install.md): install, verify and uninstall, step by step.
- [Engine CLI](docs/cli.md): run the relay without OBS's panel.
- [AGENTS.md](AGENTS.md): layout and invariants for contributors and coding agents.

## License

The engine (everything outside `obs-plugin/`) is [MIT](LICENSE). The OBS plugin in `obs-plugin/`
links against OBS and is [GPL-2.0](obs-plugin/LICENSE).
