// index.ts —— 入口：初始化日志并分发命令。

import { dispatch } from './cli.js';
import * as log from './log.js';

async function main(): Promise<void> {
  log.initLog();
  log.setDebug(process.env.DSH_LAUNCHER_DEBUG !== undefined);

  try {
    await dispatch(process.argv.slice(2));
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e);
    log.error(msg);
    // install/start/stop/move 等命令失败以退出码 1 结束；
    // UI 模式下的服务常驻错误由服务器内部捕获，不走到这里。
    process.exitCode = 1;
  }
}

void main();
