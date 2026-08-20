# 🐋 dsh-launcher

[![Release](https://img.shields.io/github/v/release/kuaizhongqiang/dsh-launcher?style=flat-square)](https://github.com/kuaizhongqiang/dsh-launcher/releases)
[![License](https://img.shields.io/github/license/kuaizhongqiang/dsh-launcher?style=flat-square)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-Windows%2010%2F11-0a7dff?style=flat-square)]()

dsh（[`@deepseek-ai/dsh`](https://www.npmjs.com/package/@deepseek-ai/dsh)，npm 版）的 **Windows 单文件安装 + 启动引导器**。双击 `dsh-launcher.exe` 即弹出桌面窗口：查看环境状态、选择安装目录、安装、一键启动。

> **TS/JS 技术栈，与 dsh web 同源**：界面直接消费 dsh 的 dsw 设计系统（同一套 `--dsw-*` 令牌），Electron 打包为单文件 exe，启动零依赖（内置 Chromium + Node）。

## 截图

![dsh-launcher GUI](assets/screenshot.png)

> 截图来自早期 Go 版；TS/JS 版界面视觉沿用 dsh web 主题（极光背景 / 毛玻璃卡片 / 脉冲状态点 / 按钮辉光）。

## 特性

- 🪟 **桌面窗口** — 双击 exe 弹出 frameless 窗口（自绘标题栏），**不依赖浏览器**
- 📦 **单文件零依赖** — Electron portable 单 exe（约 65MB），内置 Chromium + Node，启动无需任何外部运行时
- 🎨 **dsh web 同款视觉** — dsw 设计系统：背景极光、毛玻璃卡片、脉冲状态点、按钮辉光、不定进度条
- ⚡ **一键启动** — 未安装 dsh 时自动引导（目录选择 → 安装 → 启动），已安装则直接拉起服务并打开浏览器
- 🕊️ **独立运行** — dsh 以独立进程运行，关闭启动器窗口**不会**停止 dsh；再次点启动（已在运行）只是重新打开浏览器
- 🛑 **停止 dsh** — 按配置端口定位进程并结束（窗口「停止」按钮 / `stop` 命令）
- 🔀 **挪动 dsh** — 把已安装的 dsh 包挪到任意路径（跨盘自动复制+删除）；dsh 运行中时拒绝移动
- 🔍 **环境状态** — Node.js / npm / dsh 版本、安装状态、端口健康度
- 🔔 **升级检测** — 启动时与点「检查更新」都会比对 dsh（npm registry 的 `@deepseek-ai/dsh`）与启动器自身（GitHub Release 最新版）
- 📜 **实时日志** — 安装与启动输出实时显示在窗口内（SSE 推送）
- ⌨️ **命令行备用** — `install` / `move` / `start` / `stop` / `status` / `check-update` / `--version` / `--help` 照常可用
- 🧩 **插件生态** — 以 git 子模块携带 [dsh-plugins](dsh-plugins/) 插件合集（当前 v0.5.0）

## 环境要求

- **Windows 10 / 11**（x64）
- 目标机器需安装 **Node.js**：`^22.19 || >=24`（仅在运行 dsh 时需要；启动器本身自包含）
- 目标机器无需 Go、无需 npm 全局配置、无需环境变量

## 下载

从 [Releases 页面](https://github.com/kuaizhongqiang/dsh-launcher/releases) 获取最新 `dsh-launcher.exe`。每次发布由 GitHub Actions 在 `windows-latest` 上自动构建并附带 exe 发布。

## 使用

### 桌面窗口（双击，推荐）

1. 双击 `dsh-launcher.exe`（或命令行无参运行）→ 弹出桌面窗口。
2. 查看"环境状态"卡片（Node / npm / dsh / 端口）。
3. 点击**启动**：
   - 已安装 dsh → 启动服务（已在运行则直接打开浏览器），按钮变为"已运行"
   - 未安装 → 先点**浏览…**选择安装目录（或直接输入），再点**启动**即可自动安装并启动
4. 需要挪动 dsh 时，点**移动**并选择目标目录（dsh 运行中会拒绝）。
5. 点**停止**结束 dsh；点 **×** 只关闭窗口——dsh 继续在后台运行。
6. 点**检查更新**可立即比对 dsh 与启动器的最新版本。

> 设 `DSH_LAUNCHER_NO_BROWSER=1` 可跳过自动打开浏览器。

### 命令行

```powershell
dsh-launcher.exe install [--dir <目录>]   # 安装 @deepseek-ai/dsh（默认 %LOCALAPPDATA%\dsh）
dsh-launcher.exe move --dir <目录>        # 把已安装的 dsh 挪到新路径（运行中拒绝）
dsh-launcher.exe start [--no-browser]     # 确保 dsh 运行并打开浏览器（幂等）
dsh-launcher.exe stop                     # 停止 dsh（结束监听端口的进程）
dsh-launcher.exe status                   # 显示安装目录 / 版本 / 运行状态
dsh-launcher.exe check-update             # 检查升级：dsh（npm）与启动器（GitHub Release）
dsh-launcher.exe --version                # 显示版本
dsh-launcher.exe --help
```

> dsh **独立运行**：`start` 在 dsh 就绪后即返回，启动器退出不影响 dsh；用 `stop` 停止。

日志写入 `%TEMP%\dsh-launcher.log`（窗口版无控制台，以此为准）。

## 配置（launcher.json）

首次 `install` 时生成在 **exe 同目录**（便携：exe 与配置一起拷走）：

```json
{
  "dshInstallDir": "C:\\Users\\<user>\\AppData\\Local\\dsh",
  "dshVersion": "0.1.0-rc.7",
  "port": 3080,
  "installedAt": "2026-08-20T20:08:35+08:00"
}
```

- **绝不触碰** `~/.dsh`（`DSH_HOME`）— 现有 profile / 插件 / 会话保持原样。
- 新机器上运行 `install` 会覆盖配置中的安装路径。

## 插件生态（dsh-plugins）

dsh 本身支持插件：插件放在 `%DSH_HOME%\profiles\web\plugins\`，通过 profile 的
`cordis.patch.yml` 挂载。本仓库以 **git 子模块**方式携带社区插件合集（当前 **v0.5.0**）：

- 本地检出：[`dsh-plugins/`](dsh-plugins/)
- GitHub：[kuaizhongqiang/dsh-plugins](https://github.com/kuaizhongqiang/dsh-plugins)

合集里每个插件包都自包含（`install.ps1` + `plugins/`），并配套一个**安装技能**
（`skills/install-<name>/SKILL.md`），告诉 dsh 的 Agent 如何一步步安装。

更新子模块：

```powershell
git submodule update --remote dsh-plugins
git add dsh-plugins && git commit -m "chore: bump dsh-plugins submodule"
```

> dsh-launcher **绝不触碰** `~/.dsh`（`DSH_HOME`）：插件的安装与使用与启动器完全独立。

## 从源码构建

需要 Node.js 22+（开发机）。无 Go 依赖。

```powershell
npm ci                      # 安装开发依赖（esbuild / electron / electron-builder）
npm run check               # TypeScript 类型检查
npm run build               # esbuild 打包（dist/launcher.cjs + electron-main + preload）
npm run dist                # electron-builder 打包单文件：release\dsh-launcher.exe
```

开发模式直接跑桌面窗口：

```powershell
npm run start:desktop       # npx electron .（本地窗口，走 dev 逻辑）
npm run start               # 纯 Node CLI（node dist/launcher.cjs）
```

> **体积敏感备选（Node SEA）**：`npm run dist:sea` 用 Node 单文件可执行（约 87MB，内嵌 Node 运行时，但界面需浏览器打开）——保留为备选路线，默认交付为 Electron portable。

## 技术架构

```
src/                          TS 逻辑（与语言无关，Node/Electron 通用）
├── config.ts                 launcher.json 读写（便携：跟随 exe）
├── node.ts                   node/npm 探测、registry 镜像策略、安装
├── install.ts                install / move 流程
├── launch.ts                 start/stop（独立进程、端口定位）
├── update.ts                 升级检测（npm registry + GitHub Release）
├── server.ts                 node:http 本地服务（UI 静态资源 + REST bridge + SSE 日志）
├── cli.ts                    命令行入口
├── electron-main.ts          Electron 主进程（窗口 + CLI 双模式）
└── electron-preload.ts       窗口控制桥（最小化）
ui/                           Web 界面（dsh web 同款 dsw 设计系统）
├── tokens.css                从 @deepseek-ai/dsh-client-ui-theme 提取的设计令牌（脚本可再生成）
├── launcher.css              界面样式（只消费令牌）
├── index.html / app.js       页面 + 逻辑（bridge 注入，模拟/真实自动切换）
scripts/
├── build.mjs                 esbuild 打包
├── build-sea.mjs             Node SEA 备选构建
└── extract-dsw-tokens.mjs    重新提取 dsw 设计令牌
```

## 设计要点

| 决策 | 理由 |
|---|---|
| TS/JS 与 dsh 同栈 | 界面直接消费 dsw 设计系统；逻辑可复用/可插件化；单一语言维护 |
| Electron portable 单文件 | 满足"单一文件 + 启动零依赖 + 桌面窗口"；内置 Chromium + Node |
| UI 走本地 http 服务 | 主进程 `node:http` 服务 + BrowserWindow 加载；浏览器/桌面窗口形态共用一套前端 |
| frameless 自绘标题栏 | 与 dsh 风格统一，自绘拖动区/关闭/最小化 |
| `PORTABLE_EXECUTABLE_DIR` | electron-builder portable 提供 exe 所在目录，配置跟随 exe（便携） |
| dsh 独立进程 | 启动器退出后 dsh 继续运行；按端口定位停止 |
| npm 安装流式输出 | 每行经日志订阅推送到窗口（SSE），实时可见 |
| SEA 备选保留 | 体积敏感场景可退化为内嵌 Node 的单文件（界面走浏览器） |

## License

[MIT](LICENSE)
