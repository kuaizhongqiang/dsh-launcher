// node.ts —— Node.js / npm 探测与命令封装，以及 dsh 安装目录的路径约定。
// 移植自 Go internal/node。

import { spawn, execFile } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
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

/** 安装目录中 dsh 的 bin.js 路径。 */
export function dshBinPath(installDir: string): string {
  return join(installDir, 'node_modules', '@deepseek-ai', 'dsh', 'lib', 'bin.js');
}

/** 安装目录中 dsh 包的目录。 */
export function dshPackageDir(installDir: string): string {
  return join(installDir, 'node_modules', '@deepseek-ai', 'dsh');
}

/** 运行 `node bin.js --version` 获取版本字符串。 */
export async function dshVersion(installDir: string): Promise<string> {
  const { stdout } = await run('node', [dshBinPath(installDir), '--version']);
  const v = stdout.trim();
  if (!v) throw new Error('bin.js --version 无输出');
  return v;
}

/** 直接从 package.json 读取版本（bin.js 不可用时的回退）。 */
export function dshVersionFromPackage(installDir: string): string {
  const data = readFileSync(join(dshPackageDir(installDir), 'package.json'), 'utf8');
  const p = JSON.parse(data) as { version?: string };
  if (!p.version) throw new Error('package.json 缺少 version 字段');
  return p.version;
}
