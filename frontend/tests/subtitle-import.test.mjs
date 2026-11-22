import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import { File } from 'node:buffer'
import ts from 'typescript'
import { zipSync, strToU8 } from 'fflate'
import { subtitleLimits } from './helpers/subtitle-limits.mjs'

const require = createRequire(import.meta.url)
const source = await readFile(new URL('../src/utils/subtitleImport.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const namingSource = await readFile(new URL('../src/utils/subtitleNaming.ts', import.meta.url), 'utf8')
const namingCompiled = ts.transpileModule(namingSource, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS, esModuleInterop: true } }).outputText
const namingModule = { exports: {} }
const requireNaming = createRequire(new URL('../src/utils/subtitleNaming.ts', import.meta.url))
new Function('require', 'module', 'exports', namingCompiled)(requireNaming, namingModule, namingModule.exports)
const module = { exports: {} }
new Function('require', 'module', 'exports', compiled)(name => {
  if (name === './subtitleLimits') return subtitleLimits
  if (name === './subtitleNaming') return namingModule.exports
  if (name === './subtitleArchiveClient') return { readSubtitleArchive: (bytes, kind, limits, signal) => { assert.equal(kind, 'zip'); return module.exports.readSubtitleZip(bytes, signal, limits) } }
  return require(name)
}, module, module.exports)
const { episodeNumber, analyzeSubtitle, continuousSubtitleRanges, subtitlePairKey, readLocalSubtitles, mergeSubtitleFiles } = module.exports
const { subtitleLanguage } = namingModule.exports
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
  assert.equal(subtitleLanguage('show.中英双语.ass'), 'zh')
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
  for (const name of ['Show_03.ass', 'Show - 03.chs.ass', '[Group][03][1080p].ass', 'Show EP03.ass', 'Show 第03话.ass', 'Show 第 03 集.ass', 'Show # 03.ass']) assert.equal(episodeNumber(name).episode, 3)
})

test('confirmed target episodes match numeric subtitles independently of original video names', () => {
  const videos = [1, 2, 3].map(episode => ({
    ...video(String(episode), `[LP-Raws] Mayoi Neko Overrun! ${String(episode).padStart(2, '0')} (BDRip 1080p HEVC-YUV420P10 FLAC).mkv`),
    season: 1, episode,
  }))
  for (const target of videos) {
    assert.deepEqual(episodeNumber(target.name), { episode: target.episode })
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

test('space-separated release episodes accept complete technical suffixes without guessing metadata', () => {
  const names = [
    '[ReinForce] Shangri-La 01 (DVDRip 852x480 x264 FLAC).ass',
    '[LP-Raws] Mayoi Neko Overrun! 01 (BDRip 1080p HEVC-YUV420P10 FLAC).ass',
    'Shangri-La 01 [DVDRip][852x480][x264][FLAC].chs.ass',
    'Shangri-La 01 DVDRip 852x480 x264 FLAC.ass',
    '目录/[ReinForce] Shangri-La 01 (DVDRip 852x480 x264 FLAC).tc.ass',
    '香格里拉 01 (DVDRip 852×480 x264 FLAC).srt',
    '[Nekomoe kissaten] Kanojo mo Kanojo 01 [WebRip 1080p HEVC-10bit AAC].JPSC.ass',
    '[Nekomoe kissaten] Kanojo mo Kanojo 01 [WebRip 1080p HEVC-10bit AAC].JPTC.ass',
    'Shangri-La 01 [DVDRip][852x480][x264][FLAC].ScJp.ass',
  ]
  const videos = [video('first', '香格里拉.S02E01.mkv'), video('other-season', '香格里拉.S01E01.mkv')]
  for (const name of names) {
    assert.deepEqual(episodeNumber(name), { episode: 1 }, name)
    const match = analyzeSubtitle(name, videos, 2)
    assert.equal(match.videoId, 'first', name)
    assert.equal(match.confidence, 'high', name)
    assert.equal(analyzeSubtitle(name, videos, NaN).videoId, '', name)
    assert.equal(analyzeSubtitle(name, [...videos, video('version', '香格里拉.S02E01 - 1080p.mkv')], 2).confidence, 'medium', name)
  }
  assert.deepEqual(episodeNumber('Show 112 (BDRip 1080p x265 FLAC).ass'), { episode: 112 })
  assert.equal(analyzeSubtitle(names[0], [video('done', names[0].replace(/ass$/, 'mkv'))], 2).videoId, 'done')
})

test('space-separated episodes reject revisions, resolutions, specials and competing numeric evidence', () => {
  const suffix = '(DVDRip 852x480 x264 FLAC).ass'
  const invalid = [
    'Show 01.ass', 'Show 01.chs.ass', 'Show 01 (unknown release).ass',
    'Show 01 (BDRip 1080p x265 FLAC.ass', 'Show 01 BDRip 1080p x265 FLAC).ass',
    ...['00', '2024', '1080', '2160', '480', '576', '720', '852', '264', '265', '9999',
      '01v2', '01-02', '01~02', '01+02', '01.5', 'SP01', 'OVA01', '01 02',
      '2024 01', 'v2 01', 'revision 01', 'SP 01'].map(number => `Show ${number} ${suffix}`),
    'Show 01 (BDRip 1080p x265 FLAC v2).ass', 'Show 01 (BDRip 1080p x265 FLAC 02).ass',
    'Show 01 (BDRip 1080p x265 FLAC) (SP).ass',
    'Show 852x480 x264 FLAC.ass', '[Group] 01 (BDRip 1080p x265 FLAC).ass',
    'Show 01 [WebRip 1080p HEVC-10bit AAC].CUSTOM.ass', 'Show 01 [WebRip 1080p].JPTC.v2.ass',
    'Show 01 [unknown].JPTC.ass', 'Show 01 [WebRip 1080p].JPSC.mkv',
  ]
  const videos = [video('first', 'Show.S01E01.mkv'), video('second', 'Show.S01E02.mkv')]
  for (const name of invalid) {
    assert.equal(episodeNumber(name), undefined, name)
    assert.equal(analyzeSubtitle(name, videos, 1).videoId, '', name)
  }
})

test('explicit legacy episode evidence wins over numeric titles and years before space-separated fallback', () => {
  const names = [
    'Show (2020) - 03.ass', 'Show 2 [03].ass',
    'Show (2020) - 03 (DVDRip 852x480 x264 FLAC).ass',
    'Show 2 [03] (BDRip 1080p x265 FLAC).ass',
    'Show 2 EP03.ass', 'Show 2 #03.ass', 'Show 2 第 03 集.ass', 'Show 2020_03.ass',
  ]
  const videos = [video('third', 'Show.S01E03.mkv'), video('other', 'Show.S01E04.mkv')]
  for (const name of names) {
    assert.deepEqual(episodeNumber(name), { episode: 3 }, name)
    assert.equal(analyzeSubtitle(name, videos, 1).videoId, 'third', name)
  }
  // 无括号的年份加横线仍沿用原数字范围保护，不借本次增强放宽。
  for (const name of ['Show 2020 - 03.ass', 'Show 2020 - 03 (DVDRip 852x480 x264 FLAC).ass', 'Show 2 [03] [04].ass', 'Show 2020 - 03 [04].ass', 'Show 2 EP03 #04.ass', 'Show 2 03 (BDRip 1080p x265 FLAC).ass']) {
    assert.equal(episodeNumber(name), undefined, name)
    assert.equal(analyzeSubtitle(name, videos, 1).videoId, '', name)
  }
})

test('space-separated source episodes preserve authoritative targets and ambiguous offset interpretations', () => {
  const name = '[ReinForce] Shangri-La 27 (DVDRip 852x480 x264 FLAC).ass'
  const offset = { ...video('offset', name.replace(/ass$/, 'mkv')), season: 2, episode: 1 }
  const target = { ...video('target', 'Shangri-La 53 (BDRip 1080p x265 FLAC).mkv'), season: 2, episode: 27 }
  const options = { sourceEpisodes: true }
  assert.equal(analyzeSubtitle(name, [offset], 2).videoId, '')
  assert.equal(analyzeSubtitle(name, [offset], 2, 'tv', options).videoId, 'offset')
  assert.match(analyzeSubtitle(name, [offset], 2, 'tv', options).reason, /发布第 27 集.*S02E01/)
  assert.equal(analyzeSubtitle(name, [offset], 1, 'tv', options).videoId, '')
  assert.equal(analyzeSubtitle(name, [offset], NaN, 'tv', options).videoId, '')
  assert.equal(analyzeSubtitle('S02E27.ass', [offset], 2, 'tv', options).videoId, '')
  assert.equal(analyzeSubtitle('01.ass', [offset], 2, 'tv', options).videoId, 'offset')
  assert.equal(analyzeSubtitle(name, [offset, target], 2).videoId, 'target')
  const ambiguous = analyzeSubtitle(name, [offset, target], 2, 'tv', options)
  assert.equal(ambiguous.videoId, '')
  assert.equal(ambiguous.confidence, 'medium')
  assert.deepEqual(new Set(ambiguous.candidates.map(candidate => candidate.id)), new Set(['offset', 'target']))
  assert.equal(analyzeSubtitle('S02E27.ass', [offset, target], NaN, 'tv', options).videoId, 'target')
  for (const fields of [{ episode: 0 }, { episode: '' }, { season: '' }, { season: 3 }]) {
    assert.equal(analyzeSubtitle(name, [{ ...offset, ...fields }], 2, 'tv', options).videoId, '', JSON.stringify(fields))
  }
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

test('multi-season subtitles require a chosen scope unless their target season is explicit', () => {
  const videos = [1, 2].map(season => ({ ...video(String(season), `Release - 01.mkv`), season, episode: 1 }))
  const options = { sourceEpisodes: true }
  assert.equal(analyzeSubtitle('01.sc.ass', videos, NaN, 'tv', options).videoId, '')
  assert.equal(analyzeSubtitle('01.sc.ass', videos, 1, 'tv', options).videoId, '1')
  assert.equal(analyzeSubtitle('01.sc.ass', videos, 2, 'tv', options).videoId, '2')
  assert.equal(analyzeSubtitle('S02E01.sc.ass', videos, NaN, 'tv', options).videoId, '2')
  assert.equal(analyzeSubtitle('S01E01.sc.ass', videos, 2, 'tv', options).videoId, '1')
})

test('download subtitle matching can use source numbers after offsets without choosing ambiguous interpretations', () => {
  const first = { ...video('offset', '[Group] Show - 27 [1080p].mkv'), season: 2, episode: 1 }
  const options = { sourceEpisodes: true }
  assert.equal(analyzeSubtitle('27.sc.ass', [first], 2).videoId, '', 'other subtitle flows retain target-only matching')
  const match = analyzeSubtitle('27.sc.ass', [first], 2, 'tv', options)
  assert.equal(match.videoId, 'offset')
  assert.match(match.reason, /发布第 27 集.*S02E01/)
  assert.equal(analyzeSubtitle('01.sc.ass', [first], 2, 'tv', options).videoId, 'offset')
  assert.equal(analyzeSubtitle('S01E27.sc.ass', [first], 2, 'tv', options).videoId, '', 'explicit target seasons do not fall back to original release numbering')
  assert.equal(analyzeSubtitle('27.sc.ass', [first], 1, 'tv', options).videoId, '', 'original numbers stay inside the chosen target season')
  const second = { ...video('target', '[Group] Show - 53 [1080p].mkv'), season: 2, episode: 27 }
  const ambiguous = analyzeSubtitle('27.sc.ass', [first, second], 2, 'tv', options)
  assert.equal(ambiguous.videoId, '')
  assert.deepEqual(new Set(ambiguous.candidates.map(candidate => candidate.id)), new Set(['offset', 'target']))
  assert.equal(analyzeSubtitle('S02E27.sc.ass', [first, second], NaN, 'tv', options).videoId, 'target')
  assert.equal(analyzeSubtitle('27.sc.ass', [{ ...first, episode: 0 }], 2, 'tv', options).videoId, '', 'invalid target mapping never becomes eligible through its original name')
})

test('连续字幕编号按指定范围换算，只关联季内唯一视频', () => {
  const videos = Array.from({ length: 12 }, (_, i) => ({ ...video(String(i + 1), `Release - ${String(i + 1).padStart(2, '0')}.mkv`), season: 2, episode: i + 1 }))
  const options = { sourceEpisodes: true, continuousRanges: [{ season: 2, offset: 12, episodeCount: 12 }] }
  const subtitleName = number => `[Coalgirls]_Rinne_no_Lagrange_${String(number).padStart(2, '0')}_(1920x1080_Blu-Ray_FLAC)_[108C51C3].DHR.tc.ass`
  for (let i = 1; i <= 12; i++) {
    const matched = analyzeSubtitle(subtitleName(i + 12), videos, 2, 'tv', options)
    assert.equal(matched.videoId, String(i))
    const earlierSeason = analyzeSubtitle(subtitleName(i), videos, 2, 'tv', options)
    assert.equal(earlierSeason.videoId, '')
    assert.match(earlierSeason.reason, /不在第 2 季范围内/)
  }
  assert.match(analyzeSubtitle(subtitleName(13), videos, 2, 'tv', options).reason, /连续第 13 集.*S02E01/)
  assert.equal(analyzeSubtitle(subtitleName(25), videos, 2, 'tv', options).videoId, '')
  assert.equal(analyzeSubtitle(subtitleName(13), videos.slice(1), 2, 'tv', options).videoId, '')
  assert.equal(analyzeSubtitle(subtitleName(13), [...videos, { ...videos[0], id: 'second-version' }], 2, 'tv', options).confidence, 'medium')
  assert.equal(analyzeSubtitle('S02E01.tc.ass', videos, 2, 'tv', options).videoId, '1')
  assert.equal(analyzeSubtitle('S01E13.tc.ass', videos, 2, 'tv', options).videoId, '')
  assert.equal(analyzeSubtitle('Show - 13-14.tc.ass', videos, 2, 'tv', options).videoId, '')
  assert.equal(analyzeSubtitle('01.tc.ass', [video('exact', '01.tc.mkv')], 2, 'tv', options).videoId, '')
  const rawMatch = { ...video('raw', 'Release - 13.mkv'), season: 2, episode: 2 }
  assert.equal(analyzeSubtitle('13.tc.ass', [rawMatch], 2, 'tv', options).videoId, '')
})

test('连续字幕编号按完整正式季累计，未选季保留区间且忽略特别篇', () => {
  const seasons = [{ number: 3, episodeCount: 6 }, { number: 1, episodeCount: 12 }, { number: 0, episodeCount: 5 }, { number: 2, episodeCount: 12 }]
  const ranges = continuousSubtitleRanges(seasons, [3, 0, 1, 3])
  assert.deepEqual(ranges, [{ season: 1, offset: 0, episodeCount: 12 }, { season: 3, offset: 24, episodeCount: 6 }])
  const videos = [
    { ...video('s1-12', 'Release.S01E12.mkv'), season: 1, episode: 12 },
    { ...video('s3-01', 'Release.S03E01.mkv'), season: 3, episode: 1 },
    { ...video('special', 'Release.S00E01.mkv'), season: 0, episode: 1 },
  ]
  const options = { sourceEpisodes: true, continuousRanges: ranges }
  assert.equal(analyzeSubtitle('12.tc.ass', videos, NaN, 'tv', options).videoId, 's1-12')
  assert.equal(analyzeSubtitle('13.tc.ass', videos, NaN, 'tv', options).videoId, '')
  assert.equal(analyzeSubtitle('24.tc.ass', videos, NaN, 'tv', options).videoId, '')
  assert.equal(analyzeSubtitle('25.tc.ass', videos, NaN, 'tv', options).videoId, 's3-01')
  assert.equal(analyzeSubtitle('01.tc.ass', videos, NaN, 'tv', options).videoId, '')
  assert.equal(analyzeSubtitle('S00E01.tc.ass', videos, NaN, 'tv', options).videoId, 'special')
  assert.deepEqual(continuousSubtitleRanges(seasons.filter(item => item.number !== 2), [1, 3]), [])
  assert.deepEqual(continuousSubtitleRanges(seasons.map(item => item.number === 3 ? { ...item, episodeCount: 0 } : item), [3]), [])
  assert.deepEqual(continuousSubtitleRanges(seasons, [0]), [])
})

test('连续字幕 01–24 关联两季，缺集保留原编号且同季同集多个版本待确认', () => {
  const continuousRanges = continuousSubtitleRanges([{ number: 1, episodeCount: 12 }, { number: 2, episodeCount: 12 }], [1, 2])
  const options = { sourceEpisodes: true, continuousRanges }
  const videos = [1, 2].flatMap(season => Array.from({ length: 12 }, (_, i) => ({
    ...video(`s${season}-${i + 1}`, `Release.S0${season}E${String(i + 1).padStart(2, '0')}.mkv`),
    season, episode: i + 1,
  })))
  const name = number => `[Coalgirls]_Rinne_no_Lagrange_${String(number).padStart(2, '0')}_(1920x1080_Blu-Ray_FLAC)_[108C51C3].DHR.tc.ass`
  for (let number = 1; number <= 24; number++) {
    const expected = number <= 12 ? `s1-${number}` : `s2-${number - 12}`
    assert.equal(analyzeSubtitle(name(number), videos, NaN, 'tv', options).videoId, expected)
  }
  const remaining = videos.filter(item => item.id !== 's2-1' && item.id !== 's1-11')
  assert.equal(analyzeSubtitle(name(11), remaining, NaN, 'tv', options).videoId, '')
  assert.equal(analyzeSubtitle(name(13), remaining, NaN, 'tv', options).videoId, '')
  assert.equal(analyzeSubtitle(name(14), remaining, NaN, 'tv', options).videoId, 's2-2')
  assert.equal(analyzeSubtitle(name(25), videos, NaN, 'tv', options).videoId, '')
  const duplicate = { ...videos[12], id: 'alternate' }
  const match = analyzeSubtitle(name(13), [...videos, duplicate], NaN, 'tv', options)
  assert.equal(match.videoId, '')
  assert.deepEqual(new Set(match.candidates.map(candidate => candidate.id)), new Set(['s2-1', 'alternate']))
  assert.equal(analyzeSubtitle('S02E01.tc.ass', videos, NaN, 'tv', options).videoId, 's2-1')
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
  for (const [tag, language] of [['eng', 'en'], ['jpn', 'ja'], ['chs', 'zh-CN'], ['cht', 'zh-TW'], ['chs+eng', 'zh-CN']]) {
    assert.equal(subtitleLanguage(`Show AAC[2.0(eng)+5.1(jpn)] SUB[${tag}].ass`), language)
  }
  assert.equal(subtitleLanguage('Show.jpn.ass'), 'ja')
  assert.equal(subtitleLanguage('Show[eng].ass'), 'en')
  assert.equal(subtitleLanguage('Show_CHT.ass'), 'zh-TW')
  assert.equal(subtitleLanguage('Show_CHT_ENG.ass'), 'zh-TW')
  assert.equal(subtitleLanguage('Show_SUB[fra].zh-CN.ass'), '')
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
  await assert.rejects(readLocalSubtitles([new File([], 'empty.srt')]), /空字幕文件/)
  await assert.rejects(readLocalSubtitles(Array.from({ length: 1001 }, () => new File(['x'], 'a.srt'))), /1000/)
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
  const full = Array.from({ length: 1000 }, (_, index) => ({ ...old, name: `${index}.srt` }))
  assert.throws(() => mergeSubtitleFiles(full, [{ ...old, name: 'new.srt' }], true), /1000/)
  assert.equal(full.length, 1000)
})

test('long-series ZIPs import and match 112 episodes, 224 language variants and 1000 subtitles', async t => {
  for (const count of [112, 224, 1000]) {
    const episodes = count === 224 ? 112 : count
    const videos = Array.from({ length: episodes }, (_, index) => ({
      ...video(String(index + 1), `Yu.Yu.Hakusho.S01E${String(index + 1).padStart(3, '0')}.mkv`),
      season: 1, episode: index + 1,
    }))
    const entries = Object.fromEntries(Array.from({ length: count }, (_, index) => {
      const episode = index % episodes + 1
      const language = index >= episodes ? 'cht' : 'chs'
      // 分辨率同形数字沿用现有保护，使用明确季集标记避免歧义。
      const tag = [480, 576, 720].includes(episode) ? 'S01E' : 'E'
      return [`Yu.Yu.Hakusho.${tag}${String(episode).padStart(3, '0')}.${language}.srt`, strToU8(`subtitle ${episode} ${language}`)]
    }))
    const start = performance.now()
    const result = await readLocalSubtitles([new File([zipSync(entries)], '幽游白书.zip')])
    const imported = performance.now()
    assert.equal(result.files.length, count)
    for (let index = 0; index < result.files.length; index++) {
      const file = result.files[index]
      const match = analyzeSubtitle(file.name, videos, 1, 'tv', { sourceEpisodes: true })
      assert.equal(match.videoId, String(index % episodes + 1))
      assert.equal(match.confidence, 'high')
      assert.equal(subtitleLanguage(file.name), index >= episodes ? 'zh-TW' : 'zh-CN')
    }
    t.diagnostic(`${count} subtitles: ZIP/import ${(imported - start).toFixed(1)} ms, matching ${(performance.now() - imported).toFixed(1)} ms`)
  }
})

test('ZIP count and remaining byte budgets report the actual exceeded constraint', async () => {
  const entries = Object.fromEntries(Array.from({ length: 1001 }, (_, index) => [`${index + 1}.srt`, strToU8('text')]))
  await assert.rejects(readLocalSubtitles([new File([zipSync(entries)], 'too-many.zip')]), /数量.*1000/)
  const zip = zipSync({ '01.srt': strToU8('text') })
  await assert.rejects(module.exports.readSubtitleZip(zip, undefined, { files: 1000, bytes: 3 }), /总大小.*64 MiB/)
  const empty = zipSync({ 'empty.srt': new Uint8Array() })
  await assert.rejects(readLocalSubtitles([new File([empty], 'empty.zip')]), /empty.srt.*空字幕文件/)
})

test('size limits stay bounded and are checked before reading large local files', async () => {
  const unreadable = size => ({ name: 'large.srt', size, arrayBuffer() { throw new Error('不应读取超限文件') } })
  await assert.rejects(readLocalSubtitles([unreadable(subtitleLimits.maxFileBytes + 1)]), /单个字幕.*16 MiB/)
  await assert.rejects(readLocalSubtitles([unreadable(subtitleLimits.maxTotalBytes + 1)]), /总大小.*64 MiB/)
  assert.equal(subtitleLimits.subtitleSelectionIssue(1000, subtitleLimits.maxTotalBytes), '')
  assert.match(subtitleLimits.subtitleSelectionIssue(1001, 1), /数量.*1000/)
  assert.match(subtitleLimits.subtitleSelectionIssue(1, subtitleLimits.maxTotalBytes + 1), /总大小.*64 MiB/)
  const data = new Uint8Array(subtitleLimits.maxFileBytes)
  const current = Array.from({ length: 4 }, (_, index) => ({ name: `${index}.srt`, data, sha256: String(index) }))
  assert.equal(mergeSubtitleFiles([], current, true).added.length, 4)
  assert.throws(() => mergeSubtitleFiles(current, [{ name: 'over.srt', data: new Uint8Array([1]), sha256: 'extra' }], true), /总大小.*64 MiB/)
  assert.equal(current.length, 4)
})

test('cancelling during the final ZIP time slice never returns imported files', async t => {
  let time = 0
  t.mock.method(performance, 'now', () => time += 20)
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 0)
  try {
    await assert.rejects(module.exports.readSubtitleZip(zipSync({ '01.srt': strToU8('text') }, { level: 0 }), controller.signal), { name: 'AbortError' })
  } finally { clearTimeout(timer) }
})
