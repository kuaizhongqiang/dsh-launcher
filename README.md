# 🐋 dsh-launcher

[![Release](https://img.shields.io/github/v/release/kuaizhongqiang/dsh-launcher?style=flat-square)](https://github.com/kuaizhongqiang/dsh-launcher/releases)
[![License](https://img.shields.io/github/license/kuaizhongqiang/dsh-launcher?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/github/go-mod/go-version/kuaizhongqiang/dsh-launcher?style=flat-square)](go.mod)
[![CI](https://img.shields.io/github/actions/workflow/status/kuaizhongqiang/dsh-launcher/release.yml?style=flat-square)](https://github.com/kuaizhongqiang/dsh-launcher/actions)
[![Platform](https://img.shields.io/badge/platform-Windows%2010%2F11-0a7dff?style=flat-square)]()

A single-file Windows launcher for the **native dsh CLI** ([`@deepseek-ai/dsh`](https://www.npmjs.com/package/@deepseek-ai/dsh), npm edition). Double-click to open a dsh-styled dark GUI: check your environment, pick an install directory, install, and start dsh with one click.

> Built with pure Go — zero runtime dependencies. Ships as one `dsh-launcher.exe`; copy it to any Windows PC and run.

[中文说明](README.zh-CN.md)

## Screenshot

![dsh-launcher GUI](assets/screenshot.png)

## Features

- 🖥️ **Dark dsh-style GUI** — theme tokens taken from the dsh web UI (`#151517` background, `#5686FE` brand blue, 12px rounded corners)
- ⚡ **One-click start** — installs dsh if missing (with a directory picker), then starts the server and opens your browser
- 🔀 **Relocate dsh** — move the installed dsh package to any path (cross-drive handled with copy+delete); refused while dsh is running
- 🔍 **Environment status** — Node.js / npm / dsh versions, install state, port health
- 📜 **Live log** — install & startup output streamed into the window
- 🧹 **Clean lifecycle** — closing the window stops dsh; a Windows Job Object reaps the child process even on force-kill
- ⌨️ **CLI fallback** — `install` / `start` / `status` / `--version` / `--help` work from a terminal too
- 🐋 **DeepSeek Harness icon** — the dsh whale, embedded as a multi-size `.ico`

## Requirements

- **Windows 10 / 11** (x64)
- **Node.js** on the target machine: `^22.19 || >=24` — needed only to *run* dsh; the launcher itself is a standalone Go binary
- No Go toolchain, no npm global config, no environment variables on the target machine

## Download

Grab the latest `dsh-launcher.exe` from the [Releases page](https://github.com/kuaizhongqiang/dsh-launcher/releases). Releases are built automatically by GitHub Actions on `windows-latest` and published with the exe attached.

## Usage

### GUI (double-click, recommended)

1. Double-click `dsh-launcher.exe` (or run it with no arguments).
2. Read the environment status card (Node / npm / dsh / port).
3. Click **启动 (Start)**:
   - dsh already installed → starts the server, opens the browser, button turns into "running"
   - not installed → click **浏览… (Browse…)** to pick an install dir (or type one), then **启动** again to install & start automatically
4. To relocate the installed dsh package, click **移动 (Move)** and pick a target directory (refused while dsh is running).
5. Close the window (or click **退出**) to stop dsh — no orphan processes.

> Set `DSH_LAUNCHER_NO_BROWSER=1` to skip auto-opening the browser.

### CLI

```powershell
dsh-launcher.exe install [--dir <dir>]   # install @deepseek-ai/dsh (default %LOCALAPPDATA%\dsh)
dsh-launcher.exe move --dir <dir>        # relocate the installed dsh package (refused while running)
dsh-launcher.exe start [--no-browser]    # start dsh web, open browser
dsh-launcher.exe status                  # show install dir / version / running state
dsh-launcher.exe --version               # show version
dsh-launcher.exe --help
```

Logs are written to `%TEMP%\dsh-launcher.log`.

## Configuration (`launcher.json`)

Created next to the exe on first `install` (portable — copy the exe together with its config):

```json
{
  "dshInstallDir": "C:\\Users\\<user>\\AppData\\Local\\dsh",
  "dshVersion": "0.1.0-rc.6",
  "port": 3080,
  "installedAt": "2026-08-15T17:00:00+08:00"
}
```

- `~/.dsh` (`DSH_HOME`) is **never** touched — your existing profiles / plugins / sessions stay intact.
- Running `install` on a new machine overwrites the config with the new path.

## Build from source

Requires Go 1.22+.

```powershell
# One-command build (installs rsrc, generates icon resources, builds the exe)
.\build.ps1 -Version v0.0.1        # release build (windowsgui, no console flash)
.\build.ps1 -Debug                 # debug build with console output
```

Manual steps:

```powershell
go mod tidy
go install github.com/akavel/rsrc@v0.10.2
& (Join-Path (go env GOPATH) 'bin\rsrc.exe') -ico icon.ico -manifest app.manifest -o rsrc.syso
go build -ldflags="-s -w -H windowsgui -X main.guiBuild=1 -X main.version=v0.0.1" -o dsh-launcher.exe .
```

Re-generate the icon from the source SVG:

```powershell
go run ./tools/svg2ico favicon.svg icon.ico preview.png
```

## Release workflow

Pushing a `v*` tag triggers [GitHub Actions](.github/workflows/release.yml) — it builds, smoke-tests (`--version` / `--help`), uploads the artifact and publishes a GitHub Release with the exe attached:

```powershell
git tag v0.0.1
git push origin v0.0.1
```

## Design notes

| Decision | Why |
|---|---|
| Single Go exe | Static compile, zero runtime deps, double-click to run |
| `npm install -g --prefix <dir>` | Install dir is explicit & recorded; npm global env untouched |
| `move` = Rename, fallback copy+delete | Same-disk move is instant; cross-drive works via recursive copy |
| Config next to exe | Portable — copy exe + launcher.json together |
| Child process + port polling | Controllable lifecycle, no orphan dsh |
| Job Object (`KILL_ON_JOB_CLOSE`) | Even `taskkill /F` of the launcher reaps the child |
| `CREATE_NO_WINDOW` on children | No console flash from node/npm under a windowsgui parent |
| `BeginPaint` / `EndPaint` | Prevents WM_PAINT storms (`GetDC` never clears the invalid region) |
| UI watchdog | A stuck UI thread auto-exits after 12 s (modal dialogs exempt) |
| `ExtractIconExW` for the icon | Not tied to a fragile resource ID |

## License

[MIT](LICENSE)
