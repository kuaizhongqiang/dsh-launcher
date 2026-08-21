/* app.js —— dsh-launcher 界面逻辑
   预览模式：默认跑模拟数据（window.launcherBridge 不存在时）。
   真实模式：Node SEA 内嵌 http 服务在页面注入 window.launcherBridge（REST + SSE），
   本文件自动切换。 */

'use strict';

/* ---------- 模拟桥接层 ---------- */

const mock = {
  async getStatus() {
    return {
      node: { present: true, version: 'v24.14.0' },
      npm:  { present: true, version: '11.3.2' },
      dsh:  { installed: true, version: 'v0.1.0-rc.7' },
      port: { number: 3080, running: state.running },
      update: { checking: false, dshAvail: false, launcherAvail: false },
      defaultDir: 'C:\\Users\\kua\\AppData\\Local\\dsh',
      installedDir: 'C:\\Users\\kua\\AppData\\Local\\dsh',
    };
  },
  async start() { await delay(900); return { ok: true }; },
  async stop()  { await delay(700); return { ok: true }; },
  async install(dir) {
    await delay(300);
    streamInstallLogs();
    await delay(2600);
    return { ok: true };
  },
  async move(dir) { await delay(800); return { ok: true }; },
  async checkUpdate() { await delay(1200); return { dshAvail: false, launcherAvail: false }; },
  async browse() { return 'C:\\Users\\kua\\AppData\\Local\\dsh'; },
};

const bridge = window.launcherBridge || mock;

/* ---------- 模拟状态 ---------- */

const state = {
  busy: false,
  running: false,
  installed: true,
  updating: false,
};

function delay(ms) { return new Promise((r) => setTimeout(r, ms)); }

/* ---------- DOM ---------- */

const $ = (id) => document.getElementById(id);
const fields = {
  node: $('node'), npm: $('npm'), dsh: $('dsh'), port: $('port'), update: $('update'),
};
const rowEls = {
  node: document.querySelector('[data-key="node"] .dot'),
  npm: document.querySelector('[data-key="npm"] .dot'),
  dsh: document.querySelector('[data-key="dsh"] .dot'),
  port: document.querySelector('[data-key="port"] .dot'),
  update: document.querySelector('[data-key="update"] .dot'),
};
const logBox = $('log');

/* ---------- 日志 ---------- */

function log(line, kind = '') {
  const el = document.createElement('div');
  // 服务端推送的行已带时间戳（2026-08-21 12:00:00.000 [INFO] ...），不再重复加
  const hasTs = /^\[\d{4}-\d{2}-\d{2}/.test(line);
  if (!hasTs) {
    const t = new Date().toLocaleTimeString('zh-CN', { hour12: false });
    const time = document.createElement('span');
    time.className = 'line-time';
    time.textContent = '[' + t + ']';
    el.appendChild(time);
  }
  const text = document.createElement('span');
  text.className = 'line-' + kind;
  text.textContent = line;
  el.appendChild(text);
  logBox.appendChild(el);
  // 限行 + 自动滚动
  while (logBox.children.length > 300) logBox.removeChild(logBox.firstChild);
  logBox.scrollTop = logBox.scrollHeight;
}

function streamInstallLogs() {
  const lines = [
    ['检测 npm 源：registry.npmjs.org …', ''],
    ['官方源延迟 42ms，使用官方源', ''],
    ['npm install -g --prefix C:\\Users\\kua\\AppData\\Local\\dsh @deepseek-ai/dsh', 'brand'],
    ['added 218 packages in 9.1s', 'ok'],
    ['dsh 安装完成（v0.1.0-rc.7）', 'ok'],
  ];
  lines.forEach(([text, kind], i) => setTimeout(() => log(text, kind), 350 + i * 420));
}

/* ---------- 渲染状态 ---------- */

function setDot(key, cls) {
  rowEls[key].className = 'dot ' + cls;
}

function setValue(key, text, cls = '') {
  fields[key].textContent = text;
  fields[key].className = 'row-value' + (cls ? ' is-' + cls : '');
}

async function refreshStatus() {
  const s = await bridge.getStatus();
  // 同步状态，供按钮启用/文案使用（此前只更新显示、未同步 state）
  state.installed = s.dsh.installed;
  state.running = s.port.running;
  if (s.node.present) {
    setValue('node', 'Node.js  ' + s.node.version);
    setDot('node', 'dot-green');
  } else {
    setValue('node', 'Node.js  未安装', 'red');
    setDot('node', 'dot-red');
  }
  if (s.npm.present) {
    setValue('npm', 'npm  ' + s.npm.version);
    setDot('npm', 'dot-green');
  } else {
    setValue('npm', 'npm  未安装', 'red');
    setDot('npm', 'dot-red');
  }
  if (s.dsh.installed) {
    setValue('dsh', 'dsh  ' + s.dsh.version + '  已安装', 'green');
    setDot('dsh', 'dot-green');
  } else {
    setValue('dsh', 'dsh  未安装', 'red');
    setDot('dsh', 'dot-red');
  }
  if (s.dsh.installed) {
    if (s.port.running) {
      setValue('port', '端口  ' + s.port.number + '  运行中', 'green');
      setDot('port', 'dot-green');
    } else {
      setValue('port', '端口  ' + s.port.number + '  未运行', 'dim');
      setDot('port', 'dot-dim');
    }
  } else {
    setValue('port', '端口  —', 'dim');
    setDot('port', 'dot-dim');
  }
  if (s.update.checking) {
    setValue('update', '更新  正在检查…', 'dim');
    setDot('update', 'dot-dim');
  } else if (s.update.dshAvail || s.update.launcherAvail) {
    setValue('update', '更新  有新版本可升级', 'brand');
    setDot('update', 'dot-brand');
  } else {
    setValue('update', '更新  已是最新', 'green');
    setDot('update', 'dot-green');
  }
}

function renderButtons() {
  const start = $('btnStart');
  start.textContent = state.running ? '已运行' : '启动';
  start.disabled = state.busy;
  $('btnStop').disabled = state.busy || !state.running;
  $('btnInstall').disabled = state.busy;
  $('btnMove').disabled = state.busy;
  $('btnBrowse').disabled = state.busy;
  $('btnUpdate').disabled = state.busy || state.updating;
  $('btnUpdate').textContent = state.updating ? '检查中…' : '检查更新';
}

function setBusy(b) {
  state.busy = b;
  renderButtons();
}

function setProgress(on, text) {
  $('progress').hidden = !on;
  if (text) $('progressText').textContent = text;
}

/* ---------- 交互 ---------- */

async function onStart() {
  if (state.busy) return;
  if (state.running) { log('dsh 已在运行，打开浏览器…', 'ok'); return; }
  setBusy(true);
  log('启动 dsh…', 'brand');
  try {
    const r = await bridge.start();
    if (!r.ok) throw new Error(r.message || '启动失败');
    state.running = true;
    log(r.already ? 'dsh 已在运行，已打开浏览器。' : 'dsh 已启动并独立运行（关闭本窗口不影响 dsh）。', 'ok');
  } catch (e) {
    log('启动失败：' + e.message, 'err');
  }
  setBusy(false);
  await refreshStatus();
  renderButtons();
}

async function onStop() {
  if (state.busy || !state.running) return;
  setBusy(true);
  log('停止 dsh（端口 3080）…', 'brand');
  try {
    const r = await bridge.stop();
    if (!r.ok) throw new Error(r.message || '停止失败');
    state.running = false;
    log('已停止 dsh。', 'ok');
  } catch (e) {
    log('停止失败：' + e.message, 'err');
  }
  setBusy(false);
  await refreshStatus();
  renderButtons();
}

async function onInstall() {
  if (state.busy) return;
  const dir = $('pathInput').value.trim();
  if (!dir) { log('请先选择安装目录。', 'warn'); return; }
  setBusy(true);
  setProgress(true, '安装中…');
  log('开始安装到 ' + dir + ' …', 'brand');
  try {
    const r = await bridge.install(dir);
    if (!r.ok) throw new Error(r.message || '安装失败');
    state.installed = true;
    log('安装完成。可以点击「启动」。', 'ok');
  } catch (e) {
    log('安装失败：' + e.message, 'err');
  }
  setProgress(false);
  setBusy(false);
  await refreshStatus();
  renderButtons();
}

async function onMove() {
  if (state.busy) return;
  const dir = await bridge.browse();
  if (!dir) { log('已取消移动。', 'dim'); return; }
  setBusy(true);
  log('移动 dsh 到 ' + dir + ' …', 'brand');
  try {
    const r = await bridge.move(dir);
    if (!r.ok) throw new Error(r.message || '移动失败');
    $('pathInput').value = dir;
    log('移动完成。', 'ok');
  } catch (e) {
    log('移动失败：' + e.message, 'err');
  }
  setBusy(false);
  renderButtons();
}

async function onBrowse() {
  if (state.busy) return;
  const dir = await bridge.browse();
  if (dir) {
    $('pathInput').value = dir;
    log('安装目录：' + dir, '');
  }
}

async function onUpdate() {
  if (state.busy || state.updating) return;
  state.updating = true;
  renderButtons();
  log('检查更新：dsh（npm registry）与启动器（GitHub Release）…', 'brand');
  try {
    const r = await bridge.checkUpdate();
    if (r.dshAvail || r.launcherAvail) {
      const parts = [];
      if (r.dshAvail) parts.push('dsh → ' + (r.dshLatest || '最新'));
      if (r.launcherAvail) parts.push('启动器 → ' + (r.launcherLatest || '最新'));
      log('发现新版本（' + parts.join('；') + '）。dsh 运行「安装」即升级，启动器到 GitHub Release 下载。', 'warn');
    } else {
      log('dsh 与启动器均已是最新。', 'ok');
    }
  } catch (e) {
    log('更新检测失败：' + e.message, 'err');
  }
  state.updating = false;
  await refreshStatus();
  renderButtons();
}

function onExit() {
  log('dsh 仍在后台运行（独立进程）。关闭窗口不影响 dsh。', 'dim');
  // 桌面窗口：通过 preload 关闭窗口（触发 main 的 closed → app.quit）
  if (window.electronWindow && window.electronWindow.close) {
    window.electronWindow.close();
    return;
  }
  // 浏览器 / SEA 版：调用后端 /api/exit 结束本服务
  if (bridge.exit) {
    void bridge.exit();
  }
}

/* ---------- 启动 ---------- */

(async function init() {
  // 桌面窗口（Electron frameless）：显示最小化按钮 + 占满布局
  if (window.electronWindow && window.electronWindow.isDesktop) {
    document.body.classList.add('desktop');
    $('btnMin').hidden = false;
    $('btnMin').addEventListener('click', () => window.electronWindow.minimize());
  }
  // 真实模式下订阅服务端日志流
  if (bridge.onLog) {
    bridge.onLog((line, kind) => log(line, kind));
  }
  // 版本号 + 默认安装目录预填
  if (window.launcherVersion) {
    $('ver').textContent = window.launcherVersion;
  }
  try {
    const s0 = await bridge.getStatus();
    if (!window.launcherBridge && !state.installed) {
      // 预览模式：先不预填，保持模拟效果
    } else if (s0.installedDir) {
      $('pathInput').value = s0.installedDir;
    } else if (s0.defaultDir && !$('pathInput').value.trim()) {
      $('pathInput').value = s0.defaultDir;
    }
  } catch (e) {
    /* 忽略 */
  }
  log('dsh-launcher 已就绪。', '');
  $('btnStart').addEventListener('click', onStart);
  $('btnStop').addEventListener('click', onStop);
  $('btnInstall').addEventListener('click', onInstall);
  $('btnMove').addEventListener('click', onMove);
  $('btnBrowse').addEventListener('click', onBrowse);
  $('btnUpdate').addEventListener('click', onUpdate);
  $('btnExit').addEventListener('click', onExit);
  $('btnClose').addEventListener('click', onExit);

  await refreshStatus();
  renderButtons();
})();

/* ---------- 真实后端契约（Node SEA 版，由服务端注入） ----------
   window.launcherVersion: string
   window.launcherBridge = {
     getStatus(): Promise<{ node, npm, dsh, port, update, defaultDir, installedDir }>,
     start(): Promise<{ok}>,
     stop(): Promise<{ok}>,
     install(dir): Promise<{ok}>,
     move(dir): Promise<{ok}>,
     checkUpdate(): Promise<{dshAvail, launcherAvail, dshLatest, launcherLatest}>,
     browse(): Promise<string|null>,
     defaultDir: string,
     onLog(fn): void   // 订阅日志流（SSE /api/events），fn(line, kind)
   }
*/
