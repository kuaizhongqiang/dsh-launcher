// version.ts —— 版本号单一来源（package.json）。

import pkg from '../package.json';

export const VERSION: string = pkg.version;
