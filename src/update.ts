// update.ts —— 升级检测，覆盖两条独立的分发渠道：
//   - dsh 本体（@deepseek-ai/dsh）走 npm registry，检测 latest dist-tag；
//   - dsh-launcher 走 GitHub Release，检测仓库最新 tag。
// 本模块只做"检测"，不执行升级。移植自 Go internal/update。

import * as node from './node.js';

/** 启动器 GitHub Release 最新版页面（升级启动器时打开）。 */
export const LauncherReleaseURL = 'https://github.com/kuaizhongqiang/dsh-launcher/releases/latest';
const launcherAPI = 'https://api.github.com/repos/kuaizhongqiang/dsh-launcher/releases/latest';
const httpTimeoutMs = 10_000;

// ---------- 语义化版本比较（支持 v 前缀与 -prerelease） ----------

interface Semver {
  major: number;
  minor: number;
  patch: number;
  pre: string[]; // 预发布标识符（空 = 正式版）
}

/** 解析语义化版本：可选 v/V 前缀、major.minor.patch、可选 -prerelease，忽略 +build。 */
function parseSemver(s: string): Semver | null {
  let v = s.trim();
  if (v.startsWith('v') || v.startsWith('V')) v = v.slice(1);
  const plus = v.indexOf('+');
  if (plus >= 0) v = v.slice(0, plus);
  const dash = v.indexOf('-');
  const main = dash >= 0 ? v.slice(0, dash) : v;
  const preStr = dash >= 0 ? v.slice(dash + 1) : '';
  const parts = main.split('.');
  if (parts.length !== 3) return null;
  const nums = parts.map(Number);
  if (nums.some((n) => !Number.isInteger(n) || n < 0)) return null;
  const pre = preStr ? preStr.split('.') : [];
  if (pre.some((id) => id === '')) return null;
  return { major: nums[0], minor: nums[1], patch: nums[2], pre };
}

/** 按 semver 规范比较：返回 -1（a<b）、0（a==b）、1（a>b）。正式版 > 预发布版。 */
function compareSemver(a: Semver, b: Semver): number {
  if (a.major !== b.major) return sign(a.major - b.major);
  if (a.minor !== b.minor) return sign(a.minor - b.minor);
  if (a.patch !== b.patch) return sign(a.patch - b.patch);
  if (a.pre.length === 0 && b.pre.length === 0) return 0;
  if (a.pre.length === 0) return 1;
  if (b.pre.length === 0) return -1;
  for (let i = 0; i < Math.min(a.pre.length, b.pre.length); i++) {
    const c = comparePreID(a.pre[i], b.pre[i]);
    if (c !== 0) return c;
  }
  return sign(a.pre.length - b.pre.length);
}

function sign(n: number): number {
  return n < 0 ? -1 : n > 0 ? 1 : 0;
}

/** 比较两个预发布标识符：数字按数值、字母按 ASCII；数字 < 字母。 */
function comparePreID(a: string, b: string): number {
  const an = Number(a);
  const bn = Number(b);
  const aIsNum = Number.isInteger(an) && /^\d+$/.test(a);
  const bIsNum = Number.isInteger(bn) && /^\d+$/.test(b);
  if (aIsNum && bIsNum) return sign(an - bn);
  if (aIsNum) return -1; // 数字 < 字母
  if (bIsNum) return 1;
  return a < b ? -1 : a > b ? 1 : 0;
}

// ---------- 检测 ----------

/** 检测启动器自身（GitHub Release 最新 tag）是否有新版。current 为 dev 时跳过。 */
export async function checkLauncher(current: string): Promise<{ latest: string; hasUpdate: boolean }> {
  if (!current || current === 'dev') {
    throw new Error('当前为 dev 构建，跳过启动器升级检测');
  }
  const cur = parseSemver(current);
  if (!cur) throw new Error(`当前版本 "${current}" 无法解析`);
  const rel = await getJSON<{ tag_name?: string; html_url?: string }>(launcherAPI);
  const tag = rel.tag_name ?? '';
  const lat = parseSemver(tag);
  if (!lat) throw new Error(`最新 Release 版本 "${tag}" 无法解析`);
  return { latest: tag, hasUpdate: compareSemver(lat, cur) > 0 };
}

/** 检测 npm registry 上 @deepseek-ai/dsh 的 latest 版本是否有新版。 */
export async function checkDsh(
  current: string,
  spec: node.RegistrySpec,
): Promise<{ latest: string; hasUpdate: boolean }> {
  const cur = parseSemver(current);
  if (!cur) throw new Error(`当前版本 "${current}" 无法解析`);

  const { primary, mirror } = node.specEffective(spec);
  const registries = [primary];
  if (!spec.preferMirror && !spec.disableAutoSwitch && mirror !== primary) {
    registries.push(mirror);
  }
  let firstErr: Error | null = null;
  let lastErr: Error | null = null;
  for (const reg of registries) {
    try {
      const v = await dshLatest(reg);
      const lat = parseSemver(v);
      if (!lat) throw new Error(`最新版本 "${v}" 无法解析`);
      return { latest: v, hasUpdate: compareSemver(lat, cur) > 0 };
    } catch (e) {
      if (!firstErr) firstErr = e as Error;
      lastErr = e as Error;
    }
  }
  if (firstErr && lastErr && firstErr !== lastErr) {
    throw new Error(`主源与镜像均查询失败：主源 ${firstErr.message}；镜像 ${lastErr.message}`);
  }
  throw lastErr ?? new Error('查询失败');
}

/** 查询某个 registry 上 @deepseek-ai/dsh 的 latest dist-tag 版本。 */
async function dshLatest(registry: string): Promise<string> {
  const base = registry.replace(/\/+$/, '');
  const url = `${base}/@deepseek-ai/dsh/latest`;
  const p = await getJSON<{ version?: string }>(url);
  if (!p.version) throw new Error(`${url} 未返回 version 字段`);
  return p.version;
}

/** 请求 JSON 接口并解码。GitHub API 拒绝无 User-Agent 的请求（403）。 */
async function getJSON<T>(url: string): Promise<T> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), httpTimeoutMs);
  try {
    const resp = await fetch(url, {
      signal: ctrl.signal,
      headers: { 'User-Agent': 'dsh-launcher/update-check', Accept: 'application/json' },
    });
    if (resp.status !== 200) {
      throw new Error(`HTTP ${resp.status}（${url}）`);
    }
    return (await resp.json()) as T;
  } finally {
    clearTimeout(timer);
  }
}
