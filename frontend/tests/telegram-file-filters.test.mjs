import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

function loadModule(filename, dependencies = {}) {
  const module = { exports: {} }
  const code = ts.transpileModule(readFileSync(new URL(filename, import.meta.url), 'utf8'), { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
  new Function('module', 'exports', 'require', code)(module, module.exports, name => {
    assert.ok(name in dependencies, `未声明的模块依赖：${name}`)
    return dependencies[name]
  })
  return module.exports
}
const filesModule = loadModule('../src/utils/telegramFiles.ts')
const { defaultTelegramFileFilters, resolveTelegramFileFilters } = loadModule('../src/utils/telegramFileFilters.ts', { './telegramFiles': filesModule })
const file = (id, season, resolution, dynamicRange, platform, releaseGroup) => ({ id, name: `Show.${id}.mkv`, version: '测试版本', versionKey: 'test', sizeBytes: '1000', sources: [], season, metadata: { resolution, dynamicRange, platform, releaseGroup } })
const files = [
  file('1080-nf-1', 1, '1080p', 'SDR', 'NF', 'HHWEB'),
  file('1080-nf-2', 1, '1080p', 'SDR', 'NF', 'HHWEB'),
  file('1080-youku', 1, '1080p', 'SDR', 'YOUKU', 'ADWeb'),
  file('4k-nf-1', 1, '2160p', 'DV', 'NF', 'HHWEB'),
  file('4k-nf-2', 1, '2160p', 'DV', 'NF', 'HHWEB'),
  file('4k-youku', 1, '2160p', 'HDR', 'YOUKU', 'ADWeb'),
  file('season-2', 2, '720p', 'SDR', 'AMZN', 'Other'),
  file('unknown'),
]
const values = extra => ({ ...defaultTelegramFileFilters(), ...extra })
const facet = (result, key) => result.facets.find(item => item.key === key)
const options = (result, key) => facet(result, key).options.map(option => [option.value, option.count])

test('按季、清晰度、动态范围、平台、发布组逐级收窄，选项计数不受下游条件影响', () => {
  const result = resolveTelegramFileFilters(files, values({ season: '1', resolution: '2160p', dynamicRange: 'DV' }))
  assert.deepEqual(result.facets.map(item => item.key), ['season', 'resolution', 'dynamicRange', 'platform', 'releaseGroup'])
  assert.deepEqual(options(result, 'season'), [['all', 8], ['1', 6], ['2', 1]])
  assert.deepEqual(options(result, 'resolution'), [['all', 6], ['1080p', 3], ['2160p', 3]])
  assert.deepEqual(options(result, 'dynamicRange'), [['all', 3], ['DV', 2], ['HDR', 1]])
  assert.deepEqual(options(result, 'platform'), [['all', 2], ['NF', 2]])
  assert.deepEqual(options(result, 'releaseGroup'), [['all', 2], ['HHWEB', 2]])
  assert.equal(facet(result, 'dynamicRange').options[1].label, 'DV（2）')
  assert.equal(facet(result, 'resolution').selectedLabel, '2160p')
  assert.deepEqual(result.files.map(item => item.id), ['4k-nf-1', '4k-nf-2'])
})

test('切换上游只清理不兼容的后续条件，保留仍然有效的选择', () => {
  const original = values({ season: '1', resolution: '1080p', dynamicRange: 'SDR', platform: 'NF', releaseGroup: 'HHWEB' })
  const previous = resolveTelegramFileFilters(files, original)
  const changed = resolveTelegramFileFilters(files, { ...previous.values, resolution: '2160p' })
  assert.deepEqual(changed.values, { ...original, resolution: '2160p', dynamicRange: 'all' })
  assert.equal(changed.files.length, 2)
  const conflict = resolveTelegramFileFilters(files, values({ season: '1', resolution: '2160p', dynamicRange: 'DV', platform: 'YOUKU', releaseGroup: 'ADWeb' }))
  assert.equal(conflict.values.dynamicRange, 'DV')
  assert.equal(conflict.values.platform, 'all')
  assert.equal(conflict.values.releaseGroup, 'all')
  assert.equal(conflict.files.length, 2, '不保留截图中逐项存在、组合为零的条件')
  const nextSeason = resolveTelegramFileFilters(files, { ...conflict.values, season: '2' })
  assert.deepEqual(nextSeason.values, values({ season: '2' }))
  assert.equal(nextSeason.files[0].id, 'season-2')
})

test('关键词参与所有层级选项计算，冲突条件清理后不自动选替代值', () => {
  const result = resolveTelegramFileFilters(files, values({ resolution: '2160p', dynamicRange: 'DV', platform: 'NF', releaseGroup: 'HHWEB' }), 'YOUKU')
  assert.deepEqual(options(result, 'resolution'), [['all', 2], ['1080p', 1], ['2160p', 1]])
  assert.deepEqual(result.values, values({ resolution: '2160p' }))
  assert.equal(result.files[0].id, '4k-youku')
  assert.equal(facet(result, 'dynamicRange').value, 'all', '只剩 HDR 时不替用户自动选中 HDR')
  assert.equal(facet(result, 'dynamicRange').options[1].label, 'HDR（1）')
  const empty = resolveTelegramFileFilters(files, result.values, '不存在的关键词')
  assert.equal(empty.files.length, 0)
  assert.equal(empty.facets.every(item => item.options.length === 1 && item.value === 'all'), true)
})

test('未知规格只留在全部结果，不虚构可选值，唯一有效已选值仍可取消', () => {
  const result = resolveTelegramFileFilters([files.at(-1)], values())
  assert.equal(result.files.length, 1)
  assert.equal(result.facets.every(item => item.options.length === 1), true)
  const known = resolveTelegramFileFilters([files[3]], values({ resolution: '2160p' }))
  assert.equal(facet(known, 'resolution').options[0].value, 'all')
  assert.equal(facet(known, 'resolution').options.length, 2)
  assert.equal(facet(known, 'resolution').selectedLabel, '2160p')
  const seasons = resolveTelegramFileFilters([file('ten', 10), file('two', 2), file('special', 0)], values())
  assert.deepEqual(options(seasons, 'season'), [['all', 3], ['0', 1], ['2', 1], ['10', 1]])
  assert.equal(facet(seasons, 'season').options[1].label, '特别篇（1）')
})

test('自动追加只补充选项和数量，保留有效条件，移除的冲突条件不会重新出现', () => {
  const original = values({ resolution: '2160p', dynamicRange: 'DV', platform: 'NF' })
  const before = structuredClone(files), beforeValues = structuredClone(original)
  const previous = resolveTelegramFileFilters(files, original)
  const appended = file('new-4k', 1, '2160p', 'DV', 'HBO', 'NewGroup')
  const next = resolveTelegramFileFilters([...files, appended, file('new-1080', 1, '1080p', 'SDR', 'NF', 'NewGroup')], previous.values)
  assert.deepEqual(next.values, original)
  assert.deepEqual(options(next, 'platform'), [['all', 3], ['HBO', 1], ['NF', 2]])
  assert.equal(next.files.length, 2)
  assert.equal(next.values.releaseGroup, 'all')
  assert.deepEqual(files, before)
  assert.deepEqual(original, beforeValues)
  const repeated = resolveTelegramFileFilters(files, previous.values)
  assert.deepEqual(repeated, previous)
})

test('联动筛选保留来稿顺序，供显示层选择季集或大小排序', () => {
  const incoming = [
    { ...file('episode-10', 1, '2160p', 'HDR', 'NF', 'HHWEB'), episodeNumber: 10, sizeBytes: '3000' },
    { ...file('episode-2', 1, '2160p', 'HDR', 'NF', 'HHWEB'), episodeNumber: 2, sizeBytes: '2000' },
    { ...file('episode-1', 1, '1080p', 'SDR', 'AMZN', 'ADWeb'), episodeNumber: 1 },
  ]
  const result = resolveTelegramFileFilters(incoming, values({ resolution: '2160p' }), 'SHOW')
  assert.deepEqual(result.files.map(file => file.id), ['episode-10', 'episode-2'])
  assert.deepEqual(options(result, 'resolution'), [['all', 3], ['1080p', 1], ['2160p', 2]])
  assert.deepEqual(filesModule.filterTelegramFiles(result.files).map(file => file.id), ['episode-2', 'episode-10'])
  assert.deepEqual(filesModule.filterTelegramFiles(result.files, { sort: 'size' }).map(file => file.id), ['episode-10', 'episode-2'])
  assert.deepEqual(result.files.map(file => file.id), ['episode-10', 'episode-2'])
})

test('每一级呈现的选项均有匹配文件，逐级选到底不会产生空组合', () => {
  function visit(current, depth = 0) {
    const result = resolveTelegramFileFilters(files, current)
    assert.ok(result.files.length > 0)
    if (depth === result.facets.length) return
    const currentFacet = result.facets[depth]
    for (const option of currentFacet.options) {
      assert.ok(option.count > 0)
      visit({ ...result.values, [currentFacet.key]: option.value }, depth + 1)
    }
  }
  visit(values())
})
