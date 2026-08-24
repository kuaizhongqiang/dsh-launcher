// electron-main.ts —— Electron 桌面窗口入口。
// 无参数（双击 exe）→ 启动本地 UI 服务 + 打开桌面窗口（frameless，加载 http://127.0.0.1:<port>/）。
// 带 CLI 参数 → 与命令行版一致（install/start/stop/move/status/check-update）。

import { app, BrowserWindow, ipcMain, shell } from 'electron';
import { dirname, join } from 'node:path';

import { dispatch } from './cli.js';
import * as consoleWin from './console.js';
import * as launch from './launch.js';
import * as log from './log.js';
import { DefaultUIPort, startServer } from './server.js';

// esbuild 以 CJS 输出：__dirname 是真实模块目录变量（避免 import.meta.url 在 asar 下失效）
declare const __dirname: string;

// ---------- CLI 命令集合 ----------

const KNOWN_COMMANDS = new Set([
  'install', 'start', 'stop', 'move', 'status', 'check-update', 'ui',
  '--help', '-h', 'help', '--version', '-v',
]);

/** 清洗 argv：去掉 exe 路径、可能的 app 路径（dev 模式 `electron .`）与 Chromium 开关。 */
function cleanArgs(raw: string[]): string[] {
  const args = raw.slice(1).filter((a) => {
    // Chromium/Electron 开关（--remote-debugging-port 等）会出现在 process.argv 里，
    // 但不是启动器命令，忽略之（保留 --help/-h/--version/-v 等已知命令）。
    return !(a.startsWith('-') && !KNOWN_COMMANDS.has(a));
  });
  if (args.length > 0 && !args[0].startsWith('-') && !KNOWN_COMMANDS.has(args[0])) {
    args.shift(); // dev: `electron . args...` 的 app 路径
  }
  return args;
}

/** 窗口模式：启动服务 + 创建 frameless 桌面窗口。 */
async function runDesktop(): Promise<void> {
  await app.whenReady();
  log.initLog();
  log.setDebug(process.env.DSH_LAUNCHER_DEBUG !== undefined);

  // 持有隐藏控制台：dsh 作为子进程继承后，其 pwsh 子进程不再弹窗。
  // 必须尽早调用（在 /api/start 真正 spawn 之前）。
  consoleWin.ensureHiddenConsole();

  // 启动器常驻：关闭窗口/退出时自动停止 dsh 子进程
  app.on('before-quit', () => {
    launch.stopChildSilently();
  });
  process.on('exit', () => {
    launch.stopChildSilently();
  });

  // 端口占用则顺延（最多试 10 个）
  let server: Awaited<ReturnType<typeof startServer>> | null = null;
  for (let p = DefaultUIPort; p < DefaultUIPort + 10; p++) {
    try {
      server = await startServer(p);
      break;
    } catch (e) {
      log.warn(`端口 ${p} 不可用（${(e as Error).message}），尝试下一个……`);
    }
  }
  if (!server) {
    log.error('启动本地 UI 服务失败');
    app.exit(1);
    return;
  }
  log.info(`dsh-launcher 桌面窗口已启动：${server.url}`);

  // 便携版提示：electron-builder portable 会把 app 解压到临时目录运行、退出即删，
  // 此时从任务栏固定得到的快捷方式指向临时路径，关闭后会失效（“快捷方式丢失”）。
  // 需要固定到任务栏请使用安装版（NSIS）。
  const exeDir = dirname(process.execPath);
  const portableDir = process.env.PORTABLE_EXECUTABLE_DIR;
  if (portableDir && portableDir.toLowerCase() !== exeDir.toLowerCase()) {
    log.warn('便携版运行在临时解压目录，任务栏固定会在退出后失效；如需固定请使用安装版（NSIS）。');
  }

  const win = new BrowserWindow({
    width: 720,
    height: 600,
    frame: false, // 无边框：自绘标题栏（与 Go 版一致）
    resizable: false,
    show: false,
    backgroundColor: '#151517',
    webPreferences: {
      preload: join(__dirname, 'electron-preload.cjs'),
      contextIsolation: true,
      nodeIntegration: false,
    },
  });

  // 外链一律走系统浏览器（不占用窗口）
  win.webContents.setWindowOpenHandler(({ url }) => {
    void shell.openExternal(url);
    return { action: 'deny' };
  });

  // ready-to-show 必须在 loadURL 之前注册：本地 UI 是内嵌内存资源、响应极快，
  // 事件可能在 loadURL 的 Promise resolve 前就已触发，注册晚了窗口永不显示。
  win.once('ready-to-show', () => win.show());

  await win.loadURL(server.url);
  // 兜底：若 ready-to-show 已错过（页面早已渲染完成），直接显示。
  if (!win.isVisible()) win.show();

  win.on('closed', () => app.quit());
}

/** 窗口控制 IPC（最小化等），供 preload 使用。 */
ipcMain.on('win:minimize', (e) => {
  BrowserWindow.fromWebContents(e.sender)?.minimize();
});

ipcMain.on('win:close', (e) => {
  BrowserWindow.fromWebContents(e.sender)?.close();
});

// ---------- 入口 ----------

const args = cleanArgs(process.argv);

if (args.length > 0 && args[0] !== 'ui') {
  // CLI 模式：不需要窗口，直接跑命令后退出
  log.initLog();
  log.setDebug(process.env.DSH_LAUNCHER_DEBUG !== undefined);
  // CLI 同样先持有隐藏控制台（start 时 dsh 子进程继承，不弹窗）
  consoleWin.ensureHiddenConsole();
  process.on('exit', () => {
    launch.stopChildSilently();
  });
  void (async () => {
    try {
      await dispatch(args);
    } catch (e) {
      log.error(e instanceof Error ? e.message : String(e));
      process.exitCode = 1;
    }
    app.exit(typeof process.exitCode === 'number' ? process.exitCode : 0);
  })();
} else {
  // 无参数或 `ui` → 桌面窗口
  void runDesktop().catch((e) => {
    log.error(`桌面窗口启动失败：${e instanceof Error ? e.message : String(e)}`);
    app.exit(1);
  });
}
