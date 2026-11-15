# Installing ClipDelay: guide for AI agents

You are installing the ClipDelay OBS plugin on the user's Windows PC. Follow these steps in order.
Every step has a check; do not go on when a check fails, tell the user what you saw.

## Ground rules

- **Never read, print or log the user's stream key.** Do not open OBS's `service.json` or
  ClipDelay's `settings.json`; both hold it. You do not need them.
- **Do not turn ClipDelay on for the user.** The switch rewrites OBS's stream settings, so the user
  ticks it in the panel.
- **Do not close OBS while it is streaming or recording.** Ask the user to stop and close it.
- **Do not start a stream** on the user's account to test the install.
- Install only from this repository's GitHub releases.

## 1. Check the system

```powershell
[Environment]::Is64BitOperatingSystem
(Get-Item "$env:ProgramFiles\obs-studio\bin\64bit\obs64.exe").VersionInfo.ProductVersion
```

Expect `True` and an OBS version of `31.0.0` or newer. If OBS lives elsewhere, ask the user for the
path. If it is older than 31, ask the user to update OBS first.

## 2. Make sure OBS is closed

```powershell
Get-Process obs64 -ErrorAction SilentlyContinue
```

If it prints a process, ask the user to stop any stream or recording and close OBS, then check again.

## 3. Download the release and verify it

```powershell
$dir = Join-Path $env:TEMP "clipdelay-install"
New-Item -ItemType Directory -Force $dir | Out-Null
gh release download --repo rechedev9/clipdelay --pattern "clipdelay-*-windows-x64.zip" --pattern "SHA256SUMS.txt" --dir $dir --clobber
```

Without the GitHub CLI, take the two asset URLs from
`https://api.github.com/repos/rechedev9/clipdelay/releases/latest` and download them with
`Invoke-WebRequest`.

Verify the checksum:

```powershell
$zip = Get-ChildItem $dir -Filter "clipdelay-*-windows-x64.zip" | Select-Object -First 1
$expected = (Select-String -Path "$dir\SHA256SUMS.txt" -Pattern $zip.Name).Line.Split(" ")[0]
(Get-FileHash $zip.FullName -Algorithm SHA256).Hash.ToLower() -eq $expected.ToLower()
```

Expect `True`. On `False`, stop and tell the user.

## 4. Install

```powershell
$plugins = Join-Path $env:ProgramData "obs-studio\plugins"
New-Item -ItemType Directory -Force $plugins | Out-Null
Expand-Archive $zip.FullName -DestinationPath $plugins -Force
Get-ChildItem "$plugins\clipdelay" -Recurse -File | Unblock-File
Get-ChildItem "$plugins\clipdelay" -Recurse -File | Select-Object -ExpandProperty FullName
```

Expect at least these files:

```text
C:\ProgramData\obs-studio\plugins\clipdelay\bin\64bit\clipdelay.dll
C:\ProgramData\obs-studio\plugins\clipdelay\data\clipdelay-engine.exe
C:\ProgramData\obs-studio\plugins\clipdelay\data\locale\en-US.ini
C:\ProgramData\obs-studio\plugins\clipdelay\data\locale\es-ES.ini
```

An older OBS plugin layout (`obs-plugins\64bit` inside the OBS folder) is not needed and should not
be used.

## 5. Verify that OBS loads it

Ask the user to open OBS once (or start it yourself if they agree), then read the newest log:

```powershell
$log = Get-ChildItem "$env:APPDATA\obs-studio\logs" | Sort-Object LastWriteTime -Descending | Select-Object -First 1
Select-String -Path $log.FullName -Pattern "\[clipdelay\]"
```

Expect a line like `[clipdelay] loaded (version 0.1.0)`. If it is missing, look for lines that
mention `clipdelay.dll` in the same log and report them.

## 6. Hand over to the user

Tell the user, in their language:

1. Open the panel: **Docks > ClipDelay** (new docks start hidden).
2. Set **Settings > Output > Keyframe Interval** to `2`.
3. Bind the three hotkeys in **Settings > Hotkeys** (`ClipDelay: start match`, `end match`,
   `go live now`).
4. With their platform already set in **Settings > Stream** and OBS not streaming, tick
   **Use ClipDelay when streaming**.
5. For a first test on Twitch, add `?bandwidthtest=true` to the stream key before ticking the switch:
   Twitch receives the stream without putting it live.

## Uninstall

1. Ask the user to untick **Use ClipDelay when streaming** first, so OBS gets its original stream
   settings back.
2. Close OBS, then:

```powershell
Remove-Item -Recurse -Force (Join-Path $env:ProgramData "obs-studio\plugins\clipdelay")
```

If the plugin was removed while the switch was on, OBS still points at `127.0.0.1:19355`. The user
then sets their platform again in **Settings > Stream**.

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| No **ClipDelay** entry under **Docks** | The plugin did not load. Check step 5 and the folder layout in step 4. |
| "Set up your platform first..." in the panel | **Settings > Stream** has no server or key. The user fills it in, then ticks the switch again. |
| "clipdelay-engine.exe is missing..." | The `data` folder is incomplete. Reinstall from step 4. |
| "Reconnecting to the platform (n)" | The engine cannot reach the platform. Check the network and the stream key in OBS. The panel shows the last error with the key masked. |
| Delay changes take several seconds | The keyframe interval is too long. Set it to `2`. |
| "ClipDelay is not running" with `bind:` under it | Port `19355` is taken, usually by a second OBS with ClipDelay on. Close the other one; the port is fixed in this version. |
