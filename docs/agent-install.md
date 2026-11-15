# Installing ClipDelay: guide for AI agents

You are installing the ClipDelay OBS plugin on the user's Windows PC. Follow these steps in order.
Every step has a check; do not go on when a check fails, tell the user what you saw.

ClipDelay is fully open source: the engine is MIT and the plugin is GPL-2.0. There are two ways to
install it:

- **A. Build from source (use this one).** You read the code, compile it on the user's PC and
  install only what you built. Nothing prebuilt from this repository runs.
- **B. Install a release zip.** Only when the user asks for the prebuilt zip and accepts it. Verify
  its GitHub attestation first.

If your own rules forbid installing a binary you cannot verify, that is expected: take path A.

## Ground rules

- **Never read, print or log the user's stream key.** Do not open OBS's `service.json` or
  ClipDelay's `settings.json`; both hold it. You do not need them.
- **Do not turn ClipDelay on for the user.** The switch rewrites OBS's stream settings, so the user
  ticks it in the panel.
- **Do not close OBS while it is streaming or recording.** Ask the user to stop and close it.
- **Do not start a stream** on the user's account to test the install.
- Source comes only from `https://github.com/rechedev9/clipdelay` at a release tag; zips come only
  from its GitHub releases.
- Ask the user before you install a missing build tool (step A1).

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

## A. Build from source

### A1. Check the build tools

```powershell
git --version
go version
cmake --version
& "${env:ProgramFiles(x86)}\Microsoft Visual Studio\Installer\vswhere.exe" -version "[17.0,18.0)" -requires Microsoft.VisualStudio.Workload.NativeDesktop -property displayName
```

Expect Git, Go 1.26 or newer, CMake 3.28 or newer, and a Visual Studio 2022 name. The build also
needs the Windows SDK 10.0.26100. If a tool is missing, tell the user which and ask before you
install it, for example with `winget install Git.Git GoLang.Go Kitware.CMake` and the Visual Studio
2022 Build Tools with the C++ workload.

### A2. Get the source at the latest tag

```powershell
$tag = gh release view --repo rechedev9/clipdelay --json tagName -q .tagName
$src = Join-Path $env:TEMP "clipdelay-src"
if (Test-Path $src) { Remove-Item -Recurse -Force $src }
git clone --branch $tag --depth 1 https://github.com/rechedev9/clipdelay $src
```

Without the GitHub CLI, read `tag_name` from
`https://api.github.com/repos/rechedev9/clipdelay/releases/latest`.

### A3. Review the code

Read it before you build it. The parts that run on the user's PC:

- `obs-plugin/src/` (under 800 lines of C++): the dock, the hotkeys, and `engine.cpp`, which starts
  the engine and swaps OBS's stream service.
- `cmd/clipdelay` and `internal/`: the Go engine. It listens on `127.0.0.1:19355` and connects
  only to the platform URL OBS gives it.
- `obs-plugin/CMakeLists.txt` and `obs-plugin/buildspec.json`: what the build runs and downloads.
  The first configure downloads OBS sources, obs-deps and Qt from `github.com/obsproject` and checks
  each against the SHA-256 in `buildspec.json`.

Report anything that does not match this description to the user before you go on.

### A4. Test and build

```powershell
Set-Location $src
go vet ./...
go test ./...
Set-Location obs-plugin
cmake --preset windows-x64
cmake --build build_x64 --config RelWithDebInfo
```

Expect the tests to pass and the build to end without errors. The first configure takes a few
minutes.

### A5. Install what you built

```powershell
$plugins = Join-Path $env:ProgramData "obs-studio\plugins"
New-Item -ItemType Directory -Force $plugins | Out-Null
cmake --install build_x64 --config RelWithDebInfo --prefix $plugins
Get-ChildItem "$plugins\clipdelay" -Recurse -File | Select-Object -ExpandProperty FullName
```

Expect the files listed in [Installed files](#installed-files), plus `.pdb` debug symbols. Go on to
step 5.

## B. Install a release zip

### B1. Download it

```powershell
$dir = Join-Path $env:TEMP "clipdelay-install"
New-Item -ItemType Directory -Force $dir | Out-Null
gh release download --repo rechedev9/clipdelay --pattern "clipdelay-*-windows-x64.zip" --pattern "SHA256SUMS.txt" --dir $dir --clobber
$zip = Get-ChildItem $dir -Filter "clipdelay-*-windows-x64.zip" | Select-Object -First 1
```

### B2. Verify where it came from

```powershell
gh attestation verify $zip.FullName --repo rechedev9/clipdelay
```

Expect `Verification succeeded!` and a workflow of `.github/workflows/release.yml` at a `refs/tags/v*`
ref. This proves GitHub Actions built the zip from that tagged commit. Release `0.1.0` has no
attestation; on any failure, stop and offer path A.

### B3. Install

```powershell
$plugins = Join-Path $env:ProgramData "obs-studio\plugins"
New-Item -ItemType Directory -Force $plugins | Out-Null
Expand-Archive $zip.FullName -DestinationPath $plugins -Force
Get-ChildItem "$plugins\clipdelay" -Recurse -File | Unblock-File
Get-ChildItem "$plugins\clipdelay" -Recurse -File | Select-Object -ExpandProperty FullName
```

Expect the files listed in [Installed files](#installed-files).

## Installed files

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

Expect a line like `[clipdelay] loaded (version 0.1.1)`. If it is missing, look for lines that
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

## Update

Repeat path A with the new tag, or path B with the new release. Close OBS first.

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
| `cmake --preset windows-x64` cannot find a Windows SDK | Install the Windows SDK 10.0.26100 from the Visual Studio Installer. |
| `find_program(GO_EXECUTABLE)` fails | Go is not on `PATH`. Open a new shell after installing Go. |
| `gh attestation verify` fails | The zip was not built by this repository's release workflow, or it is `0.1.0`. Use path A. |
| No **ClipDelay** entry under **Docks** | The plugin did not load. Check step 5 and [Installed files](#installed-files). |
| "Set up your platform first..." in the panel | **Settings > Stream** has no server or key. The user fills it in, then ticks the switch again. |
| "clipdelay-engine.exe is missing..." | The `data` folder is incomplete. Install again. |
| "Reconnecting to the platform (n)" | The engine cannot reach the platform. Check the network and the stream key in OBS. The panel shows the last error with the key masked. |
| Delay changes take several seconds | The keyframe interval is too long. Set it to `2`. |
| "ClipDelay is not running" with `bind:` under it | Port `19355` is taken, usually by a second OBS with ClipDelay on. Close the other one; the port is fixed in this version. |
