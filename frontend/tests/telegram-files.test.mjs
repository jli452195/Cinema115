import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const module = { exports: {} }
const code = ts.transpileModule(readFileSync(new URL('../src/utils/telegramFiles.ts', import.meta.url), 'utf8'), { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
new Function('module', 'exports', code)(module, module.exports)
const { telegramFiles, matchTelegramFiles, filterTelegramFiles, groupTelegramFiles } = module.exports
const hashA = '0123456789abcdef0123456789abcdef'
const hashB = 'fedcba9876543210fedcba9876543210'
const link = (name, hash = hashA, size = '1000', metadata) => ({ type: 'ed2k', url: `ed2k://|file|${encodeURIComponent(name)}|${size}|${hash}|/`, metadata })
const resource = (id, links, extra = {}) => ({ id, title: '测试作品', text: '原帖广告、S01E99 和无关关键词', links, channel: { id: 'channel', title: '测试频道' }, sourceUrl: `https://t.me/channel/${id}`, postedAt: '2026-11-17T01:00:00Z', ...extra })

test('同哈希与精确大小合并来源，大小差异和不同版本保留独立文件', () => {
  const items = [resource('one', [link('Show.S01E01.1080p-HHWEB.mkv', hashA.toUpperCase(), '0009007199254740993')]), resource('two', [link('另一个文件名.mkv', hashA, '9007199254740993')]), resource('three', [link('Show.S01E01.1080p-HHWEB.mkv', hashA, '9007199254740992'), link('Show.S01E01.2160p-HHWEB.mkv', hashB)])]
  const files = telegramFiles(items)
  assert.equal(files.length, 3)
  assert.equal(files[0].id, `${hashA}:9007199254740993`)
  assert.equal(files[0].sizeBytes, '9007199254740993')
  assert.equal(files[0].sources.length, 2)
  assert.equal(telegramFiles([items[0], items[0]])[0].sources.length, 1)
})

test('外部页面只提供一次文件集合，聚合原帖来源并与直接文件合并', () => {
  const file = link('Show.S01E06.1080p-HHWEB.mkv')
  const origins = Array.from({ length: 20 }, (_, index) => ({ id: `post-${index}`, title: '测试作品', url: `https://t.me/channel/${index}`, postedAt: '2026-11-17T01:00:00Z', channelTitle: '测试频道' }))
  const page = resource('page/version', [file], { fileSources: [...origins, origins[0]] })
  const [result] = telegramFiles([page, resource('post-0', [file], { sourceUrl: origins[0].url })])
  assert.equal(result.sources.length, 20)
  assert.deepEqual(result.sources.map(source => source.id), origins.map(source => source.id))
  assert.equal(result.episodeNumber, 6)
  assert.equal(page.fileSources.length, 21, '来源合并不改写页面输入')
})

test('文件名只解码一次，保留字面加号，跳过无效 ED2K', () => {
  const raw = 'ed2k://|file|Show+A%2520B.S01E13.mkv|1000|' + hashA + '|/'
  const files = telegramFiles([resource('a', [{ type: 'ed2k', url: raw }, { type: 'ed2k', url: raw.replace(hashA, 'bad') }, { type: '115', url: 'https://115.com/s/share' }])])
  assert.equal(files.length, 1)
  assert.equal(files[0].name, 'Show+A%20B.S01E13.mkv')
  assert.equal(files[0].url, raw)
  assert.equal(files[0].season, 1)
  assert.equal(files[0].episodeNumber, 13)
})

test('重复来源补齐缺失规格，冲突声明不覆盖已知字段', () => {
  const [file] = telegramFiles([resource('a', [link('Show.S01E01.mkv', hashA, '1000', { resolution: '1080p' })]), resource('b', [link('Show.S01E01.mkv', hashA, '1000', { resolution: '2160p', platform: 'NF', audio: 'AAC 2.0' })])])
  assert.equal(file.metadata.resolution, '1080p')
  assert.equal(file.metadata.platform, 'NF')
  assert.equal(file.metadata.audio, 'AAC 2.0')
  assert.equal(file.sources.length, 2)
})

test('多文件原帖筛集只命中对应文件，不继承父级季集与总体积', () => {
  const files = telegramFiles([resource('a', [link('Show.S01E13.2160p.WEB-DL.mkv', hashA, '1000', { specs: ['2160p', 'HEVC'] }), link('Show.S01E14.2160p.WEB-DL.mkv', hashB, '2000')], { metadata: { episode: 'S01E99', size: '100 GiB', specs: ['1080p'] } })])
  assert.deepEqual(filterTelegramFiles(files, { query: 'E13 2160p' }).map(file => file.episodeNumber), [13])
  assert.equal(filterTelegramFiles(files, { query: '广告' }).length, 0)
  assert.equal(filterTelegramFiles(files, { query: 'S01E99' }).length, 0)
  assert.equal(files[1].metadata.size, undefined)
  assert.equal(files[1].metadata.resolution, undefined)
  assert.equal(files[0].metadata.resolution, '2160p')
})

test('单文件保留卡片元数据，链接字段覆盖父级字段且不修改输入', () => {
  const parent = { episode: 'S02E03', specs: ['1080p', 'HDR'], size: '1 GiB' }
  const original = resource('a', [link('Show.S02E03.mkv', hashA, '1000', { size: '1000 B' })], { metadata: parent })
  const [file] = telegramFiles([original])
  assert.equal(file.metadata.episode, 'S02E03')
  assert.equal(file.metadata.size, '1000 B')
  assert.equal(file.metadata.dynamicRange, 'HDR')
  file.metadata.specs.push('extra')
  assert.deepEqual(parent.specs, ['1080p', 'HDR'])
})

test('范围发布保留范围展示，不能把起点当单集，文件名中的年份也不当小数集号', () => {
  const files = telegramFiles([resource('range', [link('Show.S01E01-E02.1080p-NF.mkv', hashA, '1000', { episode: 'S01E01-E02' })]), resource('one', [link('Show.S01E01.2026.1080p-NF.mkv', hashB, '2000')])])
  assert.equal(files[0].metadata.episode, 'S01E01-E02')
  assert.equal(files[0].season, 1)
  assert.equal(files[0].episodeNumber, undefined)
  assert.equal(files[0].version.includes('E02'), false)
  assert.equal(files[1].episodeNumber, 1)
  assert.equal(files[1].version.includes('2026'), true)
})

test('自然季集排序、精确大小排序、来源最新时间排序均保持输入顺序不变', () => {
  const files = telegramFiles([resource('a', [link('Show.S01E10.mkv', hashA, '9007199254740992')]), resource('b', [link('Show.S00E02.mkv', hashB, '9007199254740993')], { postedAt: '2026-11-18T01:00:00Z' }), resource('c', [link('Movie.mkv', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', '1')])])
  assert.deepEqual(filterTelegramFiles(files).map(file => file.episodeNumber), [2, 10, undefined])
  assert.equal(filterTelegramFiles(files, { sort: 'size' })[0].sizeBytes, '9007199254740993')
  assert.equal(filterTelegramFiles(files, { sort: 'latest' })[0].sources[0].id, 'b')
  assert.equal(files[0].episodeNumber, 10)
  assert.equal(filterTelegramFiles(files, { season: 0 }).length, 1)
  assert.equal(filterTelegramFiles(files, { season: 'unknown' })[0].name, 'Movie.mkv')
})

test('条件匹配保留原始顺序，关键词和规格取交集，显示阶段仍按指定方式排序', () => {
  const files = telegramFiles([resource('a', [
    link('Show.S01E10.NF.mkv', hashA, '3000', { resolution: '2160p', platform: 'NF' }),
    link('Show.S01E02.NF.mkv', hashB, '1000', { resolution: '2160p', platform: 'NF' }),
    link('Show.S01E01.NF.mkv', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', '2000', { resolution: '1080p', platform: 'NF' }),
  ])])
  const filter = { query: 'show NF', season: 1, resolution: '2160p' }
  assert.deepEqual(matchTelegramFiles(files, filter).map(file => file.episodeNumber), [10, 2])
  assert.deepEqual(filterTelegramFiles(files, filter).map(file => file.episodeNumber), [2, 10])
  assert.deepEqual(filterTelegramFiles(files, { ...filter, sort: 'size' }).map(file => file.episodeNumber), [10, 2])
  const all = matchTelegramFiles(files, { query: '  ' })
  assert.notEqual(all, files)
  assert.deepEqual(all, files)
  assert.deepEqual(files.map(file => file.episodeNumber), [10, 2, 1])
})

test('相同版本跨集归组，平台、发布组、动态范围差异不合并', () => {
  const files = telegramFiles([resource('a', [link('Show.S01E01.1080p.NF.WEB-DL-HHWEB.mkv', hashA), link('Show.S01E02.1080p.NF.WEB-DL-HHWEB.mkv', hashB), link('Show.S01E01.1080p.NF.WEB-DL-ADWeb.mkv', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'), link('Show.S01E01.1080p.AMZN.WEB-DL-HHWEB.mkv', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb')])])
  const groups = groupTelegramFiles(files)
  assert.equal(groups.length, 3)
  assert.equal(groups[0].files.length, 2)
  assert.equal(filterTelegramFiles(files, { query: 'ADWeb' }).length, 1)
  const groupedPage = telegramFiles([resource('a', [link('Show.S01E01.mkv', hashA, '1', { resolution: '1080p', dynamicRange: 'HDR' }), link('Show.S01E02.mkv', hashB, '1', { resolution: '1080p', dynamicRange: 'SDR' })], { version: '页面明确版本标题' })])
  assert.equal(groupTelegramFiles(groupedPage).length, 2)
  assert.equal(filterTelegramFiles(groupedPage, { resolution: '1080p', dynamicRange: 'HDR' }).length, 1)
})
