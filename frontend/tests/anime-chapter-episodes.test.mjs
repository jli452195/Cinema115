import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'

const source = await readFile(new URL('../src/utils/animeChapterEpisodes.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
const { chapterEpisodeReferences: match, seasonEpisodeLabel } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
const chapter = (number = 59, patch = {}) => ({ id: String(number), number, chapterNumber: 1, title: '混战都市', originalTitle: '混戦都市', date: '2025-02-05', ...patch })
const episode = (number = 59, patch = {}) => ({ id: '5563270', number, name: '混战都市', date: '2025-02-05', sourceSeason: 1, sourceNumber: 59, ...patch })
const target = (episodes = [episode()], season = 1) => ({ tmdbId: '65942', groupId: '', season, seasons: [{ number: season, episodes }] })

test('同一篇章按当前 TMDB 编排展示，分组保持真实剧集 ID', () => {
  const bgm = [chapter()]
  const normal = match(bgm, target())[0]
  assert.equal(seasonEpisodeLabel(normal.season, normal.episode.number), 'S01E59')
  const grouped = match(bgm, target([episode(9)], 3))[0]
  assert.equal(seasonEpisodeLabel(grouped.season, grouped.episode.number), 'S03E09')
  assert.equal(grouped.episode.id, normal.episode.id)
  assert.equal(bgm[0].number, 59)
  assert.equal(bgm[0].chapterNumber, 1)
})
test('译名不同可用唯一首播日期对应，缺日期可用唯一原标题', () => {
  assert.equal(match([chapter()], target([episode(59, { name: '另一译名' })]))[0].evidence, 'date')
  assert.equal(match([chapter(59, { date: '' })], target([episode(59, { name: '混戦都市' })]))[0].evidence, 'title')
})
test('编号相等及通用第几集标题均不足以建立对应', () => {
  const bgm = [chapter(59, { title: '第59话', originalTitle: 'Episode 59', date: '' })]
  assert.equal(match(bgm, target([episode(59, { name: 'Episode 59', date: '' })]))[0].episode, undefined)
})
test('标题与日期相互矛盾时保留待核对', () => {
  const rows = match([chapter()], target([episode(59, { date: '2025-02-12' }), episode(60, { id: '2', name: '其他标题' })]))
  assert.equal(rows[0].episode, undefined)
  assert.equal(match([chapter()], target([episode(59, { date: '2025-02-12' })]))[0].episode, undefined)
})
test('同日多集需要标题消歧，无证据不能选第一个', () => {
  const episodes = [episode(), episode(60, { id: '2', name: '另一集' })]
  assert.equal(match([chapter()], target(episodes))[0].episode.number, 59)
  assert.equal(match([chapter(59, { title: '其他译名', originalTitle: '' })], target(episodes))[0].episode, undefined)
})
test('多条 Bangumi 记录撞到同一 TMDB 集时均待核对', () => {
  assert.ok(match([chapter(), chapter(60)], target()).every(row => !row.episode))
})
test('只使用当前季，原名模式与缺失季资料保留 Bangumi 编号', () => {
  const t = { ...target(), season: 2 }
  assert.equal(match([chapter()], t)[0].episode, undefined)
  assert.equal(match([chapter()])[0].episode, undefined)
  assert.equal(match([chapter()], target(null))[0].episode, undefined)
})
test('空日期、非法日期和纯数字标题不作身份依据', () => {
  for (const date of ['', '0000-00-00', '2025-02-30']) {
    const rows = match([chapter(59, { title: '59', originalTitle: '', date })], target([episode(59, { name: '59', date })]))
    assert.equal(rows[0].episode, undefined)
  }
})
