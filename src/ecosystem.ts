// ecosystem.ts —— 生态清单（ecosystem.json）+ `pull` 命令逻辑（M1 / 路线图 Phase 1）。
//
// 决策落点：
//   D3 清单即生态 —— 一切版本与组件由 ecosystem.json 声明；默认清单内嵌随启动器走，
//      `--manifest <url|file>` 指向私有清单做个性化覆盖。
//   P1-7 供应链（强制）—— 清单内每个插件包声明 sha256；pull 执行 install.ps1 前逐文件
//      验哈希；远程清单强制 HTTPS；dsh-plugins 源锁 commit（不依赖 --remote 漂移）。
//   D1 目录边界 —— launcher 只编排：core 走既有 install 流程；插件逐个跑其 install.ps1；
//      技能跑 install-skills.ps1；结果写入 launcher 旁的 ecosystem-state.json。

import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, isAbsolute, join, resolve } from 'node:path';

import * as config from './config.js';
import * as log from './log.js';
import * as node from './node.js';

/** 生态状态文件（D4：生态状态在 launcher 旁，与 launcher.json 同目录）。 */
export const EcosystemStateFileName = 'ecosystem-state.json';

// ---------------------------------------------------------------- 类型

export type DshSource = node.DshSource;

/** 清单里 core（dsh）的版本策略。 */
export interface ManifestDsh {
  /** 安装源：github（源码构建，默认）| npm。 */
  source: DshSource;
  /** "latest"（取最新 tag）或具体版本（github=tag，npm=版本）。 */
  version: string;
}

/** dsh-plugins 源的锁点（P1-7：锁 commit，禁止随远端 HEAD 漂移）。 */
export interface ManifestPluginsSource {
  repo: string;
  /** 40 位 commit sha（锁死）。 */
  commit: string;
}

/** 单个插件包声明：id、包目录（相对 dsh-plugins 检出根）、待执行文件 sha256。 */
export interface ManifestPluginPackage {
  id: string;
  /** 包目录，如 plugins/audio-read-dsh-plugin。 */
  dir: string;
  /** 相对包目录的文件 → sha256（install.ps1 至少一项；可含载荷文件）。 */
  sha256: Record<string, string>;
}

/** 技能安装声明（相对 dsh-plugins 检出根）。 */
export interface ManifestSkills {
  /** 如 skills/install-skills.ps1。 */
  script: string;
  /** 该脚本 sha256（可选；声明则执行前校验）。 */
  sha256?: string;
}

/** ecosystem.json v1。 */
export interface EcosystemManifest {
  version: 1;
  dsh: ManifestDsh;
  plugins: {
    source: ManifestPluginsSource;
    packages: ManifestPluginPackage[];
  };
  skills?: ManifestSkills;
}

/** 生态状态 v1：pull 结果落盘（launcher 旁 ecosystem-state.json）。 */
export interface EcosystemState {
  version: 1;
  updatedAt: string;
  core: { installed: boolean; version?: string; installDir?: string };
  plugins: Record<string, { ok: boolean; installedAt?: string; error?: string }>;
  skills?: { ok: boolean; installedAt?: string; error?: string };
  manifest: { label: string; pluginsCommit: string };
}

/** `pull` 选项（M1）。 */
export interface PullOptions {
  /** 清单来源：undefined=默认（内嵌）；https://…=远程（强制 HTTPS）；其余按本地文件路径。 */
  manifest?: string;
  /** 插件选择：undefined=全部；空数组=不装插件；数组=指定 id 子集。 */
  plugins?: string[] | null;
  /** 是否处理技能（manifest 声明了 skills 才有效）。默认 true。 */
  skills?: boolean;
  /** 是否补齐 core 缺口（launcher.json 未记录安装时走 install）。默认 true。 */
  core?: boolean;
  /** 只校验与规划，不执行安装、不写状态。 */
  dryRun?: boolean;
  /** 显式 dsh-plugins 检出目录（覆盖环境变量与默认目录；供测试/特殊布局）。 */
  pluginsDir?: string;
}

// ---------------------------------------------------------------- 默认清单

const sha = {
  'audio-read': '8a7492b61b2500ed91a1bbe1f2e4a6118c27bce3dc1dab51b7bdd214040576c8',
  'audio-speak': '00f1d463c43f302a6daa54c02d8345ee60be7e1c060d60ed10dd72d94380c627',
  credentials: 'e4d183d676ee2c2e5e6e9cd2bbb85cd37ec77171588ff4226cd60d7e7563214b',
  'deepseek-balance': '238e8f515221d67c955ccdf4d5a344c0549bb04701188758fe50380711b04730',
  'deepseek-recharge': '7a86c60a23d805032ae42d53c998ed8d3506efdd2c0b2527634841f7228b2ecf',
  'describe-image': 'dda9bc1a6d8af3b3d71a0ca8ab6f9380671927747faf3f48cda54dbaafad2e3d',
  'document-read': 'bd86ca28a74c898b0299487d66e7ec91a468aa8ed72bb9cf8b999f720d7e1962',
  github: 'd69340d34628549cb793fc7600632ceeabdf23f1a3fed09bafb6b0afe0414254',
  stock: 'e20fed7cac47e4bb4d976dbc752d5c6dd735fc94c215c4920425ba7fe4731a89',
  'unity-mcp': 'e76e308719dd22c804276e48584c6d5b3c16ce5a2c5a205f3b598165576e02a5',
  'video-read': '5c6ee57203857ced694f96e706564902ab4072aaf968191b5468f31bc5eb8ac2',
};

const packageDirs: Record<string, string> = {};
for (const id of Object.keys(sha)) packageDirs[id] = `plugins/${id}-dsh-plugin`;

/**
 * 默认清单（内嵌，随启动器走）：与当前 dsh-plugins 锁点 15ffcfd 对齐。
 * 更新流程：在 dsh-plugins 新 commit 上重算各 install.ps1 sha256 → 同步本对象与仓库根 ecosystem.json。
 */
export const DEFAULT_ECOSYSTEM: EcosystemManifest = {
  version: 1,
  dsh: { source: 'github', version: 'latest' },
  plugins: {
    source: {
      repo: 'https://github.com/kuaizhongqiang/dsh-plugins.git',
      commit: '15ffcfd77d391d6ba5fed8dc6285e6bb5ff0f72c',
    },
    packages: Object.entries(sha).map(([id, installSha]) => ({
      id,
      dir: packageDirs[id],
      sha256: { 'install.ps1': installSha },
    })),
  },
  skills: {
    script: 'skills/install-skills.ps1',
    sha256: '2fc2d4f396b1c5fc1c21437058bf473a107017698c3694511721ba3f7f61e96a',
  },
};

// ---------------------------------------------------------------- 加载与校验

const SHA256_RE = /^[0-9a-f]{64}$/i;
const COMMIT_RE = /^[0-9a-f]{40}$/i;

function relSafe(p: string): boolean {
  return !isAbsolute(p) && !p.split(/[\\/]/).includes('..') && p.length > 0;
}

function failManifest(why: string): never {
  throw new Error(`ecosystem.json 校验失败：${why}`);
}

/** 清单结构校验（版本、字段、sha256 格式、路径不越界）。校验通过后可按类型使用。 */
export function validateManifest(m: unknown): asserts m is EcosystemManifest {
  if (typeof m !== 'object' || m === null) failManifest('不是对象');
  const x = m as Record<string, unknown>;
  if (x.version !== 1) failManifest('version 必须为 1');
  const dsh = (x.dsh ?? failManifest('缺少 dsh')) as ManifestDsh;
  if (dsh.source !== 'github' && dsh.source !== 'npm') failManifest('dsh.source 必须为 github|npm');
  if (typeof dsh.version !== 'string' || dsh.version.length === 0) failManifest('dsh.version 必须为非空字符串');
  const pg = (x.plugins ?? failManifest('缺少 plugins')) as { source?: unknown; packages?: unknown };
  const src = (pg.source ?? failManifest('缺少 plugins.source')) as ManifestPluginsSource;
  if (typeof src.repo !== 'string' || !/^https:\/\//i.test(src.repo)) failManifest('plugins.source.repo 必须为 https URL');
  if (typeof src.commit !== 'string' || !COMMIT_RE.test(src.commit)) failManifest('plugins.source.commit 必须为 40 位 commit sha');
  if (!Array.isArray(pg.packages) || pg.packages.length === 0) failManifest('plugins.packages 必须为非空数组');
  for (const p of pg.packages as ManifestPluginPackage[]) {
    if (typeof p.id !== 'string' || !p.id) failManifest('插件缺少 id');
    if (typeof p.dir !== 'string' || !relSafe(p.dir)) failManifest(`插件 ${String(p.id)} dir 非法（须为相对路径且不含 ..）`);
    if (typeof p.sha256 !== 'object' || p.sha256 === null) failManifest(`插件 ${String(p.id)} 缺少 sha256`);
    for (const [file, hex] of Object.entries(p.sha256)) {
      if (typeof hex !== 'string' || !SHA256_RE.test(hex)) failManifest(`插件 ${String(p.id)} ${file} 的 sha256 格式非法`);
      if (!relSafe(file)) failManifest(`插件 ${String(p.id)} 文件 ${file} 路径越界`);
    }
  }
  const sk = x.skills as ManifestSkills | undefined;
  if (sk !== undefined) {
    if (typeof sk.script !== 'string' || !relSafe(sk.script)) failManifest('skills.script 非法（须为相对路径且不含 ..）');
    if (sk.sha256 !== undefined && (typeof sk.sha256 !== 'string' || !SHA256_RE.test(sk.sha256))) {
      failManifest('skills.sha256 格式非法');
    }
  }
}

function parseManifest(text: string, label: string): EcosystemManifest {
  let json: unknown;
  try {
    let data = text;
    if (data.charCodeAt(0) === 0xfeff) data = data.slice(1); // 去 BOM
    json = JSON.parse(data);
  } catch (e) {
    throw new Error(`清单解析失败（${label}）：${(e as Error).message}`);
  }
  validateManifest(json);
  return json;
}

/**
 * 加载清单。
 * - undefined / "default"：内嵌默认清单（随 launcher 走）；
 * - https://…：远程清单，**强制 HTTPS**（P1-7）；
 * - 其余：本地文件路径（支持 file 绝对/相对路径）。
 */
export async function loadManifest(spec?: string): Promise<{ manifest: EcosystemManifest; label: string }> {
  if (!spec || spec === 'default') {
    validateManifest(DEFAULT_ECOSYSTEM);
    return { manifest: DEFAULT_ECOSYSTEM, label: '默认（内嵌）' };
  }
  if (/^https?:\/\//i.test(spec)) {
    if (/^http:\/\//i.test(spec)) {
      throw new Error(`私有清单强制 HTTPS（P1-7）：收到 http 地址 ${spec}；请改用 https 或本地文件路径`);
    }
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), 15_000);
    try {
      const resp = await fetch(spec, {
        signal: ctrl.signal,
        headers: { 'User-Agent': 'dsh-launcher/pull', Accept: 'application/json' },
      });
      if (resp.status !== 200) throw new Error(`HTTP ${resp.status}（${spec}）`);
      const text = await resp.text();
      return { manifest: parseManifest(text, spec), label: spec };
    } finally {
      clearTimeout(timer);
    }
  }
  const abs = resolve(spec);
  try {
    return { manifest: parseManifest(readFileSync(abs, 'utf8'), abs), label: abs };
  } catch (e) {
    if ((e as NodeJS.ErrnoException).code === 'ENOENT') throw new Error(`清单文件不存在：${abs}`);
    throw e;
  }
}

// ---------------------------------------------------------------- 源与哈希

/** dsh-plugins 检出根：显式 > 环境变量 > launcher 旁默认。 */
export function pluginsRootDir(explicit?: string): string {
  if (explicit) return resolve(explicit);
  const env = process.env.DSH_LAUNCHER_PLUGINS_DIR;
  if (env) return resolve(env);
  return join(dirname(config.configPath()), 'dsh-plugins');
}

/**
 * 确保 dsh-plugins 检出在**锁定的 commit**（P1-7）：
 * - 目录不存在 → 克隆并 checkout 锁定 commit；
 * - 目录存在但非 git 检出 → 报错（提示删除后让 pull 克隆，或设置 DSH_LAUNCHER_PLUGINS_DIR）；
 * - 检出 HEAD ≠ 锁定 commit → 报错（不自动漂移）。
 * dryRun 时不做克隆（离线也允许校验既有检出）。
 */
export async function ensurePluginsSource(
  manifest: EcosystemManifest,
  explicit?: string,
  dryRun = false,
): Promise<string> {
  const src = manifest.plugins.source;
  const dir = pluginsRootDir(explicit);
  if (!existsSync(dir)) {
    if (dryRun) {
      throw new Error(`dry-run：插件检出不存在（${dir}），无法校验；先执行真实 pull 克隆后重试`);
    }
    log.info(`克隆 dsh-plugins（锁 ${src.commit.slice(0, 8)}）：${src.repo}`);
    await clonePinned(src.repo, src.commit, dir);
    return dir;
  }
  if (!existsSync(join(dir, '.git'))) {
    throw new Error(
      `插件源目录 ${dir} 不是 git 检出（缺 .git）。请删除该目录后让 pull 重新克隆，` +
        `或把 DSH_LAUNCHER_PLUGINS_DIR 指向已检出的 dsh-plugins 仓库`,
    );
  }
  const head = (await node.runGit(['rev-parse', 'HEAD'], undefined, dir)).trim();
  if (head !== src.commit) {
    throw new Error(
      `dsh-plugins 检出 HEAD=${head.slice(0, 8)}，清单锁定 ${src.commit.slice(0, 8)}（P1-7 锁 commit，不自动漂移）。` +
        `请在 ${dir} 更新到锁定 commit 后重试 pull`,
    );
  }
  return dir;
}

/** 克隆仓库并 checkout 锁定 commit（fetch 指定 sha，GitHub 允许可达 sha 的浅取）。 */
async function clonePinned(repo: string, commit: string, target: string): Promise<void> {
  const fsP = await import('node:fs/promises');
  await fsP.rm(target, { recursive: true, force: true });
  mkdirSync(dirname(target), { recursive: true });
  await fsP.mkdir(target, { recursive: true });
  const proxy = node.resolveProxy(undefined, config.load()?.proxy);
  const run = (args: string[]): Promise<string> => node.runGit(args, undefined, target);
  await run(['init', '-q']);
  await run(['remote', 'add', 'origin', ...node.gitProxyArgs(proxy), repo]);
  await run(['fetch', '-q', '--depth', '1', 'origin', commit]);
  await run(['checkout', '-q', 'FETCH_HEAD']);
  const head = (await run(['rev-parse', 'HEAD'])).trim();
  if (head !== commit) throw new Error(`克隆后 HEAD=${head.slice(0, 8)} ≠ 锁定 ${commit.slice(0, 8)}`);
}

/** 单文件 sha256（hex）。 */
export function fileSha256(abs: string): string {
  return createHash('sha256').update(readFileSync(abs)).digest('hex');
}

/**
 * 供应链校验（P1-7）：执行 install.ps1 前逐文件验哈希。
 * 校验范围内任一文件缺失/不匹配即抛错（不做任何执行）。
 */
export function verifyHashes(manifest: EcosystemManifest, rootDir: string, pkgIds: string[]): void {
  const src = manifest.plugins.source;
  for (const pkg of manifest.plugins.packages) {
    if (pkgIds.length > 0 && !pkgIds.includes(pkg.id)) continue;
    const base = join(rootDir, pkg.dir);
    for (const [rel, hex] of Object.entries(pkg.sha256)) {
      const file = join(base, rel);
      if (!existsSync(file)) {
        throw new Error(`供应链校验失败：${pkg.dir}/${rel} 不存在（锁 ${src.commit.slice(0, 8)}）`);
      }
      const actual = fileSha256(file);
      if (actual.toLowerCase() !== hex.toLowerCase()) {
        throw new Error(
          `供应链校验失败：${pkg.dir}/${rel} sha256 不匹配（清单 ${hex}，实际 ${actual}）。` +
            `检出与清单不一致或已被篡改，拒绝执行`,
        );
      }
      log.info(`sha256 ✓ ${pkg.dir}/${rel}`);
    }
  }
  if (manifest.skills?.sha256) {
    const file = join(rootDir, manifest.skills.script);
    if (!existsSync(file)) throw new Error(`供应链校验失败：${manifest.skills.script} 不存在`);
    const actual = fileSha256(file);
    if (actual.toLowerCase() !== manifest.skills.sha256.toLowerCase()) {
      throw new Error(
        `供应链校验失败：${manifest.skills.script} sha256 不匹配（清单 ${manifest.skills.sha256}，实际 ${actual}）`,
      );
    }
    log.info(`sha256 ✓ ${manifest.skills.script}`);
  }
}

// ---------------------------------------------------------------- pull

/** 生态状态文件路径（与 launcher.json 同目录）。 */
export function ecosystemStatePath(): string {
  return join(dirname(config.configPath()), EcosystemStateFileName);
}

/**
 * `pull` 主流程：core（缺口时）→ 插件 install.ps1（逐个）→ 技能 install-skills.ps1 → 写状态。
 * 供应链校验通过才执行对应脚本；失败项记录到状态与汇总错误。
 */
export async function runPull(opts: PullOptions = {}): Promise<void> {
  const { manifest: manifestSpec, plugins: pluginSel, skills = true, core = true, dryRun = false, pluginsDir } = opts;
  const { manifest, label } = await loadManifest(manifestSpec);
  const labelShort = manifestSpec && manifestSpec !== 'default' ? manifestSpec : '默认';
  log.info(`生态清单：${label}（dsh=${manifest.dsh.source}/${manifest.dsh.version}）`);
  log.info(`插件源锁定：${manifest.plugins.source.commit.slice(0, 8)}（${manifest.plugins.source.repo}）`);

  // 0. 插件选择
  const allIds = manifest.plugins.packages.map((p) => p.id);
  let selected: string[] = allIds;
  if (pluginSel !== undefined && pluginSel !== null) {
    if (pluginSel.length === 0) selected = [];
    else {
      const unknown = pluginSel.filter((id) => !allIds.includes(id));
      for (const u of unknown) log.warn(`清单中没有插件 ${u}，跳过`);
      selected = pluginSel.filter((id) => allIds.includes(id));
    }
  }

  // 1. core 缺口（复用既有 install；不在 dry-run 里执行）
  let cfg = config.load();
  const coreState = { installed: !!(cfg && config.isInstalled(cfg)), version: cfg?.dshVersion, installDir: cfg?.dshInstallDir };
  if (core && !coreState.installed) {
    if (dryRun) {
      log.info('core：未安装 → 真实 pull 将执行 install（dsh=' + manifest.dsh.source + '）');
    } else {
      log.info(`core 未安装，按清单执行 install（source=${manifest.dsh.source}）……`);
      const install = await import('./install.js');
      await install.run(install.defaultInstallDir(), {}, {
        source: manifest.dsh.source,
        version: manifest.dsh.version === 'latest' ? undefined : manifest.dsh.version,
      });
      cfg = config.load();
      coreState.installed = !!(cfg && config.isInstalled(cfg));
      coreState.version = cfg?.dshVersion;
      coreState.installDir = cfg?.dshInstallDir;
    }
  } else if (core && coreState.installed) {
    log.info(`core：已安装（${cfg?.dshVersion ?? ''} @ ${cfg?.dshInstallDir ?? ''}），跳过`);
  }

  // 2. 插件源 + 供应链校验
  const pluginFailures: string[] = [];
  if (selected.length > 0) {
    const root = await ensurePluginsSource(manifest, pluginsDir, dryRun);
    verifyHashes(manifest, root, selected);
    for (const id of selected) {
      const pkg = manifest.plugins.packages.find((p) => p.id === id)!;
      if (dryRun) {
        log.info(`[dry-run] 将执行 install.ps1：${pkg.dir}`);
        continue;
      }
      log.info(`安装插件 ${id}（${pkg.dir}/install.ps1）……`);
      try {
        await node.runPowerShellFile(join(root, pkg.dir, 'install.ps1'), [], join(root, pkg.dir), (l) =>
          log.info(`[${id}] ${l}`),
        );
        log.info(`插件 ${id} 安装完成`);
      } catch (e) {
        pluginFailures.push(id);
        log.error(`插件 ${id} 安装失败：${(e as Error).message}`);
      }
    }
  } else {
    log.info('插件：未选择任何包，跳过');
  }

  // 3. 技能（与插件选择无关：清单声明了即处理）
  let skillsRes: { ok: boolean; installedAt?: string; error?: string } | undefined;
  if (skills && manifest.skills) {
    const root = await ensurePluginsSource(manifest, pluginsDir, dryRun);
    if (dryRun) {
      log.info(`[dry-run] 将执行技能安装：${manifest.skills.script}`);
    } else {
      log.info(`安装技能（${manifest.skills.script}）……`);
      try {
        await node.runPowerShellFile(join(root, manifest.skills.script), [], root, (l) => log.info(`[skills] ${l}`));
        skillsRes = { ok: true, installedAt: new Date().toISOString() };
        log.info('技能安装完成');
      } catch (e) {
        skillsRes = { ok: false, error: (e as Error).message };
        log.error(`技能安装失败：${skillsRes.error}`);
      }
    }
  } else if (skills && !manifest.skills) {
    log.info('技能：清单未声明 skills，跳过');
  }

  // 4. 状态落盘
  if (!dryRun) {
    const now = new Date().toISOString();
    const pluginsState: EcosystemState['plugins'] = {};
    for (const id of selected) {
      pluginsState[id] = pluginFailures.includes(id)
        ? { ok: false, error: 'install.ps1 执行失败' }
        : { ok: true, installedAt: now };
    }
    const state: EcosystemState = {
      version: 1,
      updatedAt: now,
      core: coreState,
      plugins: pluginsState,
      ...(skillsRes || manifest.skills ? { skills: skillsRes ?? { ok: false, error: '未执行' } } : {}),
      manifest: { label: labelShort, pluginsCommit: manifest.plugins.source.commit },
    };
    writeFileSync(ecosystemStatePath(), JSON.stringify(state, null, 2) + '\n', 'utf8');
    log.info(`生态状态已写入 ${ecosystemStatePath()}`);
  } else {
    log.info('[dry-run] 未执行任何安装、未写状态');
  }

  if (pluginFailures.length > 0 || (skillsRes && !skillsRes.ok)) {
    const bad: string[] = [...pluginFailures];
    if (skillsRes && !skillsRes.ok) bad.push('skills');
    throw new Error(`pull 完成但存在失败项：${bad.join(', ')}（详见上方日志）`);
  }
  log.info(`pull 完成：core=${coreState.installed ? '已装' : '跳过/未装'}，插件 ${selected.length} 个${dryRun ? '(dry-run)' : ''}`);
}
