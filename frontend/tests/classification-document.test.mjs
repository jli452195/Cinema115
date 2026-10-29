import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'

const source = await readFile(new URL('../src/utils/classificationDocument.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText.replace("from 'yaml'", `from ${JSON.stringify(import.meta.resolve('yaml'))}`)
const { parseClassification, writeClassification, validateClassification, classificationIssue, classificationSignature } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
const defaults = await readFile(new URL('../../backend/internal/mediacategory/default.yaml', import.meta.url), 'utf8')
const clone = value => structuredClone(value)

test('editing and reordering preserve comments and leave the original AST unchanged', () => {
  const text = defaults.replace('    - name: 动画电影', '    # animation rule\n    - name: 动画电影 # title comment').replace('genres: [16]', 'genres: [16] # genre comment').replace('path: 电影/动画电影', 'path: 电影/动画电影 # path comment') + '\n# end comment\n'
  const editor = parseClassification(text), config = clone(editor.config)
  config.movie.rules.reverse()
  config.movie.rules[1].name = '动画优先级二'
  config.movie.rules[1].path = '电影/日本动画'
  config.movie.rules[1].match.origin_countries = ['JP']
  const written = writeClassification(editor, config)
  for (const comment of ['animation rule', 'title comment', 'genre comment', 'path comment', 'end comment']) assert.ok(written.includes(`# ${comment}`))
  assert.equal(classificationSignature(parseClassification(written).config), classificationSignature(config))
  assert.equal(editor.document.toString(), parseClassification(text).document.toString())
  assert.equal(editor.config.movie.rules[0].name, '动画电影')
})

test('new, removed and empty rule lists serialize with stable semantic dirty checks', () => {
  const editor = parseClassification(defaults), config = clone(editor.config)
  config.tv.rules = []
  config.movie.rules = [{ key: 'new', name: '其他语言', path: '电影/其他语言', match: { languages: ['de', 'es'] } }]
  const written = writeClassification(editor, config), parsed = parseClassification(written)
  assert.equal(classificationIssue(parsed.config), undefined)
  assert.equal(classificationSignature(config), classificationSignature(parsed.config))
  assert.deepEqual(parsed.config.tv.rules, [])
  assert.ok(!written.includes('name: 动漫'))
})

test('an empty added condition cannot silently broaden an existing rule', () => {
  const config = parseClassification(defaults).config
  config.movie.rules[0].match.origin_countries = []
  assert.match(classificationIssue(config).message, /至少选择一个匹配值/)
  delete config.movie.rules[0].match.origin_countries
  config.movie.rules[0].path = '../escape'
  assert.match(classificationIssue(config).message, /相对目录/)
  config.movie.rules[0].path = '电影/动画'
  config.movie.rules[1].name = config.movie.rules[0].name
  assert.match(classificationIssue(config).message, /同名规则/)
})

test('unsupported structures are rejected before visual projection can discard them', () => {
  for (const text of [
    defaults + '\nunknown: true', defaults + '\n---\nversion: 1',
    defaults.replace('genres: [16]', 'genres: &values [16]'),
    defaults.replace('genres: [16]', 'genres: [16, 16]'),
    defaults.replace('genres: [16]', 'genres: ["16"]'),
    defaults.replace('genres: [16]', 'extra: [16]'),
    defaults.replace('genres: [16]', 'genres: !custom [16]'),
  ]) assert.throws(() => parseClassification(text))
  const tagged = defaults.replace('version: 1', 'version: !!int 1').replace('genres: [16]', 'genres: [!!int 16]').replace('languages: [zh, cn]', 'languages: [!!str zh, cn]')
  assert.equal(classificationSignature(parseClassification(tagged).config), classificationSignature(parseClassification(defaults).config))
})

test('unchanged visual/source round trips preserve exact source and the latest parse baseline', () => {
  const original = defaults.replace(/\r?\n/g, '\r\n').trimEnd() + ' # final comment'
  const editor = parseClassification(original), config = clone(editor.config)
  config.movie.rules[0].key = 'ui-only-key'
  assert.equal(editor.source, original)
  assert.equal(writeClassification(editor, config), original)
  const changedSource = original.replace('电影/动画电影', '电影/新的目录') + '\r\n# new source comment\r\n'
  const reparsed = parseClassification(changedSource)
  assert.equal(writeClassification(reparsed, clone(reparsed.config)), changedSource)
  config.movie.rules[0].name = '新视觉名称'
  const written = writeClassification(editor, config)
  assert.equal(parseClassification(written).config.movie.rules[0].name, '新视觉名称')
  assert.ok(written.includes('# final comment'))
})

test('explicit empty lists survive serialization and strict validation rejects them at the field', () => {
  const editor = parseClassification(defaults), config = clone(editor.config)
  config.movie.rules[0].match.genres = []
  config.movie.rules[0].match.languages = []
  const written = writeClassification(editor, config), parsed = parseClassification(written)
  assert.deepEqual(parsed.config.movie.rules[0].match, { genres: [], languages: [] })
  assert.throws(() => validateClassification(parsed), error => error.diagnostic?.field === 'movie.rules[0].match.genres')
  delete config.movie.rules[0].match.languages
  assert.equal(parseClassification(writeClassification(editor, config)).config.movie.rules[0].match.languages, undefined)
})

test('syntax and structural failures include their original field and line/column', () => {
  const unknown = defaults.replace('genres: [16]', 'unexpected: [16]')
  assert.throws(() => parseClassification(unknown), error => {
    assert.equal(error.diagnostic.field, 'movie.rules[0].match.unexpected')
    const before = unknown.slice(0, unknown.indexOf('unexpected'))
    assert.equal(error.diagnostic.line, before.split('\n').length)
    assert.equal(error.diagnostic.column, before.length - before.lastIndexOf('\n'))
    return true
  })
  const broken = 'version: 1\nmovie: [broken\n'
  assert.throws(() => parseClassification(broken), error => {
    assert.equal(error.diagnostic.field, '$')
    assert.ok(error.diagnostic.line >= 2)
    assert.ok(error.diagnostic.column >= 1)
    return true
  })
  assert.throws(() => parseClassification(defaults.replace('genres: [16]', 'genres: [16, 16]')), error => error.diagnostic?.field === 'movie.rules[0].match.genres[1]')
  assert.throws(() => parseClassification(defaults.replace('genres: [16]', 'genres: []\n        genres: [99]')), error => error.diagnostic?.line >= 1 && error.diagnostic?.column >= 1)
})

test('incomplete visual fields remain representable while source validation requires valid rules', () => {
  for (const [from, to, field] of [
    ['name: 动画电影', "name: ' '", 'movie.rules[0].name'],
    ['path: 电影/动画电影', "path: ''", 'movie.rules[0].path'],
    ['fallback: 电影/其他电影', "fallback: ''", 'movie.fallback'],
    ['genres: [16]', 'genres: []', 'movie.rules[0].match.genres'],
    ['match:\n        genres: [16]', 'match: {}', 'movie.rules[0].match'],
    ['name: 华语电影', 'name: 动画电影', 'movie.rules[1].name'],
  ]) {
    const source = defaults.replace(from, to), editor = parseClassification(source)
    assert.throws(() => validateClassification(editor), error => {
      assert.equal(error.diagnostic.field, field)
      assert.ok(error.diagnostic.line >= 1)
      assert.ok(error.diagnostic.column >= 1)
      assert.equal(writeClassification(editor, clone(editor.config)), source)
      return true
    })
  }
})

test('directory validation matches cross-platform names and UTF-8 component limits', () => {
  for (const path of ['.actors', '电影/CON', '电影/LPT¹', '电影/CONIN$', '电影/a\u0085b', '影'.repeat(81), '/电影', '电影/../其他', '电影/a.']) {
    const editor = parseClassification(defaults.replace('path: 电影/动画电影', `path: ${JSON.stringify(path)}`))
    assert.throws(() => validateClassification(editor), error => error.diagnostic?.field === 'movie.rules[0].path')
  }
  for (const path of ['电影/CONSOLE', '电影/COM10', '影'.repeat(80)]) {
    validateClassification(parseClassification(defaults.replace('path: 电影/动画电影', `path: ${path}`)))
  }
})

test('integer syntax preserves Go YAML semantics and rejects float or text values', () => {
  for (const source of [
    defaults.replace('version: 1', 'version: 1.0'),
    defaults.replace('version: 1', 'version: 1e0'),
    ...['16.0', '1e1', '!!float 16', '!!str 16', '"16"', '0', '-1', '08'].map(value => defaults.replace('genres: [16]', `genres: [${value}]`)),
  ]) assert.throws(() => parseClassification(source), error => ['version', 'movie.rules[0].match.genres[0]'].includes(error.diagnostic?.field))
  for (const [notation, expected] of [['016', 14], ['0x10', 16], ['0o20', 16], ['0b10000', 16], ['1_6', 16], ["!!int '16'", 16]]) {
    const source = defaults.replace('genres: [16]', `genres: [${notation}] # integer comment`)
    const editor = parseClassification(source), config = clone(editor.config)
    assert.deepEqual(config.movie.rules[0].match.genres, [expected])
    assert.equal(writeClassification(editor, config), source)
    config.movie.rules[0].name = '其他名称'
    const written = writeClassification(editor, config)
    assert.deepEqual(parseClassification(written).config.movie.rules[0].match.genres, [expected])
    assert.ok(written.includes('# integer comment'))
  }
  assert.throws(() => parseClassification(defaults.replace('name: 动画电影', '!!int name: 动画电影')), error => !!error.diagnostic)
})
