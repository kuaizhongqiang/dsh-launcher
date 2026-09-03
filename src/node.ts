// node.ts —— Node.js / npm 探测与命令封装，以及 dsh 安装目录的路径约定。
// 移植自 Go internal/node。

import { spawn, execFile } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { compareSemver, parseSemver, type Semver } from './semver.js';
/** npm 包名。 */
export const DshPackage = '@deepseek-ai/dsh';

/** npm 官方源（registry="" 时由 npm 自身决定，默认官方源）。 */
export const DefaultNpmRegistry = 'https://registry.npmjs.org';

/** 国内镜像源（npmmirror，原淘宝镜像）。 */
export const DefaultNpmMirror = 'https://registry.npmmirror.com';

/** 安装时 npm registry 的选择策略。 */
export interface RegistrySpec {
  /** 显式指定主源（"" = npm 默认官方源）。 */
  registry?: string;
  /** 国内镜像（"" = DefaultNpmMirror）。 */
  mirror?: string;
  /** 强制用镜像，跳过速度探测与回退。 */
  preferMirror?: boolean;
  /** 关闭自动切换（主源不可达/明显慢于镜像时也不换镜像，失败也不回退）。 */
  disableAutoSwitch?: boolean;
}

/** 返回解析后的主源与镜像。 */
export function specEffective(s: RegistrySpec): { primary: string; mirror: string } {
  const primary = s.registry || DefaultNpmRegistry;
  const mirror = s.mirror || DefaultNpmMirror;
  return { primary, mirror };
}

/** 探测一个 registry 的 /-/ping 延迟（失败抛错）。 */
async function probeLatency(registry: string, timeoutMs: number): Promise<number> {
  const url = registry.replace(/\/+$/, '') + '/-/ping';
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), timeoutMs);
  const start = Date.now();
  try {
    const resp = await fetch(url, { signal: ctrl.signal });
    if (resp.status !== 200) {
      throw new Error(`registry ${registry} 返回 ${resp.status}`);
    }
    await resp.arrayBuffer(); // 读一点 body
    return Date.now() - start;
  } finally {
    clearTimeout(timer);
  }
}

/**
 * 决定安装用哪个 registry。规则（未 disableAutoSwitch 时）：
 *   - 强制镜像 → 镜像
 *   - 并发放主源与镜像：主源失败 → 镜像；镜像失败 → 主源；
 *     两者都通但主源明显慢于镜像（≥3× 且 >500ms）→ 镜像；否则主源。
 */
export async function chooseRegistry(
  s: RegistrySpec,
): Promise<{ registry: string; viaMirror: boolean; probe: string }> {
  const { primary, mirror } = specEffective(s);
  if (s.preferMirror) return { registry: mirror, viaMirror: true, probe: 'prefer-mirror' };
  if (s.disableAutoSwitch) return { registry: primary, viaMirror: false, probe: 'auto-switch disabled' };
  if (primary === mirror) return { registry: primary, viaMirror: false, probe: 'primary==mirror' };

  const timeoutMs = 3000;
  const [pr, mr] = await Promise.all([
    probeLatency(primary, timeoutMs).then(
      (d) => ({ latency: d, err: null as Error | null }),
      (e) => ({ latency: 0, err: e as Error }),
    ),
    probeLatency(mirror, timeoutMs).then(
      (d) => ({ latency: d, err: null as Error | null }),
      (e) => ({ latency: 0, err: e as Error }),
    ),
  ]);
  if (pr.err && mr.err) {
    return { registry: primary, viaMirror: false, probe: `主源与镜像均不可达（主:${pr.err.message} 镜:${mr.err.message}），回退主源` };
  }
  if (pr.err) {
    return { registry: mirror, viaMirror: true, probe: `主源不可达（${pr.err.message}），镜像 ${mr.latency}ms` };
  }
  if (mr.err) {
    return { registry: primary, viaMirror: false, probe: `镜像不可达（${mr.err.message}），主源 ${pr.latency}ms` };
  }
  if (pr.latency >= 3 * mr.latency && pr.latency > 500) {
    return { registry: mirror, viaMirror: true, probe: `主源 ${pr.latency}ms 明显慢于镜像 ${mr.latency}ms` };
  }
  return { registry: primary, viaMirror: false, probe: `主源 ${pr.latency}ms 可用（镜像 ${mr.latency}ms）` };
}

// ---------- GitHub 源（dsh 源码构建，v0.5.0 起） ----------

/** dsh 官方仓库（GitHub 源码源）。 */
export const GithubDshRepo = 'https://github.com/deepseek-ai/deepseek-harness.git';

/** 安装源类型：github = 源码构建；npm = registry 安装（旧方式，保留作后备）。 */
export type DshSource = 'github' | 'npm';

/**
 * 解析代理字符串，优先级：CLI 显式 > 环境变量 > launcher.json 配置。
 * 环境变量依次读 DSH_LAUNCHER_PROXY / HTTPS_PROXY / HTTP_PROXY / ALL_PROXY。
 * 返回 undefined 表示直连。git 走 socks5h:// 也支持（git 内置）。
 */
export function resolveProxy(cli?: string, configProxy?: string): string | undefined {
  if (cli) return cli;
  const env =
    process.env.DSH_LAUNCHER_PROXY ??
    process.env.HTTPS_PROXY ??
    process.env.https_proxy ??
    process.env.HTTP_PROXY ??
    process.env.http_proxy ??
    process.env.ALL_PROXY ??
    process.env.all_proxy;
  if (env) return env;
  if (configProxy) return configProxy;
  return undefined;
}

/** git 代理参数（无代理时为空数组）。 */
export function gitProxyArgs(proxy: string | undefined): string[] {
  if (!proxy) return [];
  return ['-c', `http.proxy=${proxy}`, '-c', `https.proxy=${proxy}`];
}

/** 运行 git（.exe，无需 shell），流式输出可选，退出码非 0 抛错。cwd 可指定运行目录。 */
export function runGit(args: string[], onLine?: LineCallback, cwd?: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const cp = spawn('git', args, { cwd, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
    let output = '';
    const push = (chunk: Buffer) => {
      const s = chunk.toString('utf8');
      output += s;
      if (onLine) {
        for (const line of s.split(/\r?\n/)) {
          if (line.trim()) onLine(line);
        }
      }
    };
    cp.stdout?.on('data', push);
    cp.stderr?.on('data', push);
    cp.on('error', reject);
    cp.on('close', (code) => {
      if (code !== 0) {
        reject(new Error(`git ${args.join(' ')} 退出码 ${code}`));
        return;
      }
      resolve(output);
    });
  });
}

/** 列出 dsh 仓库的 dsh-v* tags（ls-remote，无需完整 clone）。 */
export async function listDshTags(proxy?: string): Promise<string[]> {
  const out = await runGit(['ls-remote', '--tags', '--refs', ...gitProxyArgs(proxy), GithubDshRepo]);
  const tags: string[] = [];
  for (const line of out.split(/\r?\n/)) {
    const m = /refs\/tags\/(.+)$/.exec(line.trim());
    if (m && m[1].startsWith('dsh-')) tags.push(m[1]);
  }
  return tags;
}

/** 取最新的 dsh tag（按 semver 比较，dsh-v 前缀自动处理）。 */
export async function latestDshTag(proxy?: string): Promise<string> {
  const tags = await listDshTags(proxy);
  let best: string | null = null;
  let bestSemver: Semver | null = null;
  for (const t of tags) {
    const s = parseSemver(t);
    if (!s) continue;
    if (bestSemver === null || compareSemver(s, bestSemver) > 0) {
      best = t;
      bestSemver = s;
    }
  }
  if (!best) throw new Error('未找到任何 dsh 版本 tag（GitHub 仓库不可达？可加 --proxy 指定代理）');
  return best;
}

/** 浅克隆 dsh 仓库到 <dir>/deepseek-harness（单 tag）。目标已存在则先删除。 */
export async function cloneDsh(
  dir: string,
  tag: string,
  proxy: string | undefined,
  onLine?: LineCallback,
): Promise<string> {
  const target = join(dir, 'deepseek-harness');
  await import('node:fs/promises').then(({ rm }) => rm(target, { recursive: true, force: true }));
  const args = [
    'clone', '--depth', '1', '--branch', tag, '--single-branch',
    ...gitProxyArgs(proxy), GithubDshRepo, target,
  ];
  await runGit(args, onLine);
  return target;
}

/** 运行 pnpm（Windows 上是 pnpm.cmd，需 shell）。退出码非 0 抛错，流式输出。 */
export function runPnpm(args: string[], cwd: string, onLine?: LineCallback): Promise<string> {
  return new Promise((resolve, reject) => {
    const cp = spawn('pnpm', args, {
      cwd,
      windowsHide: true,
      shell: process.platform === 'win32',
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let output = '';
    const push = (chunk: Buffer) => {
      const s = chunk.toString('utf8');
      output += s;
      if (onLine) {
        for (const line of s.split(/\r?\n/)) {
          if (line.trim()) onLine(line);
        }
      }
    };
    cp.stdout?.on('data', push);
    cp.stderr?.on('data', push);
    cp.on('error', reject);
    cp.on('close', (code) => {
      if (code !== 0) {
        reject(new Error(`pnpm ${args.join(' ')} 退出码 ${code}`));
        return;
      }
      resolve(output);
    });
  });
}

/** 确保 pnpm 可用；不可用时尝试 `npm install -g pnpm`。返回 'pnpm'。 */
export async function ensurePnpm(onLine?: LineCallback): Promise<string> {
  try {
    await runPnpm(['--version'], process.cwd(), onLine);
    return 'pnpm';
  } catch {
    onLine?.('未检测到 pnpm，正在通过 npm 安装 pnpm……');
    await runNpm(['install', '-g', 'pnpm']);
    return 'pnpm';
  }
}

// ---------- 版本 ----------

export interface Version {
  major: number;
  minor: number;
  patch: number;
}

export function versionString(v: Version): string {
  return `${v.major}.${v.minor}.${v.patch}`;
}

/** 是否满足 README 要求：^22.19 || >=24。 */
export function versionCompatible(v: Version): boolean {
  return (v.major === 22 && v.minor >= 19) || v.major >= 24;
}

const versionRe = /^v?(\d+)\.(\d+)\.(\d+)/;

export function parseVersion(out: string): Version | null {
  const m = versionRe.exec(out.trim());
  if (!m) return null;
  return { major: Number(m[1]), minor: Number(m[2]), patch: Number(m[3]) };
}

export interface NodeInfo {
  nodePath: string;
  nodeVer: Version;
  npmPath: string;
  npmVer: string;
}

// ---------- 命令执行 ----------

/** execFile 封装：windowsHide 等价 CREATE_NO_WINDOW（防黑框）。 */
function run(
  file: string,
  args: string[],
  opts: { maxBuffer?: number } = {},
): Promise<{ stdout: string; stderr: string }> {
  return new Promise((resolve, reject) => {
    execFile(
      file,
      args,
      { windowsHide: true, maxBuffer: opts.maxBuffer ?? 64 * 1024 * 1024 },
      (err, stdout, stderr) => {
        if (err) {
          (err as Error & { stdout?: string; stderr?: string }).stdout = stdout;
          (err as Error & { stdout?: string; stderr?: string }).stderr = stderr;
          reject(err);
          return;
        }
        resolve({ stdout, stderr });
      },
    );
  });
}

/**
 * 执行 npm 命令。Windows 上 npm 是 npm.cmd（批处理），execFile/spawn 无法直接
 * 启动，必须经 cmd.exe（shell: true）。退出码非 0 时抛错（附带输出）。
 */
export function runNpm(args: string[]): Promise<{ stdout: string; stderr: string }> {
  return new Promise((resolve, reject) => {
    const cp = spawn('npm', args, {
      windowsHide: true,
      shell: process.platform === 'win32',
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let stdout = '';
    let stderr = '';
    cp.stdout?.on('data', (d: Buffer) => {
      stdout += d;
    });
    cp.stderr?.on('data', (d: Buffer) => {
      stderr += d;
    });
    cp.on('error', reject);
    cp.on('close', (code) => {
      if (code !== 0) {
        const err = new Error(`npm ${args[0]} 退出码 ${code}`) as Error & { stdout?: string; stderr?: string };
        err.stdout = stdout;
        err.stderr = stderr;
        reject(err);
        return;
      }
      resolve({ stdout, stderr });
    });
  });
}

/**
 * 执行 PowerShell 脚本（Windows 自带 powershell.exe：-NoProfile -ExecutionPolicy Bypass -File），
 * 隐藏窗口、流式输出行回调；退出码非 0 抛错（附输出尾部）。插件 install.ps1 由此执行（M1 pull）。
 */
export function runPowerShellFile(
  scriptAbs: string,
  args: string[],
  cwd: string,
  onLine?: LineCallback,
): Promise<string> {
  return new Promise((resolve, reject) => {
    const cp = spawn(
      'powershell',
      ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', scriptAbs, ...args],
      { cwd, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] },
    );
    let output = '';
    const push = (chunk: Buffer) => {
      const s = chunk.toString('utf8');
      output += s;
      if (onLine) {
        for (const line of s.split(/\r?\n/)) {
          if (line.trim()) onLine(line);
        }
      }
    };
    cp.stdout?.on('data', push);
    cp.stderr?.on('data', push);
    cp.on('error', reject);
    cp.on('close', (code) => {
      if (code !== 0) {
        const tail = output.split(/\r?\n/).slice(-8).join('\n');
        reject(new Error(`powershell ${scriptAbs} 退出码 ${code}\n${tail}`));
        return;
      }
      resolve(output);
    });
  });
}

/** 探测 node 与 npm，返回版本信息。任一缺失即抛错并给出指引。 */
export async function detect(): Promise<NodeInfo> {
  let nodeOut = '';
  try {
    nodeOut = (await run('node', ['--version'])).stdout;
  } catch {
    throw new Error('未找到 Node.js：请先安装 Node.js（建议 22.19+ 或 24+，下载 https://nodejs.org），并确保 node 在 PATH 中');
  }
  const nodeVer = parseVersion(nodeOut);
  if (!nodeVer) {
    throw new Error(`无法解析 node 版本："${nodeOut.trim()}"`);
  }

  let npmOut = '';
  try {
    npmOut = (await runNpm(['--version'])).stdout;
  } catch {
    throw new Error('未找到 npm：请确认 Node.js 安装完整（npm 随 Node.js 一起安装）');
  }

  return {
    nodePath: 'node',
    nodeVer,
    npmPath: 'npm',
    npmVer: npmOut.trim(),
  };
}

/** 安装输出行回调（用于 UI 实时日志）。 */
export type LineCallback = (line: string) => void;

/**
 * 执行 `npm install -g --prefix <dir> --registry <registry> <pkg>`，
 * 流式输出（每行回调 + 累积输出）。退出码非 0 抛错。
 */
export function installStream(
  pkg: string,
  dir: string,
  registry: string,
  onLine?: LineCallback,
): Promise<string> {
  return new Promise((resolve, reject) => {
    const cp = spawn(
      'npm',
      ['install', '-g', '--no-fund', '--no-audit', '--prefix', dir, '--registry', registry, pkg],
      { windowsHide: true, shell: process.platform === 'win32', stdio: ['ignore', 'pipe', 'pipe'] },
    );
    let output = '';
    const push = (chunk: Buffer) => {
      const s = chunk.toString('utf8');
      output += s;
      if (onLine) {
        for (const line of s.split(/\r?\n/)) {
          if (line.trim()) onLine(line);
        }
      }
    };
    cp.stdout?.on('data', push);
    cp.stderr?.on('data', push);
    cp.on('error', reject);
    cp.on('close', (code) => {
      if (code !== 0) {
        reject(new Error(`npm install 退出码 ${code}`));
        return;
      }
      resolve(output);
    });
  });
}

/** 执行任意命令（windowsHide），返回合并输出。用于 netstat / taskkill。 */
export async function runNoWindow(name: string, args: string[]): Promise<string> {
  try {
    const { stdout, stderr } = await run(name, args);
    return stdout + stderr;
  } catch (e) {
    const err = e as NodeJS.ErrnoException & { stdout?: string; stderr?: string };
    return `${err.stdout ?? ''}${err.stderr ?? ''}`;
  }
}

// ---------- dsh 路径约定 ----------

/** 安装目录中 dsh 的 bin.js 路径（按安装源：github = 源码构建产物；npm = registry 布局）。 */
export function dshBinPath(installDir: string, source?: DshSource): string {
  return source === 'github'
    ? join(installDir, 'deepseek-harness', 'apps', 'cli', 'lib', 'bin.js')
    : join(installDir, 'node_modules', '@deepseek-ai', 'dsh', 'lib', 'bin.js');
}

/** 安装目录中 dsh 包/仓库的目录（package.json 所在）。 */
export function dshPackageDir(installDir: string, source?: DshSource): string {
  return source === 'github'
    ? join(installDir, 'deepseek-harness')
    : join(installDir, 'node_modules', '@deepseek-ai', 'dsh');
}

/** 运行 `node bin.js --version` 获取版本字符串。 */
export async function dshVersion(installDir: string, source?: DshSource): Promise<string> {
  const { stdout } = await run('node', [dshBinPath(installDir, source), '--version']);
  const v = stdout.trim();
  if (!v) throw new Error('bin.js --version 无输出');
  return v;
}

/** 直接从 package.json 读取版本（bin.js 不可用时的回退）。 */
export function dshVersionFromPackage(installDir: string, source?: DshSource): string {
  const data = readFileSync(join(dshPackageDir(installDir, source), 'package.json'), 'utf8');
  const p = JSON.parse(data) as { version?: string };
  if (!p.version) throw new Error('package.json 缺少 version 字段');
  return p.version;
}
