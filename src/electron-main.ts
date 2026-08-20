// electron-main.ts —— Electron 桌面窗口入口。
// 无参数（双击 exe）→ 启动本地 UI 服务 + 打开桌面窗口（frameless，加载 http://127.0.0.1:<port>/）。
// 带 CLI 参数 → 与命令行版一致（install/start/stop/move/status/check-update）。

import { app, BrowserWindow, ipcMain, shell } from 'electron';
import { join } from 'node:path';

import { dispatch } from './cli.js';
import * as log from './log.js';
import { DefaultUIPort, startServer } from './server.js';

// esbuild 以 CJS 输出：__dirname 是真实模块目录变量（避免 import.meta.url 在 asar 下失效）
declare const __dirname: string;

// ---------- CLI 命令集合 ----------

const KNOWN_COMMANDS = new Set([
  'install', 'start', 'stop', 'move', 'status', 'check-update', 'ui',
  '--help', '-h', 'help', '--version', '-v',
]);

/** 清洗 argv：去掉 exe 路径与可能的 app 路径（dev 模式 `electron .`）。 */
function cleanArgs(raw: string[]): string[] {
  const args = raw.slice(1);
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

  await win.loadURL(server.url);
  win.once('ready-to-show', () => win.show());
  win.on('closed', () => app.quit());
}

/** 窗口控制 IPC（最小化等），供 preload 使用。 */
ipcMain.on('win:minimize', (e) => {
  BrowserWindow.fromWebContents(e.sender)?.minimize();
});

// ---------- 入口 ----------

const args = cleanArgs(process.argv);

if (args.length > 0 && args[0] !== 'ui') {
  // CLI 模式：不需要窗口，直接跑命令后退出
  log.initLog();
  log.setDebug(process.env.DSH_LAUNCHER_DEBUG !== undefined);
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
