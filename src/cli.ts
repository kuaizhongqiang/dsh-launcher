// cli.ts —— 命令行入口：install / start / stop / move / status / check-update / ui。
// 移植自 Go main.go。无参数（双击 exe）→ 启动 Web UI 并打开浏览器。

import * as config from './config.js';
import * as install from './install.js';
import * as launch from './launch.js';
import * as log from './log.js';
import * as node from './node.js';
import { DefaultUIPort, startServer } from './server.js';
import * as update from './update.js';
import { VERSION } from './version.js';
import { openURL } from './win.js';

const usage = `dsh-launcher — dsh (@deepseek-ai/dsh) 本机安装 / 启动引导器（TS/JS 版）

用法：
  dsh-launcher.exe                  无参数（双击）→ 启动 Web UI 并打开浏览器：
                                     状态展示 + 路径选择 + 安装 + 一键启动
  dsh-launcher.exe install [--dir <目录>] [--registry <url>] [--mirror] [--no-mirror]
                                     命令行安装 @deepseek-ai/dsh
                                     默认安装目录 %LOCALAPPDATA%\\dsh
  dsh-launcher.exe move --dir <目录> 把已安装的 dsh 挪到新路径
                                    （跨盘自动复制+删除，运行中禁止）
  dsh-launcher.exe start [--no-browser]  确保 dsh 运行并打开浏览器
  dsh-launcher.exe stop             停止 dsh（按配置端口结束进程）
  dsh-launcher.exe status           显示安装目录、版本与运行状态
  dsh-launcher.exe check-update     检查升级：dsh（npm registry）与启动器（GitHub Release）
  dsh-launcher.exe ui [--no-browser] [--port <端口>]  启动 Web UI 服务（默认端口 ${DefaultUIPort}）
  dsh-launcher.exe --version | -v   显示版本
  dsh-launcher.exe --help | -h      显示本帮助

说明：
  - 配置 launcher.json 与 exe 同目录，跟随 exe 走（便携）
  - 运行日志写入 %TEMP%\\dsh-launcher.log（GUI 构建无控制台输出，以此为准）
  - dsh 以独立进程运行，不绑定启动器：关闭本窗口/退出不影响 dsh，用「停止」结束
  - 国内下载加速：install 自动探测 npm 官方源与 npmmirror 镜像的延迟，
    官方源不可达或明显更慢时自动使用镜像（可 --no-mirror 关闭）；
    也可用环境变量 DSH_LAUNCHER_NPM_REGISTRY / DSH_LAUNCHER_NPM_MIRROR /
    DSH_LAUNCHER_PREFER_MIRROR=1 / DSH_LAUNCHER_NO_MIRROR=1 控制
`;

function fail(msg: string): never {
  log.error(msg);
  process.exit(1);
}

/** 解析 install 子命令参数。 */
function parseInstallArgs(rest: string[]): { dir: string; spec: node.RegistrySpec } {
  const spec: node.RegistrySpec = {};
  let dir = '';
  for (let i = 0; i < rest.length; i++) {
    switch (rest[i]) {
      case '--dir':
      case '-d':
        if (i + 1 >= rest.length) fail('--dir 缺少参数');
        dir = rest[++i];
        break;
      case '--registry':
        if (i + 1 >= rest.length) fail('--registry 缺少参数');
        spec.registry = rest[++i];
        break;
      case '--mirror':
        spec.preferMirror = true;
        break;
      case '--no-mirror':
        spec.disableAutoSwitch = true;
        break;
      default:
        fail(`install 未知参数：${rest[i]}`);
    }
  }
  return { dir, spec };
}

/** 解析 move 子命令参数。 */
function parseMoveArgs(rest: string[]): string {
  let dir = '';
  for (let i = 0; i < rest.length; i++) {
    if (rest[i] === '--dir' || rest[i] === '-d') {
      if (i + 1 >= rest.length) fail('--dir 缺少参数');
      dir = rest[++i];
    } else {
      fail(`move 未知参数：${rest[i]}`);
    }
  }
  if (!dir) fail('move 需要 --dir <目标路径>');
  return dir;
}

async function runInstall(rest: string[]): Promise<void> {
  const { dir, spec } = parseInstallArgs(rest);
  await install.run(dir, install.registrySpecFromEnv(spec));
  log.info('安装完成。现在可以运行 dsh-launcher.exe（或 dsh-launcher.exe start）启动 dsh。');
}

async function runMove(rest: string[]): Promise<void> {
  const dir = parseMoveArgs(rest);
  await install.move(dir);
  log.info('移动完成。');
}

async function runStart(rest: string[]): Promise<void> {
  const noBrowser = rest.includes('--no-browser');
  const cfg = config.load();
  if (!cfg || !config.isInstalled(cfg)) {
    fail('未找到 launcher.json：请先运行 dsh-launcher.exe install 完成安装');
  }
  await launch.startDetached(cfg, noBrowser);
  log.info('dsh 已独立运行（启动器退出不影响 dsh）。');
}

async function runStop(): Promise<void> {
  const cfg = config.load();
  if (!cfg || !config.isInstalled(cfg)) {
    fail('未找到 launcher.json：请先运行 dsh-launcher.exe install 完成安装');
  }
  await launch.stop(cfg);
  log.info('停止完成。');
}

async function runStatus(): Promise<void> {
  const cfg = config.load();
  if (!cfg || !config.isInstalled(cfg)) {
    console.log('状态：未安装（缺少 launcher.json 或未记录安装目录）');
    console.log('请先运行：dsh-launcher.exe install');
    return;
  }
  console.log(`安装目录：${cfg.dshInstallDir}`);
  console.log(`dsh 版本：${cfg.dshVersion ?? '-'}`);
  console.log(`端口：${cfg.port}`);
  console.log(`安装时间：${cfg.installedAt ?? '-'}`);

  if (!launch.installDirExists(cfg)) {
    console.log('运行状态：未就绪（安装目录不存在，请重新 install）');
    return;
  }
  if (await launch.isRunning(cfg)) {
    console.log(`运行状态：运行中（端口 ${cfg.port} 有响应）`);
  } else {
    console.log(`运行状态：未运行（端口 ${cfg.port} 无响应）`);
  }
}

async function runCheckUpdate(): Promise<void> {
  const cfg = config.load();
  const dshCur = cfg && config.isInstalled(cfg) ? cfg.dshVersion : '';
  const spec = install.registrySpecFromConfig(cfg);
  if (!dshCur) {
    console.log('dsh：未安装（先运行 dsh-launcher.exe install）');
  } else {
    try {
      const { latest, hasUpdate } = await update.checkDsh(dshCur, spec);
      if (hasUpdate) {
        console.log(`dsh：当前 ${dshCur} → 最新 ${latest}（可升级，运行 install 即升级到最新）`);
      } else {
        console.log(`dsh：当前 ${dshCur} 已是最新`);
      }
    } catch (e) {
      console.log(`dsh：升级检测失败：${(e as Error).message}`);
    }
  }
  try {
    const { latest, hasUpdate } = await update.checkLauncher(VERSION);
    if (hasUpdate) {
      console.log(`启动器：当前 v${VERSION} → 最新 ${latest}（可升级，下载页 ${update.LauncherReleaseURL}）`);
    } else {
      console.log(`启动器：当前 v${VERSION} 已是最新`);
    }
  } catch (e) {
    console.log(`启动器：升级检测失败：${(e as Error).message}`);
  }
}

/** 启动 Web UI 服务；端口占用时顺延尝试。 */
async function runUI(rest: string[]): Promise<void> {
  const noBrowser = rest.includes('--no-browser');
  let port = DefaultUIPort;
  const pi = rest.indexOf('--port');
  if (pi >= 0 && pi + 1 < rest.length) {
    port = Number(rest[pi + 1]);
    if (!Number.isInteger(port) || port <= 0 || port > 65535) fail(`非法端口：${rest[pi + 1]}`);
  }
  if (rest.some((a) => a === '--help' || a === '-h')) {
    console.log(usage);
    return;
  }

  // 端口占用则顺延（最多试 10 个）
  let server: Awaited<ReturnType<typeof startServer>> | null = null;
  for (let p = port; p < port + 10; p++) {
    try {
      server = await startServer(p);
      break;
    } catch (e) {
      log.warn(`端口 ${p} 不可用（${(e as Error).message}），尝试下一个……`);
    }
  }
  if (!server) {
    fail(`启动 UI 服务失败：端口 ${port}–${port + 9} 均不可用`);
  }

  log.info('dsh-launcher Web UI 已就绪。关闭浏览器不会停止本服务；用「退出」按钮或结束进程停止。');
  if (!noBrowser) {
    try {
      await openURL(server.url);
      log.info(`已在默认浏览器打开 ${server.url}`);
    } catch (e) {
      log.warn(`打开浏览器失败：${(e as Error).message}（可手动访问 ${server.url}）`);
    }
  }
  // 服务保持运行（进程不退出）
  await new Promise<void>(() => {});
}

export async function dispatch(args: string[]): Promise<void> {
  if (args.length === 0) {
    await runUI([]);
    return;
  }
  switch (args[0]) {
    case '--help':
    case '-h':
    case 'help':
      log.raw(usage);
      return;
    case '--version':
    case '-v':
      log.raw(`dsh-launcher v${VERSION}\n`);
      return;
    case 'install':
      await runInstall(args.slice(1));
      return;
    case 'move':
      await runMove(args.slice(1));
      return;
    case 'start':
      await runStart(args.slice(1));
      return;
    case 'stop':
      await runStop();
      return;
    case 'status':
      await runStatus();
      return;
    case 'check-update':
      await runCheckUpdate();
      return;
    case 'ui':
      await runUI(args.slice(1));
      return;
    default:
      log.raw(usage);
      fail(`未知命令：${args[0]}`);
  }
}
