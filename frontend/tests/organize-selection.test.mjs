import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'

const source = await readFile(new URL('../src/utils/organizeSelection.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
const { directoryFileRows, directoryGroups, mobileDirectoryNames, mobileFileNames, rowExcluded, setRangeSelection } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
const row = (id, sourcePath) => ({ id, sourcePath, name: sourcePath.split('/').pop(), selected: false })

test('groups preserve the directory tree and render naturally sorted subfolders before local files', () => {
  const rows = [row('4', '/show/extras/deep/10.mkv'), row('3', '/show/extras/deep/2.mkv'), row('2', '/show/10.mkv'), row('1', '/show/2.mkv'), row('5', '/show/extras2/1.mkv')]
  const groups = directoryGroups(rows, '/show')
  assert.deepEqual(groups.map(group => group.path), ['/show', '/show/extras', '/show/extras/deep', '/show/extras2'])
  assert.deepEqual(groups.flatMap(group => group.rows).map(row => row.id), ['1', '2', '3', '4', '5'])
  assert.deepEqual(directoryFileRows(groups[0]).map(row => row.id), ['3', '4', '5', '1', '2'])
  assert.deepEqual(groups.map(group => [group.name, group.depth]), [['当前目录', 0], ['extras', 1], ['deep', 2], ['extras2', 1]])
  assert.equal(groups[1].rows.length, 0)
  assert.equal(groups[1].descendants.length, 2)
  assert.equal(rowExcluded(rows[0], ['/show/extras']), true)
  assert.equal(rowExcluded(rows[4], ['/show/extras']), false)
  assert.deepEqual(directoryGroups([row('1', '/a.mkv'), row('2', '/folder/b.mkv')], '/').map(group => group.path), ['/', '/folder'])
})

test('range selection affects only displayed entries and supports reverse deselection and missing anchor', () => {
  const rows = ['1', '2', '3', '4'].map(id => row(id, `/show/${id}.mkv`))
  setRangeSelection([rows[0], rows[2], rows[3]], '1', '4', true)
  assert.deepEqual(rows.map(row => row.selected), [true, false, true, true])
  setRangeSelection(rows, '4', '3', false)
  assert.deepEqual(rows.map(row => row.selected), [true, false, false, false])
  setRangeSelection(rows, 'missing', '2', true)
  assert.deepEqual(rows.map(row => row.selected), [true, true, false, false])
})

test('mobile names expose the complete differing markers and keep shared episode numbers', () => {
  for (const tails of [
    ['EP11', 'EP12'], ['S02E01', 'S02E02'], ['S02-01', 'S02-02'],
    ['[01-02]', '[01-03]'], ['E01-E02', 'E01-E03'], ['[01-02] [720p]', '[01-02] [1080p]'],
    ['NCOP', 'NCED', 'OVA_1', 'SP2', '02'], ['第01话', '第02话'],
  ]) {
    const rows = tails.map((tail, i) => row(String(i), `/show/[字幕组] 很长的剧名 - ${tail}.mkv`))
    const groups = directoryGroups(rows, '/')
    const names = mobileFileNames(groups)
    rows.forEach((file, i) => assert.deepEqual(names.get(file.id), {
      head: tails[i], tail: '', extension: '.mkv', prefixOmitted: true, tailStartsWithMarker: false,
    }))
    rows[0].selected = true
    rows[0].episode = 90
    assert.deepEqual(mobileFileNames(groups), names)
  }
})

test('mobile name prefixes stay local to each directory and skip virtual or title-only rows', () => {
  const rows = [row('a', '/first/[One] Long series - 01.mkv'), row('b', '/first/[One] Long series - 02.mkv'),
    row('c', '/second/[Other] Long series - SP.mkv'), row('d', '/second/[Other] Long series - NCOP.mkv'),
    { ...row('e', '/first/另一个标题.1080p'), titleOnly: true }]
  const groups = directoryGroups(rows, '/')
  const names = mobileFileNames(groups)
  assert.deepEqual(['a', 'b', 'c', 'd'].map(id => names.get(id).head), ['01', '02', 'SP', 'NCOP'])
  assert.equal(names.get('e').prefixOmitted, false)
  assert.equal(names.get('e').extension, '')
  assert.ok([...mobileFileNames(groups, true).values()].every(name => !name.prefixOmitted))
})

test('mobile middle truncation retains source text, real suffixes and whole graphemes', () => {
  const rows = [row('a', '/different/第一部很长的中文原片 第05话 1080p.MKV'), row('b', '/different/Other Show [NCED] [BD].mp4'),
    row('c', '/single/👩‍💻👨‍👩‍👧‍👦一.m4v'), row('d', '/duplicates/An unusually long common label.mkv'),
    row('e', '/duplicates/An unusually long common label.mkv'), row('f', '/prefix/A long shared label.mkv'),
    row('g', '/prefix/A long shared label second.mkv'), row('h', '/different/EP42 Long Release.mkv'),
    row('i', '/different/AsterixAdventureInternationalCutSoundtrackDirectorEditionRevisedMaster001.strm')]
  const names = mobileFileNames(directoryGroups(rows, '/'))
  for (const file of rows) {
    const { head, tail, extension, prefixOmitted } = names.get(file.id)
    assert.equal(head + tail + extension, file.name)
    assert.equal(prefixOmitted, false)
  }
  assert.equal(names.get('a').tail, '第05话 1080p')
  assert.equal(names.get('a').tailStartsWithMarker, true)
  assert.equal(names.get('a').extension, '.MKV')
  assert.equal(names.get('b').tail, '[NCED] [BD]')
  assert.equal(names.get('b').tailStartsWithMarker, true)
  assert.equal(names.get('c').head, '👩‍💻')
  assert.equal(names.get('c').tail, '👨‍👩‍👧‍👦一')
  assert.equal(names.get('h').head, '')
  assert.equal(names.get('h').tail, 'EP42 Long Release')
  assert.equal(names.get('h').tailStartsWithMarker, true)
  assert.equal(names.get('i').tailStartsWithMarker, false)
  assert.ok(names.get('i').tail.endsWith('Master001'))
  assert.equal(names.get('i').extension, '.strm')
})

test('mobile directory names distinguish siblings while retaining their full paths', () => {
  const prefix = '[Snow-Raws] 輪廻のラグランジェ Flower of Rin-ne [BD 1080p] '
  const paths = ['Season 1', 'Season 2', 'Specials'].map(name => `/show/${prefix}${name}`)
  const volumes = ['Part 01', 'Part 02'].map(name => `/by-volume/${prefix}Season 1 [BluRay Complete Audio] ${name}`)
  const similar = ['/similar/相同的长标题 - 法语版', '/similar/相同的长标题 -  法语版']
  const seasons = ['第十二季', '第十三季'].map(name => `/by-season/很长的动漫整理用目录名称${name}`)
  const single = `/another/${prefix}Season 1.mkv`
  const rows = [...paths, ...volumes, ...similar, ...seasons, single].map((path, index) => row(String(index), `${path}/01.mkv`))
  const groups = directoryGroups(rows, '/')
  const names = mobileDirectoryNames(groups)
  assert.deepEqual(paths.map(path => names.get(path).text), ['Season 1', 'Season 2', 'Specials'])
  assert.deepEqual(volumes.map(path => names.get(path).text), ['Part 01', 'Part 02'])
  assert.ok(similar.every(path => !names.has(path)))
  assert.deepEqual(seasons.map(path => names.get(path).text), ['第十二季', '第十三季'])
  assert.equal(names.has(single), false)
  assert.equal(names.has('/'), false)
  assert.deepEqual(groups.find(group => group.path === '/show').children.map(group => group.path), paths)
})

test('mobile directory names keep sibling suffixes when a directory exactly matches their shared prefix', () => {
  const base = '[Snow-Raws] 輪廻のラグランジェ Flower declaration of your heart'
  const sameName = `/${base}/${base}`
  const differentParent = `/another/${base}`
  const contiguous = ['/others/The Long Story', '/others/The Long Storyteller']
  const paths = [sameName, `${sameName} 鴨川デイズ`, `${sameName} season2`, differentParent, `${differentParent} season2`, ...contiguous]
  const rows = paths.map((path, index) => row(String(index), `${path}/01.mkv`))
  const groups = directoryGroups(rows, '/')
  const names = mobileDirectoryNames(groups)
  assert.equal(names.has(`/${base}`), false)
  assert.deepEqual(names.get(sameName), { kind: 'same-as-parent' })
  assert.deepEqual(names.get(`${sameName} 鴨川デイズ`), { kind: 'suffix', text: '鴨川デイズ' })
  assert.deepEqual(names.get(`${sameName} season2`), { kind: 'suffix', text: 'season2' })
  assert.equal(names.has(differentParent), false)
  assert.deepEqual(names.get(`${differentParent} season2`), { kind: 'suffix', text: 'season2' })
  assert.ok(contiguous.every(path => !names.has(path)))
  assert.equal(groups.find(group => group.path === sameName).name, base)
})
