import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import { File } from 'node:buffer'
import ts from 'typescript'
import { zipSync, strToU8 } from 'fflate'

const require = createRequire(import.meta.url)
const source = await readFile(new URL('../src/utils/subtitleImport.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const module = { exports: {} }
new Function('require', 'module', 'exports', compiled)(require, module, module.exports)
const { episodeNumber, matchSubtitle, analyzeSubtitle, subtitlePairKey, subtitleLanguage, readLocalSubtitles } = module.exports
const video = (id, name) => ({ id, name, parentId: '10', path: `/剧集/${name}`, size: 100, directory: false })

test('season/episode matching stays ambiguous across versions, supports Chinese and numeric names', () => {
  const videos = [video('1', '剧集 - S01E03.mkv'), video('2', '剧集 - S02E03.mkv')]
  assert.equal(matchSubtitle('第03集.ass', videos, 2), '2')
  assert.equal(matchSubtitle('03.chs.srt', videos, 1), '1')
  assert.equal(matchSubtitle('[Group] Show [03].ass', videos, 1), '1')
  assert.equal(matchSubtitle('S02E03.chs.ass', videos, 1), '2')
  assert.equal(matchSubtitle('E03.ass', [...videos, video('3', '剧集 - S01E03 - 4K.mkv')], 1), '')
  assert.equal(matchSubtitle('不明字幕.ass', videos.slice(0, 1), 1), '')
  assert.equal(analyzeSubtitle('不明字幕.ass', [video('movie', '电影 (2024).mkv')], 1).confidence, 'low')
  assert.equal(episodeNumber('剧集 - S01E01-E02.ass'), undefined)
  assert.equal(matchSubtitle('不明字幕.ass', [video('combined', '剧集 - S01E01-E02.mkv')], 1), '')
  assert.equal(subtitleLanguage('show.chs.ass'), 'zh-CN')
  assert.equal(subtitleLanguage('show.中英双语.ass'), 'zh-en')
  assert.equal(subtitleLanguage('unknown.ass'), '')
})

test('49 anime subtitles with hyphen episode numbers match unique organized videos', () => {
  const videos = Array.from({ length: 49 }, (_, i) => video(String(i + 1), `Star☆Twinkle 光之美少女 - S01E${String(i + 1).padStart(2, '0')} - 1080p.mkv`))
  for (let i = 1; i <= 49; i++) {
    const result = analyzeSubtitle(`[Gelatin] Star☆Twinkle Precure - ${String(i).padStart(2, '0')} [1080p][FLsnow].chs.ass`, videos, 1)
    assert.equal(result.videoId, String(i))
    assert.equal(result.confidence, 'high')
    assert.match(result.reason, /唯一对应/)
  }
  for (const name of ['Show_03.ass', 'Show - 03.chs.ass', '[Group][03][1080p].ass', 'Show EP03.ass', 'Show 第03话.ass']) assert.equal(episodeNumber(name).episode, 3)
})

test('specials, ranges, years, checksums and encoding numbers are not guessed', () => {
  for (const name of ['Show - 1080p.ass', 'Show_2024.ass', 'Show [720].ass', 'Show [A0123456].ass', 'Show - 01-02.ass', 'Show S01E01-E02.ass', 'Show SP01.ass', 'Show [OVA][01].ass', 'Show NCOP01.ass', 'Show [01][02].ass', 'Show - 01.5.ass']) {
    assert.equal(episodeNumber(name), undefined, name)
  }
})

test('multiple versions are ranked but never selected automatically; missing season remains unmatched', () => {
  const videos = [video('720', 'Precure - S01E01 - 720p.mkv'), video('1080', 'Precure - S01E01 - 1080p.mkv')]
  const match = analyzeSubtitle('[Group] Precure - 01 [1080p].ass', videos, 1)
  assert.equal(match.videoId, '')
  assert.equal(match.confidence, 'medium')
  assert.equal(match.candidates[0].id, '1080')
  assert.equal(analyzeSubtitle('S02E01.ass', videos, 1).confidence, 'none')
  assert.equal(analyzeSubtitle('Precure.ass', videos, 1).videoId, '')
  assert.equal(subtitlePairKey('folder/01.IDX'), subtitlePairKey('folder/01.sub'))
  assert.notEqual(subtitlePairKey('a/01.idx'), subtitlePairKey('b/01.sub'))
})

test('ZIP extracts only subtitle bytes, preserves nested names and skips fonts', async () => {
  const content = strToU8('1\n00:00:00,000 --> 00:00:01,000\n字幕')
  for (const level of [0, 6]) {
    const bytes = zipSync({ '嵌套/第03集.chs.srt': content, 'font.ttf': strToU8('font'), 'readme.txt': strToU8('readme') }, { level })
    const result = await readLocalSubtitles([new File([bytes], 'subtitles.zip')])
    assert.equal(result.ignored, 2)
    assert.equal(result.files.length, 1)
    assert.equal(result.files[0].name, '嵌套/第03集.chs.srt')
    assert.deepEqual(result.files[0].data, content)
    assert.match(result.files[0].sha256, /^[a-f0-9]{64}$/)
  }
})

test('corrupted, encrypted, oversized and traversing ZIPs fail before import', async () => {
  const original = zipSync({ 'a.srt': strToU8('subtitle') }, { level: 0 })
  const corrupt = original.slice(); corrupt[35] ^= 1
  await assert.rejects(readLocalSubtitles([new File([corrupt], 'bad.zip')]), /校验失败/)
  const encrypted = original.slice()
  const central = new DataView(encrypted.buffer).getUint32(encrypted.length - 6, true)
  encrypted[central + 8] |= 1
  await assert.rejects(readLocalSubtitles([new File([encrypted], 'bad.zip')]), /加密/)
  const oversized = original.slice(); new DataView(oversized.buffer).setUint32(central + 24, 17 * 1024 * 1024, true)
  await assert.rejects(readLocalSubtitles([new File([oversized], 'bad.zip')]), /16 MiB/)
  const forged = original.slice(); new DataView(forged.buffer).setUint32(central + 24, 1, true)
  await assert.rejects(readLocalSubtitles([new File([forged], 'bad.zip')]), /大小/)
  const traversal = zipSync({ '../a.srt': strToU8('subtitle') })
  const absolute = zipSync({ 'C:/a.srt': strToU8('subtitle') })
  await assert.rejects(readLocalSubtitles([new File([absolute], 'bad.zip')]), /路径/)
  await assert.rejects(readLocalSubtitles([new File([traversal], 'bad.zip')]), /路径/)
  await assert.rejects(readLocalSubtitles([new File([original.slice(0, -3)], 'bad.zip')]), /不完整/)
  for (const name of ['subtitles.part1.rar', 'subtitles.7z.001', 'subtitles.r00', 'subtitles.z01']) {
    await assert.rejects(readLocalSubtitles([new File(['archive'], name)]), /分卷/)
  }
})

test('raw subtitles, count limits, empty files and cancellation', async () => {
  const result = await readLocalSubtitles([new File(['text'], 'a.ass'), new File(['font'], 'a.ttf')])
  assert.equal(result.files.length, 1)
  assert.equal(result.ignored, 1)
  await assert.rejects(readLocalSubtitles([new File([], 'empty.srt')]), /空文件/)
  await assert.rejects(readLocalSubtitles(Array.from({ length: 101 }, () => new File(['x'], 'a.srt'))), /100/)
  const controller = new AbortController(); controller.abort()
  await assert.rejects(readLocalSubtitles([new File(['text'], 'a.ass')], controller.signal))
})
