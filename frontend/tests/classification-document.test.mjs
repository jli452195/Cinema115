import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'

const source = await readFile(new URL('../src/utils/classificationDocument.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText.replace("from 'yaml'", `from ${JSON.stringify(import.meta.resolve('yaml'))}`)
const { parseClassification, writeClassification, validateClassification, classificationIssue, classificationSignature, classificationDirectory, importMoviePilotClassification } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
const defaults = await readFile(new URL('../../backend/internal/mediacategory/default.yaml', import.meta.url), 'utf8')
const moviePilot = await readFile(new URL('./fixtures/moviepilot-classification.yaml', import.meta.url), 'utf8')
const clone = value => structuredClone(value)

test('editing and reordering preserve comments and leave the original AST unchanged', () => {
  const text = defaults.replace('    - name: 动画电影', '    # animation rule\n    - name: 动画电影 # title comment').replace('genres: [16]', 'genres: [16] # genre comment') + '\n# end comment\n'
  const editor = parseClassification(text), config = clone(editor.config)
  config.movie.rules.reverse()
  config.movie.rules[1].name = '动画优先级二'
  config.movie.rules[1].match.countries = ['JP']
  const written = writeClassification(editor, config)
  for (const comment of ['animation rule', 'title comment', 'genre comment', 'end comment']) assert.ok(written.includes(`# ${comment}`))
  assert.equal(classificationSignature(parseClassification(written).config), classificationSignature(config))
  assert.equal(editor.document.toString(), parseClassification(text).document.toString())
  assert.equal(editor.config.movie.rules[0].name, '动画电影')
})

test('new, removed and empty rule lists serialize with stable semantic dirty checks', () => {
  const editor = parseClassification(defaults), config = clone(editor.config)
  config.tv.rules = []
  config.movie.rules = [{ key: 'new', name: '其他语言', match: { languages: ['de', 'es'] } }]
  const written = writeClassification(editor, config), parsed = parseClassification(written)
  assert.equal(classificationIssue(parsed.config), undefined)
  assert.equal(classificationSignature(config), classificationSignature(parsed.config))
  assert.deepEqual(parsed.config.tv.rules, [])
  assert.ok(!written.includes('name: 动漫'))
  assert.ok(!written.includes('path:'))
  assert.match(written, /languages: \[de, es\]/)
  assert.equal(parsed.config.movie.fallback, '其他电影')
  assert.equal(classificationDirectory('movie', parsed.config.movie.rules[0].name), '电影/其他语言')
})

test('an empty added condition cannot silently broaden an existing rule', () => {
  const config = parseClassification(defaults).config
  config.movie.rules[0].match.countries = []
  assert.match(classificationIssue(config).message, /至少选择一个匹配值/)
  delete config.movie.rules[0].match.countries
  config.movie.rules[0].name = '../escape'
  assert.match(classificationIssue(config).message, /相对目录/)
  config.movie.rules[0].name = '动画/日语'
  config.movie.rules[1].name = config.movie.rules[0].name
  assert.match(classificationIssue(config).message, /同名分类/)
})

test('unsupported structures are rejected before visual projection can discard them', () => {
  for (const text of [
    defaults + '\nunknown: true', defaults + '\n---\nmovie: {}',
    defaults.replace('genres: [16]', 'genres: &values [16]'),
    defaults.replace('genres: [16]', 'genres: [16, 16]'),
    defaults.replace('genres: [16]', 'genres: ["16"]'),
    defaults.replace('genres: [16]', 'extra: [16]'),
    defaults.replace('genres: [16]', 'genres: !custom [16]'),
    defaults.replace('genres: [16]', 'origin_countries: [JP]'),
    defaults.replace('genres: [16]', 'production_countries: [JP]'),
    defaults.replace('name: 动画电影', 'name: 动画电影\n      path: 电影/动画电影'),
  ]) assert.throws(() => parseClassification(text))
  const tagged = defaults.replace('genres: [16]', 'genres: [!!int 16]').replace('languages: [zh, cn]', 'languages: [!!str zh, cn]')
  assert.equal(classificationSignature(parseClassification(tagged).config), classificationSignature(parseClassification(defaults).config))
  const countries = parseClassification(defaults.replace('genres: [16]', "countries: [jp, ' cn ']"))
  assert.deepEqual(countries.config.movie.rules[0].match.countries, ['JP', 'CN'])
  assert.throws(() => parseClassification(defaults.replace('genres: [16]', 'countries: [JP, jp]')), error => error.diagnostic?.field === 'movie.rules[0].match.countries[1]')
})

test('unchanged visual/source round trips preserve exact source and the latest parse baseline', () => {
  const original = defaults.replace(/\r?\n/g, '\r\n').trimEnd() + ' # final comment'
  const editor = parseClassification(original), config = clone(editor.config)
  config.movie.rules[0].key = 'ui-only-key'
  assert.equal(editor.source, original)
  assert.equal(writeClassification(editor, config), original)
  const changedSource = original.replace('name: 动画电影', 'name: 新分类') + '\r\n# new source comment\r\n'
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
  const broken = 'movie:\n  rules: [broken\n'
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
    ['name: 动画电影', "name: '../错误'", 'movie.rules[0].name'],
    ['fallback: 其他电影', "fallback: ''", 'movie.fallback'],
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
    const editor = parseClassification(defaults.replace('name: 动画电影', `name: ${JSON.stringify(path)}`))
    assert.throws(() => validateClassification(editor), error => error.diagnostic?.field === 'movie.rules[0].name')
  }
  for (const path of ['电影/CONSOLE', '电影/COM10', '影'.repeat(80)]) {
    validateClassification(parseClassification(defaults.replace('name: 动画电影', `name: ${path}`)))
  }
  const trimmed = parseClassification(defaults.replace('name: 动画电影', "name: ' 动画电影 '"))
  assert.equal(trimmed.config.movie.rules[0].name, '动画电影')
  assert.equal(classificationDirectory('tv', ' 动画/日语 '), '剧集/动画/日语')
  assert.throws(() => validateClassification(parseClassification(defaults.replace('name: 华语电影', "name: ' 动画电影 '"))), error => error.diagnostic?.field === 'movie.rules[1].name')
  assert.throws(() => validateClassification(parseClassification(defaults.replace('fallback: 其他电影', "fallback: ' 其他电影 '"))), error => error.diagnostic?.field === 'movie.fallback')
})

test('integer syntax preserves Go YAML semantics and rejects float or text values', () => {
  for (const value of ['16.0', '1e1', '!!float 16', '!!str 16', '"16"', 'true', 'null', '0', '-1', '08']) {
    const source = defaults.replace('genres: [16]', `genres: [${value}]`)
    assert.throws(() => parseClassification(source), error => error.diagnostic?.field === 'movie.rules[0].match.genres[0]')
  }
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

test('MP import preserves category order, matching conditions and comments in the native format', () => {
  const yaml = importMoviePilotClassification(moviePilot), config = parseClassification(yaml).config
  assert.deepEqual(config.movie.rules.map(rule => rule.name), ['动画电影', '华语电影', '日韩电影', '欧美电影'])
  assert.deepEqual(config.tv.rules.map(rule => rule.name), ['儿童', '国漫', '日漫', '美漫', '纪录片', '综艺', '国产剧', '欧美剧', '日韩剧'])
  assert.equal(config.movie.fallback, '其它电影'); assert.equal(config.tv.fallback, '未分类')
  assert.deepEqual(config.tv.rules[1].match, { genres: [16], countries: ['CN', 'TW', 'HK'] })
  assert.deepEqual(config.movie.rules[1].match.languages, ['zh', 'cn', 'bo', 'za'])
  assert.deepEqual(config.tv.rules[5].match.genres, [10764, 10767])
  assert.ok(config.tv.rules[3].match.countries.includes('UK'))
  assert.equal(classificationDirectory('movie', config.movie.rules[0].name), '电影/动画电影')
  assert.ok(!yaml.includes('path:')); assert.ok(!yaml.includes('genre_ids:'))
  assert.match(yaml, /genres: \[16\]/)
  assert.match(yaml, /languages: \[zh, cn, bo, za\]/)
  assert.match(yaml, /countries: \[US, FR, GB, DE, ES, IT, NL, PT, RU, UK\]/)
  assert.match(yaml, /fallback: 未分类/)
  assert.doesNotMatch(yaml, /^\s*- (?:[A-Za-z]{2}|\d+)\s*$/m)
  assert.ok(yaml.includes('电影按顺序匹配')); assert.ok(yaml.includes('动画类型')); assert.ok(yaml.includes('未分类兜底'))
  validateClassification(parseClassification(yaml))
})

test('MP conversion is confined to import; editing and saving use readable native YAML only', () => {
  assert.throws(() => parseClassification(moviePilot))
  const source = moviePilot.replace("    origin_country: 'CN,TW,HK'", "    # 匹配 origin_country 国家\n    origin_country: 'CN,TW,HK'")
  const yaml = importMoviePilotClassification(source)
  assert.ok(yaml.includes('# 匹配 countries 国家'))
  assert.doesNotMatch(yaml, /\b(?:genre_ids|original_language|production_countries|origin_country)\b/)
  const editor = parseClassification(yaml), config = clone(editor.config)
  config.tv.rules[1].match.countries.push('SG')
  config.movie.rules[0].match.languages = ['ja']
  const written = writeClassification(editor, config)
  assert.match(written, /countries: \[CN, TW, HK, SG\]/)
  assert.match(written, /languages: \[ja\]/)
  assert.doesNotMatch(written, /^\s*- (?:[A-Za-z]{2}|\d+)\s*$/m)
  validateClassification(parseClassification(written))
  assert.equal(writeClassification(parseClassification(written), parseClassification(written).config), written)
  const expanded = yaml.replace('countries: [CN, TW, HK]', 'countries:\n          - CN\n          - TW\n          - HK')
  const existing = parseClassification(expanded), edited = clone(existing.config)
  assert.equal(writeClassification(existing, edited), expanded)
  edited.tv.rules[1].name = '华语动画'
  assert.match(writeClassification(existing, edited), /countries: \[CN, TW, HK\]/)
})

test('MP import accepts final empty maps and movie production countries without changing the input document', () => {
  const source = moviePilot.replace('  其它电影:', '  其它电影: {}').replace("genre_ids: '16'", "genre_ids: '16'\n    production_countries: 'jp,CN'")
  const imported = parseClassification(importMoviePilotClassification(source)).config
  assert.deepEqual(imported.movie.rules[0].match, { genres: [16], countries: ['JP', 'CN'] })
  assert.equal(imported.movie.fallback, '其它电影')
  assert.ok(source.includes('genre_ids:'))
})

test('MP import rejects ambiguous order, unsupported conditions and invalid values with source locations', () => {
  for (const source of [
    moviePilot.replace('movie:\n', 'movie:\n  提前兜底:\n'),
    moviePilot.replace('  其它电影:\n', ''),
    moviePilot.replace('  其它电影:\n', '  其它电影:\n  第二兜底:\n'),
    moviePilot.replace("genre_ids: '16'", "year: '2026'"),
    moviePilot.replace("genre_ids: '16'", "origin_country: 'JP'"),
    moviePilot.replace("origin_country: 'JP,KP,KR'", "production_countries: 'JP'"),
    ...["'0'", "'-1'", "'16.0'", "'16,,99'", "'16,16'", '16', '[]', 'null'].map(value => moviePilot.replace("genre_ids: '16'", `genre_ids: ${value}`)),
    moviePilot.replace("origin_country: 'JP,KP,KR'", "origin_country: 'JPN'"),
    moviePilot.replace("origin_country: 'JP,KP,KR'", "origin_country: 'JP,jp'"),
    moviePilot.replace('  动画电影:', '  ../动画电影:'),
    moviePilot.replace('  华语电影:', "  ' 动画电影 ':"),
    moviePilot + '\nextra: true\n',
    moviePilot + '\n---\nmovie: {}\n',
  ]) assert.throws(() => importMoviePilotClassification(source), error => !!error.diagnostic?.field && error.diagnostic.line > 0 && error.diagnostic.column > 0)
})
