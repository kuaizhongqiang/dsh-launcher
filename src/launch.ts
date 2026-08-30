// launch.ts —— dsh 的启动与停止。
// 移植自 Go internal/launch。
// 生命周期设计（v0.4.0 起）：启动器常驻，dsh server 作为启动器的子进程运行，
// 继承启动器的（隐藏）控制台 —— dsh 内部 spawn 的 pwsh 因此继承隐藏控制台，
// 不再每次弹一个可见 PowerShell 窗口。
// 停止 = 直接结束子进程（不再按端口找 PID）；启动器退出时自动停止 dsh。

import { spawn, execFileSync, type ChildProcess } from 'node:child_process';
import { openSync, closeSync, existsSync, statSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import * as consoleWin from './console.js';
import * as log from './log.js';
import * as node from './node.js';
import { openURL } from './win.js';
import type { Config } from './config.js';

const readyTimeoutMs = 60_000;
const pollIntervalMs = 500;
const httpTimeoutMs = 2000;

/** 当前由本启动器持有的 dsh 子进程（未启动时为 null）。 */
let child: ChildProcess | null = null;

/** dsh 子进程 stdout/stderr 的落盘路径。 */
export function childLogPath(): string {
  return join(tmpdir(), 'dsh-launcher-child.log');
}

/** 返回配置端口对应的访问地址。 */
export function url(cfg: Config): string {
  return `http://127.0.0.1:${cfg.port}/`;
}

/**
 * dsh v0.1.2+ 启动时会打印一次带进程 token 的访问 URL（`dsh web: http://.../?token=...`）。
 * 浏览器必须通过该 URL 才能换取登录 cookie（直接访问无 token 的 / 会 401）。
 * 从子进程日志中取**最新**一条带 token 的 URL；找不到（旧版 dsh）返回 undefined。
 */
export function tokenUrlFromLog(): string | undefined {
  try {
    const text = readFileSync(childLogPath(), 'utf8');
    const re = /(https?:\/\/[^\s"'<>]+?\?token=[A-Za-z0-9_-]+)/g;
    const matches = [...text.matchAll(re)];
    return matches.length === 0 ? undefined : matches[matches.length - 1][1];
  } catch {
    return undefined;
  }
}

/**
 * 等待子进程日志出现带 token 的 URL：dsh 端口就绪（launcher 的轮询命中）
 * 与 dsh 打印 URL 之间有几毫秒到几百毫秒的间隔，直接读可能拿不到。
 * 轮询等待，超时后返回最后一次尝试结果（可能 undefined，回退普通 URL）。
 */
async function waitForTokenUrl(timeoutMs: number): Promise<string | undefined> {
  const deadline = Date.now() + timeoutMs;
  let last: string | undefined;
  while (Date.now() < deadline) {
    last = tokenUrlFromLog();
    if (last) return last;
    await new Promise((r) => setTimeout(r, 200));
  }
  return last;
}

/** 打开浏览器：优先带 token 的 URL（新版自动登录），否则普通 URL（旧版兼容）。 */
async function openAccessUrl(cfg: Config): Promise<void> {
  const u = url(cfg);
  const tokenUrl = await waitForTokenUrl(5_000);
  const target = tokenUrl ?? u;
  try {
    await openURL(target);
    log.info(tokenUrl
      ? `已在默认浏览器打开带 token 的访问地址（自动登录）：${target}`
      : `已在默认浏览器打开 ${u}`);
  } catch (e) {
    log.warn(`打开浏览器失败：${(e as Error).message}（可手动访问 ${target}）`);
  }
}

/** 校验配置中的安装目录可用（bin.js 存在且可执行）。 */
export async function verifyInstall(cfg: Config): Promise<void> {
  if (!cfg.dshInstallDir) {
    throw new Error('未检测到已安装的 dsh：请先安装');
  }
  const bin = node.dshBinPath(cfg.dshInstallDir, cfg.source);
  if (!existsSync(bin)) {
    throw new Error(`dsh 安装目录损坏：找不到 ${bin}，请重新安装`);
  }
  try {
    const ver = await node.dshVersion(cfg.dshInstallDir, cfg.source);
    log.info(`dsh 版本校验通过：${ver}`);
  } catch (e) {
    // 回退：package.json 存在即视为可用（部分版本 bin.js 可能不响应 --version）
    try {
      const ver = node.dshVersionFromPackage(cfg.dshInstallDir, cfg.source);
      log.warn(`bin.js --version 未通过（${(e as Error).message}），按 package.json 版本 ${ver} 继续`);
    } catch (verErr) {
      throw new Error(`dsh 校验失败（${(e as Error).message}），请重新安装`);
    }
  }
}

/**
 * 启动 dsh（作为本进程子进程，继承隐藏控制台）：
 *   - dsh 已在运行（端口有响应）→ 不重复启动，直接打开浏览器，返回 alreadyRunning=true
 *   - 未运行 → 启动 dsh 子进程，等待就绪（子进程提前退出则立即失败），打开浏览器
 */
export async function start(cfg: Config, noBrowser: boolean): Promise<boolean> {
  await verifyInstall(cfg);
  const u = url(cfg);

  if (await isRunning(cfg)) {
    log.info(`dsh 已在运行：${u}`);
    if (!noBrowser) {
      await openAccessUrl(cfg);
    }
    return true;
  }

  // 持有隐藏控制台：让 dsh（及它的 pwsh 子进程）继承，避免弹窗。
  // 注意：必须在 spawn 之前调用，且 spawn 不能带 windowsHide（否则子进程
  // 拿不到控制台，pwsh 又会各自弹新窗口）。
  // 若无法提供隐藏控制台（koffi 不可用等），退回 windowsHide 防 node 自身弹窗。
  const hasHiddenConsole = consoleWin.ensureHiddenConsole();
  const windowsHide = !hasHiddenConsole;

  const bin = node.dshBinPath(cfg.dshInstallDir, cfg.source);

  // 子进程输出写入日志文件
  const childLog = openSync(childLogPath(), 'a');

  await new Promise<void>((resolve, reject) => {
    // 显式传 --port：dsh web 监听 launcher.json 配置的端口（而非 dsh 自己的默认值）；
    // 传 --no-open：浏览器由 launcher 统一控制打开，避免 dsh 自开一次 + launcher 再开一次。
    const args = [bin, 'web', '--port', String(cfg.port), '--no-open'];
    const cp = spawn('node', args, {
      // 不 detached：作为本进程子进程，继承（隐藏）控制台。
      // stdio 指向日志文件（无控制台时绝不能继承无效句柄）。
      stdio: ['ignore', childLog, childLog],
      windowsHide,
    });
    cp.on('error', (e) => {
      closeSync(childLog);
      reject(new Error(`启动 dsh 失败：${e.message}`));
    });
    cp.on('spawn', () => {
      child = cp;
      log.info(`dsh 子进程已启动（PID ${cp.pid}）：node ${args.join(' ')}`);
      resolve();
    });
  });

  // 等待就绪；子进程提前退出 → 立即失败（不用干等超时）
  log.info(`等待 ${u} 就绪（超时 60s）……`);
  const ready = await waitReadyOrChildExit(u, readyTimeoutMs, pollIntervalMs);
  closeSync(childLog);
  if (!ready) {
    const reason = childExited ? 'dsh 进程提前退出' : '等待超时（60s）';
    throw new Error(`${reason}，子进程日志：${childLogPath()}`);
  }
  log.info(`${u} 已就绪`);

  if (!noBrowser) {
    await openAccessUrl(cfg);
  }

  log.info(`dsh 已启动并绑定启动器：${u}（关闭启动器将同时停止 dsh）`);
  return false;
}

/** 兼容旧名：startDetached → start（语义已变：子进程绑定，非独立进程）。 */
export const startDetached = start;

/** 子进程是否已提前退出（waitReady 用）。 */
let childExited = false;

/** 结束当前持有的 dsh 子进程（无则按端口回退）。 */
export async function stop(cfg: Config): Promise<void> {
  if (child && !child.killed) {
    const pid = child.pid;
    if (pid) {
      log.info(`结束 dsh 子进程 PID ${pid}……`);
      try {
        await killPID(pid);
      } catch (e) {
        log.warn(`结束 PID ${pid} 失败：${(e as Error).message}`);
      }
    }
    child = null;
    return;
  }
  // 回退：按端口定位（例如上次启动器异常退出遗留的独立进程）
  const pids = await findPIDsByPort(cfg.port);
  if (pids.length === 0) {
    throw new Error(`端口 ${cfg.port} 上没有监听中的进程，dsh 可能未在运行`);
  }
  for (const pid of pids) {
    log.info(`结束进程 PID ${pid}……`);
    try {
      await killPID(pid);
    } catch (e) {
      log.warn(`结束 PID ${pid} 失败：${(e as Error).message}`);
    }
  }
}

/** 启动器退出时调用：静默结束子进程（不抛错）。退出钩子里不能异步，用同步 taskkill。 */
export function stopChildSilently(): void {
  const pid = child?.pid;
  child = null;
  if (!pid) return;
  try {
    execFileSync('taskkill', ['/F', '/T', '/PID', String(pid)], { windowsHide: true, stdio: 'ignore' });
  } catch {
    // 进程可能已退出，忽略
  }
}

/** 用 netstat 找监听端口的 PID。 */
export async function findPIDsByPort(port: number): Promise<number[]> {
  const out = await node.runNoWindow('netstat', ['-ano']);
  const marker = `:${port}`;
  const pids: number[] = [];
  for (const raw of out.split(/\r?\n/)) {
    const line = raw.trim();
    if (!line.includes(marker) || !line.includes('LISTENING')) continue;
    const fields = line.split(/\s+/);
    const last = fields[fields.length - 1];
    const pid = Number(last);
    if (Number.isInteger(pid) && pid > 0) pids.push(pid);
  }
  return pids;
}

/** 强制结束进程（taskkill /F /T 连带进程树）。以「进程是否真的消失」为准，不解析本地化输出。 */
async function killPID(pid: number): Promise<void> {
  await node.runNoWindow('taskkill', ['/F', '/T', '/PID', String(pid)]);
  for (let i = 0; i < 20; i++) {
    try {
      process.kill(pid, 0);
    } catch {
      return; // 进程已消失
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`taskkill 未能结束 PID ${pid}（进程仍在）`);
}

/** 轮询 url 直到有 HTTP 响应；持有子进程时若其提前退出则立即返回 false。 */
async function waitReadyOrChildExit(url: string, timeoutMs: number, pollMs: number): Promise<boolean> {
  childExited = false;
  const deadline = Date.now() + timeoutMs;
  let exitUnsub: (() => void) | null = null;
  if (child) {
    exitUnsub = onChildExit(() => {
      childExited = true;
      log.warn(`dsh 子进程提前退出（exit code ${child?.exitCode ?? '?'}），见 ${childLogPath()}`);
    });
  }
  try {
    while (Date.now() < deadline) {
      if (childExited) return false;
      try {
        const ctrl = new AbortController();
        const timer = setTimeout(() => ctrl.abort(), httpTimeoutMs);
        try {
          const resp = await fetch(url, { signal: ctrl.signal });
          resp.body?.cancel().catch(() => {});
          if (resp.status >= 200) return true;
        } finally {
          clearTimeout(timer);
        }
      } catch {
        // 未就绪，继续轮询
      }
      await new Promise((r) => setTimeout(r, pollMs));
    }
    return false;
  } finally {
    exitUnsub?.();
  }
}

/** 订阅子进程退出（返回取消函数）。 */
function onChildExit(fn: () => void): () => void {
  if (!child) return () => {};
  child.once('exit', fn);
  return () => {
    child?.removeListener('exit', fn);
  };
}

/** 报告配置端口当前是否有服务响应。 */
export async function isRunning(cfg: Config): Promise<boolean> {
  try {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), httpTimeoutMs);
    try {
      const resp = await fetch(url(cfg), { signal: ctrl.signal });
      resp.body?.cancel().catch(() => {});
      return resp.status >= 200;
    } finally {
      clearTimeout(timer);
    }
  } catch {
    return false;
  }
}

/** 安装目录是否存在（辅助状态展示）。 */
export function installDirExists(cfg: Config): boolean {
  if (!cfg.dshInstallDir) return false;
  try {
    return statSync(cfg.dshInstallDir).isDirectory();
  } catch {
    return false;
  }
}
