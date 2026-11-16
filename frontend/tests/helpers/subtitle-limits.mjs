import { readFileSync } from 'node:fs'
import ts from 'typescript'

// 加载真实限制，供字幕工具的 CommonJS 测试环境复用。
const compiled = ts.transpileModule(readFileSync(new URL('../../src/utils/subtitleLimits.ts', import.meta.url), 'utf8'), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
}).outputText
const module = { exports: {} }
new Function('module', 'exports', compiled)(module, module.exports)
export const subtitleLimits = module.exports
