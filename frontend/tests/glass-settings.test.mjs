import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'

const source = await readFile(new URL('../src/utils/glassSettings.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
const { normalizeGlassSettings, defaultGlassSettings, glassPresets, identifyGlassPreset, glassCssSnippet } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)

test('损坏的外观配置不会变成 CSS，有限参数钳制到允许范围', () => {
  for (const raw of [null, [], 'bad', { blur: '20px', saturate: Infinity, opacity: NaN, rim: {}, refraction: 'true' }]) {
    assert.deepEqual(normalizeGlassSettings(raw), defaultGlassSettings)
  }
  assert.deepEqual(normalizeGlassSettings({ blur: -10, saturate: 999, opacity: 5, rim: 101, refraction: true }), { blur: 0, saturate: 220, opacity: 25, rim: 100, refraction: true })
  assert.equal(normalizeGlassSettings({ blur: 12.8 }).blur, 13)
  assert.equal(normalizeGlassSettings({ blur: 999 }).blur, 24)
})

test('参数决定当前预设，实验性折射偏好不改变四项材质的预设判断', () => {
  for (const preset of glassPresets) {
    assert.equal(identifyGlassPreset(normalizeGlassSettings(preset.settings)), preset.value)
    assert.equal(identifyGlassPreset({ ...preset.settings, refraction: true }), preset.value)
  }
  assert.equal(identifyGlassPreset({ ...defaultGlassSettings, blur: 1 }), 'custom')
})

test('复制的 CSS 参数带实际单位和真实换行，可用于公共玻璃样式', () => {
  const snippet = glassCssSnippet(normalizeGlassSettings({ blur: 13, saturate: 160, opacity: 65, rim: 20 }))
  for (const line of ['--user-glass-blur: 13px;', '--user-glass-saturate: 1.60;', '--user-glass-opacity: 0.65;', '--user-glass-rim: 0.20;']) assert.ok(snippet.includes(line))
  assert.ok(snippet.includes('\n:root {\n'))
  assert.ok(!snippet.includes('undefined'))
})
