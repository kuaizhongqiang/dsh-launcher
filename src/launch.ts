// launch.ts —— dsh 的启动与停止。
// 移植自 Go internal/launch。
// 生命周期设计：dsh server 以独立进程运行，不绑定启动器。
// 启动器负责：确保 dsh 运行 → 打开浏览器 → 退出（dsh 继续服务）。
// 停止 dsh 通过按端口定位进程并结束。

import { spawn } from 'node:child_process';
import { openSync, closeSync, existsSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import * as log from './log.js';
import * as node from './node.js';
import { openURL } from './win.js';
import type { Config } from './config.js';

const readyTimeoutMs = 60_000;
const pollIntervalMs = 500;
const httpTimeoutMs = 2000;

/** dsh 子进程 stdout/stderr 的落盘路径。 */
export function childLogPath(): string {
  return join(tmpdir(), 'dsh-launcher-child.log');
}

/** 返回配置端口对应的访问地址。 */
export function url(cfg: Config): string {
  return `http://127.0.0.1:${cfg.port}/`;
}

/** 校验配置中的安装目录可用（bin.js 存在且可执行）。 */
export async function verifyInstall(cfg: Config): Promise<void> {
  if (!cfg.dshInstallDir) {
    throw new Error('未检测到已安装的 dsh：请先安装');
  }
  const bin = node.dshBinPath(cfg.dshInstallDir);
  if (!existsSync(bin)) {
    throw new Error(`dsh 安装目录损坏：找不到 ${bin}，请重新安装`);
  }
  try {
    const ver = await node.dshVersion(cfg.dshInstallDir);
    log.info(`dsh 版本校验通过：${ver}`);
  } catch (e) {
    // 回退：package.json 存在即视为可用（部分版本 bin.js 可能不响应 --version）
    try {
      const ver = node.dshVersionFromPackage(cfg.dshInstallDir);
      log.warn(`bin.js --version 未通过（${(e as Error).message}），按 package.json 版本 ${ver} 继续`);
    } catch (verErr) {
      throw new Error(`dsh 校验失败（${(e as Error).message}），请重新安装`);
    }
  }
}

/**
 * 确保 dsh 运行（独立进程，不绑定本进程生命周期）：
 *   - dsh 已在运行（端口有响应）→ 不重复启动，直接打开浏览器，返回 alreadyRunning=true
 *   - 未运行 → 启动 dsh 子进程（detached），就绪后 detach 并打开浏览器
 */
export async function startDetached(cfg: Config, noBrowser: boolean): Promise<boolean> {
  await verifyInstall(cfg);
  const u = url(cfg);

  if (await isRunning(cfg)) {
    log.info(`dsh 已在运行：${u}`);
    if (!noBrowser) {
      try {
        await openURL(u);
        log.info(`已在默认浏览器打开 ${u}`);
      } catch (e) {
        log.warn(`打开浏览器失败：${(e as Error).message}（可手动访问 ${u}）`);
      }
    }
    return true;
  }

  const bin = node.dshBinPath(cfg.dshInstallDir);

  // 子进程输出写入日志文件（无控制台时绝不能继承无效句柄）
  const childLog = openSync(childLogPath(), 'a');

  await new Promise<void>((resolve, reject) => {
    // 以「隐藏控制台」方式启动 dsh：若让 node 无控制台（windowsHide: true），
    // 它 spawn 的 pwsh 子进程会各自弹出可见控制台窗口。改用 conhost
    // --headless 包一层，给 node 一个隐藏控制台，pwsh 子进程继承它，
    // 不再弹窗（不修改 dsh 本体）。
    const cp = spawn('conhost.exe', ['--headless', 'node', bin, 'web'], {
      detached: true,
      stdio: ['ignore', childLog, childLog],
    });
    cp.on('error', (e) => {
      closeSync(childLog);
      reject(new Error(`启动 dsh 失败：${e.message}`));
    });
    cp.on('spawn', () => {
      log.info(`dsh 子进程已启动（PID ${cp.pid}）：node ${bin} web`);
      // detached + unref：dsh 独立运行，本进程退出不影响它
      cp.unref();
      resolve();
    });
  });

  // 等待就绪
  log.info(`等待 ${u} 就绪（超时 60s）……`);
  const ready = await waitReady(u, readyTimeoutMs, pollIntervalMs);
  closeSync(childLog);
  if (!ready) {
    throw new Error(`等待 ${u} 超时（60s），子进程日志：${childLogPath()}`);
  }
  log.info(`${u} 已就绪`);

  if (!noBrowser) {
    try {
      await openURL(u);
      log.info(`已在默认浏览器打开 ${u}`);
    } catch (e) {
      log.warn(`打开浏览器失败：${(e as Error).message}（可手动访问 ${u}）`);
    }
  }

  log.info(`dsh 已启动并独立运行：${u}（关闭本程序不影响 dsh，可用 stop 停止）`);
  return false;
}

/** 结束配置端口上的 dsh 进程（按端口定位 PID 并强制结束）。 */
export async function stop(cfg: Config): Promise<void> {
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

/** 强制结束进程（taskkill /F）。taskkill 输出按系统语言编码（中文 GBK），
 * 直接解析文本会乱码误判；以「进程是否真的消失」为准。 */
async function killPID(pid: number): Promise<void> {
  await node.runNoWindow('taskkill', ['/F', '/PID', String(pid)]);
  // process.kill(pid, 0) 探测：进程消失抛 ESRCH，仍存活则不抛。
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

/** 轮询 url 直到有 HTTP 响应，返回是否就绪。 */
export async function waitReady(url: string, timeoutMs: number, pollMs: number): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
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
