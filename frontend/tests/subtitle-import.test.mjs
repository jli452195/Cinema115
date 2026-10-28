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
new Function('require', 'module', 'exports', compiled)(name => name === './subtitleArchiveClient' ? { readSubtitleArchive: (bytes, kind, limits, signal) => { assert.equal(kind, 'zip'); return module.exports.readSubtitleZip(bytes, signal, limits) } } : require(name), module, module.exports)
const { episodeNumber, analyzeSubtitle, subtitlePairKey, subtitleLanguage, readLocalSubtitles, mergeSubtitleFiles } = module.exports
const video = (id, name) => ({ id, name, parentId: '10', path: `/剧集/${name}`, size: 100, directory: false })

test('movie matching ignores episode-like numbers and leaves multiple versions for confirmation', () => {
  const movie = { ...video('movie', '1917.2019.1080p.mkv'), season: 0, episode: 0 }
  for (const name of ['1917.2019.chs.srt', 'Movie_03.chs.ass', 'Film #01.ass', 'Film - 01-02.ass', 'S02E03.ass']) {
    const result = analyzeSubtitle(name, [movie], NaN, 'movie')
    assert.equal(result.videoId, 'movie', name)
    assert.equal(result.confidence, 'low', name)
    assert.doesNotMatch(result.reason, /S\d+E\d+/, name)
  }
  const versions = [movie, { ...movie, id: '4k', name: '1917.2019.2160p.mkv' }]
  for (const name of ['1917.2019.1080p.srt', 'Movie_03.chs.ass']) {
    const result = analyzeSubtitle(name, versions, 1, 'movie')
    assert.equal(result.videoId, '', 'even a matching filename must not silently choose a movie version')
    assert.equal(result.confidence, 'medium')
    assert.equal(result.candidates.length, 2)
  }
  assert.equal(analyzeSubtitle('movie.srt', [], 1, 'movie').confidence, 'none')
})

test('season/episode matching stays ambiguous across versions, supports Chinese and numeric names', () => {
  const videos = [video('1', '剧集 - S01E03.mkv'), video('2', '剧集 - S02E03.mkv')]
  assert.equal(analyzeSubtitle('第03集.ass', videos, 2).videoId, '2')
  assert.equal(analyzeSubtitle('03.chs.srt', videos, 1).videoId, '1')
  assert.equal(analyzeSubtitle('[Group] Show [03].ass', videos, 1).videoId, '1')
  assert.equal(analyzeSubtitle('S02E03.chs.ass', videos, 1).videoId, '2')
  assert.equal(analyzeSubtitle('E03.ass', [...videos, video('3', '剧集 - S01E03 - 4K.mkv')], 1).videoId, '')
  assert.equal(analyzeSubtitle('不明字幕.ass', videos.slice(0, 1), 1).videoId, '')
  assert.equal(analyzeSubtitle('不明字幕.ass', [video('movie', '电影 (2024).mkv')], 1).confidence, 'low')
  assert.equal(episodeNumber('剧集 - S01E01-E02.ass'), undefined)
  assert.equal(analyzeSubtitle('不明字幕.ass', [video('combined', '剧集 - S01E01-E02.mkv')], 1).videoId, '')
  assert.equal(subtitleLanguage('show.chs.ass'), 'zh-CN')
  assert.equal(subtitleLanguage('show.中英双语.ass'), 'zh-en')
  assert.equal(subtitleLanguage('unknown.ass'), '')
  for (const tag of ['scjp', 'GB', '简日双语']) assert.equal(subtitleLanguage(`Show.${tag}.ass`), 'zh-CN')
  for (const tag of ['tcjp', 'BIG5', '繁日雙語']) assert.equal(subtitleLanguage(`Show.${tag}.ass`), 'zh-TW')
  assert.equal(subtitleLanguage('Show.双语.ass'), '')
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

test('confirmed target episodes match numeric subtitles independently of original video names', () => {
  const videos = [1, 2, 3].map(episode => ({
    ...video(String(episode), `[LP-Raws] Mayoi Neko Overrun! ${String(episode).padStart(2, '0')} (BDRip 1080p HEVC-YUV420P10 FLAC).mkv`),
    season: 1, episode,
  }))
  for (const target of videos) {
    assert.equal(episodeNumber(target.name), undefined)
    for (const [tag, language] of [['sc', 'zh-CN'], ['tc', 'zh-TW']]) {
      const name = `${String(target.episode).padStart(2, '0')}.${tag}.ass`
      const match = analyzeSubtitle(name, videos, 1)
      assert.equal(match.videoId, target.id)
      assert.equal(match.confidence, 'high')
      assert.equal(subtitleLanguage(name), language)
    }
  }
  assert.equal(analyzeSubtitle('04.sc.ass', videos, 1).videoId, '')
  assert.equal(analyzeSubtitle('01.sc.ass', videos.slice(1), 1).videoId, '', 'unselected videos cannot be matched')
  for (const name of ['SP01.ass', '01-02.ass', 'OVA01.ass', '01.5.ass']) assert.equal(analyzeSubtitle(name, videos, 1).videoId, '', name)
})

test('edited numbers take priority, invalid targets do not fall back, and duplicate targets require confirmation', () => {
  const changed = { ...video('edited', 'Show.S01E14.mkv'), season: 2, episode: 1 }
  assert.equal(analyzeSubtitle('01.sc.ass', [changed], 2).videoId, 'edited')
  assert.equal(analyzeSubtitle('S02E01.tc.ass', [changed], 1).videoId, 'edited')
  assert.equal(analyzeSubtitle('01.sc.ass', [changed], 1).videoId, '', 'use current target season, not first video season')
  assert.equal(analyzeSubtitle('Show.S01E14.ass', [changed], 1).videoId, '', 'same original stem cannot bypass edited target')
  for (const fields of [{ season: 2, episode: '' }, { season: '', episode: 14 }, { season: 1, episode: 0 }, { season: 1, episode: 1.5 }, { season: 1, episode: 10001 }, { season: 1001, episode: 14 }]) {
    assert.equal(analyzeSubtitle('Show.S01E14.ass', [{ ...changed, ...fields }], 1).videoId, '', JSON.stringify(fields))
  }
  const other = { ...video('other', 'Different release.mkv'), season: 2, episode: 1 }
  const ambiguous = analyzeSubtitle('01.sc.ass', [changed, other], 2)
  assert.equal(ambiguous.videoId, '')
  assert.equal(ambiguous.confidence, 'medium')
  assert.equal(ambiguous.candidates.length, 2)
  assert.equal(analyzeSubtitle('Show.S01E14.ass', [video('done', 'Show.S01E14.mkv')], 1).videoId, 'done', 'completed-task filenames retain existing matching')
})

test('specials, ranges, years, checksums and encoding numbers are not guessed', () => {
  for (const name of ['Show - 1080p.ass', 'Show_2024.ass', 'Show [720].ass', 'Show [A0123456].ass', 'Show - 01-02.ass', 'Show S01E01-E02.ass', 'Show SP01.ass', 'Show [OVA][01].ass', 'Show NCOP01.ass', 'Show [01][02].ass', 'Show - 01.5.ass']) {
    assert.equal(episodeNumber(name), undefined, name)
  }
})

test('hash episode subtitles match underscore releases without treating audio channels as episodes', () => {
  const videos = [1, 2, 3].map(i => video(String(i), `[Cleo]Trinity_Blood_-_0${i}_(Dual Audio_10bit_BD1080p_x265).mkv`))
  for (const codec of ['AAC', 'FLAC', 'DTS-HD', 'Audio']) {
    const name = `[アニメ] TrinityBlood #01 FLIGHT NIGHT (BD 1080p H.264 ${codec}[2.0(jpn)+5.1(eng)] SUB[eng]).ass`
    assert.deepEqual(episodeNumber(name), { episode: 1 }, name)
    assert.equal(subtitleLanguage(name), 'en', name)
    assert.equal(analyzeSubtitle(name, videos, 1).videoId, '1', name)
  }
  assert.equal(analyzeSubtitle('TrinityBlood #03 SUB[jpn].ass', videos, 1).videoId, '3')
  assert.equal(subtitleLanguage('TrinityBlood #03 AAC[5.1(eng)] SUB[jpn].ass'), 'ja')
  assert.equal(analyzeSubtitle('TrinityBlood #01 SUB[eng].ass', [...videos, video('other', 'TrinityBlood.S01E01.mkv')], 1).videoId, '')
  for (const name of ['Show #01-02.ass', 'Show #01-#02.ass', 'Show #01.5.ass', 'Show #01 #02.ass', 'Show_01-02.ass', 'Show #01 [02].ass', 'Show #01 [SP].ass']) {
    assert.equal(episodeNumber(name), undefined, name)
    assert.equal(analyzeSubtitle(name, videos, 1).videoId, '', name)
  }
})

test('subtitle language uses explicit subtitle tags and never the labeled audio track languages', () => {
  for (const name of ['Show AAC[eng].ass', 'Show FLAC[jpn].ass', 'Show Audio[eng jpn].ass', 'Show AAC[eng] SUB[fra].ass', 'Show SUB[eng] SUB[jpn].ass', 'Show SUB[jpn+eng].ass']) {
    assert.equal(subtitleLanguage(name), '', name)
  }
  for (const [tag, language] of [['eng', 'en'], ['jpn', 'ja'], ['chs', 'zh-CN'], ['cht', 'zh-TW'], ['chs+eng', 'zh-en']]) {
    assert.equal(subtitleLanguage(`Show AAC[2.0(eng)+5.1(jpn)] SUB[${tag}].ass`), language)
  }
  assert.equal(subtitleLanguage('Show.jpn.ass'), 'ja')
  assert.equal(subtitleLanguage('Show[eng].ass'), 'en')
  assert.equal(subtitleLanguage('Show AAC[eng].chs.ass'), 'zh-CN')
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

test('append preserves edited row identity, deduplicates within sources and checks combined budget atomically', () => {
  const old = { name: '01.srt', data: new Uint8Array([1]), sha256: 'a', videoId: 'manual', language: 'en', manual: true }
  const result = mergeSubtitleFiles([old], [old, { ...old, name: '02.srt' }], true)
  assert.equal(result.kept[0], old)
  assert.equal(result.kept[0].videoId, 'manual')
  assert.equal(result.duplicates, 1)
  assert.equal(result.added.length, 1)
  assert.equal(mergeSubtitleFiles([old], [{ ...old, sourceKey: 'other-directory' }], true).added.length, 1)
  assert.equal(mergeSubtitleFiles([old], [old], false).kept.length, 0)
  const full = Array.from({ length: 100 }, (_, index) => ({ ...old, name: `${index}.srt` }))
  assert.throws(() => mergeSubtitleFiles(full, [{ ...old, name: 'new.srt' }], true), /100/)
  assert.equal(full.length, 100)
})
