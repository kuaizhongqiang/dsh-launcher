// install.ts —— install 子命令：探测 node/npm、安装 @deepseek-ai/dsh、
// 校验并写入 launcher.json；以及 move（移动安装目录）。
// 移植自 Go internal/install（install.go + move.go）。

import { cpSync, existsSync, mkdirSync, readdirSync, statSync } from 'node:fs';
import { homedir } from 'node:os';
import { dirname, join, resolve } from 'node:path';

import * as config from './config.js';
import * as launch from './launch.js';
import * as log from './log.js';
import * as node from './node.js';

/** 执行完整安装流程。dir 为 --dir 覆盖（空串表示默认 %LOCALAPPDATA%\dsh）。 */
export async function run(dir: string, spec: node.RegistrySpec): Promise<void> {
  // 1. 探测 node / npm
  const ni = await node.detect();
  if (!node.versionCompatible(ni.nodeVer)) {
    throw new Error(
      `Node.js 版本 ${node.versionString(ni.nodeVer)} 不满足要求（需要 ^22.19 或 >=24），请到 https://nodejs.org 升级后重试`,
    );
  }
  log.info(`检测到 Node.js ${node.versionString(ni.nodeVer)}`);
  log.info(`检测到 npm ${ni.npmVer}`);

  // 2. 解析安装目录
  if (!dir) {
    dir = process.env.LOCALAPPDATA ? join(process.env.LOCALAPPDATA, 'dsh') : join(homedir(), 'AppData', 'Local', 'dsh');
  }
  const abs = resolve(dir);
  mkdirSync(abs, { recursive: true });
  log.info(`安装目录：${abs}`);

  // 2.5 提示：npm 全局已装过 dsh 的话可以跳过本次安装
  await hintGlobalInstall();

  // 3. 选择下载源并 npm install（失败自动换另一源重试一次）
  const chosen = await node.chooseRegistry(spec);
  log.info(`下载源：${chosen.registry}（${chosen.probe}）`);
  if (chosen.viaMirror) {
    log.info(`已使用国内镜像 ${chosen.registry} 下载（国内用户加速）`);
  }
  const installOnce = async (r: string): Promise<string> => {
    log.info(`正在通过 ${r} 安装 ${node.DshPackage}（首次需要下载，请耐心等待）……`);
    return node.installStream(node.DshPackage, abs, r, (line) => log.info(`npm> ${line}`));
  };

  let registry = chosen.registry;
  let out = '';
  try {
    out = await installOnce(registry);
  } catch (e) {
    const installErr = e as Error;
    if (!spec.preferMirror && !spec.disableAutoSwitch) {
      // 自动回退：主源失败且镜像未尝试 → 镜像；镜像失败 → 主源
      const { primary, mirror } = node.specEffective(spec);
      const alt = registry === primary ? mirror : registry === mirror ? primary : '';
      if (alt && alt !== registry) {
        log.warn(`从 ${registry} 下载失败，自动切换 ${alt} 重试……`);
        try {
          out = await installOnce(alt);
          registry = alt;
        } catch {
          out = '';
        }
      }
    }
    if (!out) {
      log.error(`npm install 失败：${installErr.message}`);
      throw new Error(`npm install 失败，详情见日志 ${log.logPath()}`);
    }
  }
  log.info(`npm install 完成（registry：${registry}）`);

  // 4. 校验安装结果
  const bin = node.dshBinPath(abs);
  if (!existsSync(bin)) {
    throw new Error(`安装完成但未找到 ${bin}，安装可能不完整，请重试 install`);
  }
  let ver: string;
  try {
    ver = await node.dshVersion(abs);
  } catch (e) {
    log.warn(`bin.js --version 校验失败（${(e as Error).message}），回退读取 package.json`);
    ver = node.dshVersionFromPackage(abs);
  }
  log.info(`dsh 校验通过：${node.DshPackage}@${ver}`);

  // 5. 写入 launcher.json（含下载源信息，供后续 install 复用）
  const cfg: config.Config = {
    dshInstallDir: abs,
    dshVersion: ver,
    port: config.DefaultPort,
    installedAt: new Date().toISOString(),
    registry: primaryOrDefault(spec.registry),
    registryMirror: mirrorOrDefault(spec.mirror),
    preferMirror: spec.preferMirror,
  };
  config.save(cfg);
  log.info(`配置已写入 ${config.configPath()}`);
  log.info('完成。~/.dsh 配置目录保持默认，无需手动处理。');
  log.info('现在可以运行 dsh-launcher.exe（或 dsh-launcher.exe start）启动 dsh。');
}

/** 返回显式主源（未设置则留空，表示 npm 默认）。 */
function primaryOrDefault(r?: string): string | undefined {
  if (!r || r === node.DefaultNpmRegistry) return undefined;
  return r;
}

/** 返回显式镜像（未设置则留空，表示默认 npmmirror）。 */
function mirrorOrDefault(m?: string): string | undefined {
  if (!m || m === node.DefaultNpmMirror) return undefined;
  return m;
}

/** 用环境变量补齐下载源策略（CLI 已显式设置的字段不被覆盖）。 */
export function registrySpecFromEnv(s: node.RegistrySpec): node.RegistrySpec {
  if (process.env.DSH_LAUNCHER_NPM_REGISTRY && !s.registry) s.registry = process.env.DSH_LAUNCHER_NPM_REGISTRY;
  if (process.env.DSH_LAUNCHER_NPM_MIRROR && !s.mirror) s.mirror = process.env.DSH_LAUNCHER_NPM_MIRROR;
  if (process.env.DSH_LAUNCHER_PREFER_MIRROR) s.preferMirror = true;
  if (process.env.DSH_LAUNCHER_NO_MIRROR) s.disableAutoSwitch = true;
  return s;
}

/** 从既有 launcher.json 配置 + 环境变量构建下载源策略（GUI 安装用）。 */
export function registrySpecFromConfig(cfg: config.Config | null): node.RegistrySpec {
  const s: node.RegistrySpec = {
    registry: cfg?.registry,
    mirror: cfg?.registryMirror,
    preferMirror: cfg?.preferMirror,
  };
  return registrySpecFromEnv(s);
}

/** 检测 npm 全局是否已装 dsh，有则提示可跳过。 */
async function hintGlobalInstall(): Promise<void> {
  try {
    const { stdout } = await node.runNpm(['root', '-g']);
    const root = stdout.trim();
    if (!root) return;
    const pkgDir = join(root, '@deepseek-ai', 'dsh');
    if (existsSync(pkgDir)) {
      log.info(`提示：npm 全局已安装 dsh（${pkgDir}）。若不想重复安装，可终止本次 install 直接运行 start。`);
    }
  } catch {
    /* 忽略 */
  }
}

// ---------- move ----------

/** 把 dsh 安装目录移动到 newDir（任意路径，跨盘自动复制+删除）。 */
export async function move(newDir: string): Promise<void> {
  const cfg = config.load();
  if (!cfg) throw new Error('未找到 launcher.json，请先运行 install');
  if (!cfg.dshInstallDir) throw new Error('未记录安装目录，请先运行 install');

  const old = cfg.dshInstallDir;
  const newAbs = resolve(newDir);
  if (old.replace(/[\\/]+$/, '').toLowerCase() === newAbs.replace(/[\\/]+$/, '').toLowerCase()) {
    throw new Error(`目标路径与当前安装目录相同：${old}`);
  }
  if (!existsSync(old)) {
    throw new Error(`当前安装目录不存在：${old}`);
  }
  // 目标路径校验：不存在，或存在但为空目录
  if (existsSync(newAbs)) {
    if (!statSync(newAbs).isDirectory()) {
      throw new Error(`目标路径已存在且不是目录：${newAbs}`);
    }
    const entries = readdirSync(newAbs);
    if (entries.length > 0) {
      throw new Error(`目标目录已存在且非空：${newAbs}（可能是上次移动中断的残留，请先删除该目录后再试）`);
    }
  }
  // 运行中禁止移动（端口有响应即拒绝）
  if (await launch.isRunning(cfg)) {
    throw new Error(`端口 ${cfg.port} 有服务响应（可能是 dsh 或其他服务在运行），请先停止 dsh 后再移动`);
  }

  log.info(`开始移动 dsh：${old} → ${newAbs}`);
  mkdirSync(dirname(newAbs), { recursive: true });
  await moveDir(old, newAbs);

  // 更新配置；失败则回滚目录
  cfg.dshInstallDir = newAbs;
  try {
    config.save(cfg);
  } catch (e) {
    await moveDir(newAbs, old).catch(() => {});
    throw new Error(`更新 launcher.json 失败（已尝试回滚）：${(e as Error).message}`);
  }
  log.info(`launcher.json 已更新：${newAbs}`);

  // 移动后校验
  await verifyMoved(newAbs);
  log.info(`完成。dsh 已移动到 ${newAbs}`);
}

/** 校验新位置的 dsh 可用（bin.js --version，回退 package.json）。 */
async function verifyMoved(newAbs: string): Promise<void> {
  const bin = node.dshBinPath(newAbs);
  if (!existsSync(bin)) {
    log.warn(`移动后未找到 ${bin}，请手动确认安装完整性`);
    return;
  }
  try {
    const ver = await node.dshVersion(newAbs);
    log.info(`移动后校验通过：${node.DshPackage}@${ver}`);
    return;
  } catch {
    /* fallthrough */
  }
  try {
    const pver = node.dshVersionFromPackage(newAbs);
    log.info(`移动后校验通过：${node.DshPackage}@${pver}（package.json）`);
    return;
  } catch {
    /* fallthrough */
  }
  log.warn('移动后版本校验失败，请手动确认安装完整性');
}

/** 移动目录：同盘直接 rename；跨盘（或失败）回退为复制 + 删除。 */
async function moveDir(src: string, dst: string): Promise<void> {
  try {
    // rename 跨盘会抛 EXDEV；同盘 rename 目录即可
    await import('node:fs/promises').then(({ rename }) => rename(src, dst));
    return;
  } catch {
    // 跨盘：复制 + 删除
  }
  log.info('目标与源不在同一磁盘，使用复制方式（大目录可能较慢）……');
  cpSync(src, dst, { recursive: true });
  await import('node:fs/promises').then(({ rm }) => rm(src, { recursive: true, force: true }));
}
