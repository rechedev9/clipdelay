# Engine CLI

The OBS plugin runs the engine for you. You can also run it from a terminal, for example to use it
with another RTMP encoder or to debug.

```powershell
$env:CLIPDELAY_UPSTREAM = "rtmp://live.twitch.tv/app/<stream-key>"
.\clipdelay-engine.exe -match-delay 3m
```

Point the encoder at `rtmp://127.0.0.1:1935/live` (any stream key unless you pass `-ingest-key`).
The platform URL and key come from `CLIPDELAY_UPSTREAM`, so the key stays out of the command line
and the shell history; logs print it as `****`. `rtmps://` URLs work too (port 443 by default).

## Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `-listen` | `127.0.0.1:1935` | Address the encoder publishes to |
| `-ingest-key` | empty | Stream key the encoder must use; empty accepts any |
| `-delay` | `0` | Delay at start |
| `-match-delay` | `3m` | Delay the `match` command sets |
| `-retain` | `5m` | History kept in memory, and so the largest delay; must be at least `-match-delay` |
| `-max-mb` | `512` | Hard cap on buffered stream data; when it holds less than the delay, the delay shrinks to fit |
| `-control` | off | Plugin mode: exit once stdin closes and the delayed tail is sent |

## Commands (stdin)

| Command | Effect |
| --- | --- |
| `match`, `partida` | Set the delay to `-match-delay` |
| `end`, `fin` | Keep the delay until viewers see this moment, then go live |
| `live` | Go live now |
| `30`, `90s`, `2m` | Set any delay up to `-retain` |
| `status` | Phase, delay, buffer and platform connection, for people |
| `json` | The same as one JSON line, for programs |
| `quit` | Stop |

A raise beyond the history buffered so far stays pending at the current delay, and `status` shows
how long until it is ready. Stopping the encoder sends the delayed tail, then closes the platform
connection.
