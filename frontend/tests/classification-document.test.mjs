import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'

const source = await readFile(new URL('../src/utils/classificationDocument.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText.replace("from 'yaml'", `from ${JSON.stringify(import.meta.resolve('yaml'))}`)
const { parseClassification, writeClassification, classificationIssue, classificationSignature } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
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
    defaults.replace('genres: [16]', 'genres: []'),
    defaults.replace('genres: [16]', 'extra: [16]'),
    defaults.replace('genres: [16]', 'genres: !custom [16]'),
  ]) assert.throws(() => parseClassification(text))
  const tagged = defaults.replace('version: 1', 'version: !!int 1').replace('genres: [16]', 'genres: [!!int 16]').replace('languages: [zh, cn]', 'languages: [!!str zh, cn]')
  assert.equal(classificationSignature(parseClassification(tagged).config), classificationSignature(parseClassification(defaults).config))
})
