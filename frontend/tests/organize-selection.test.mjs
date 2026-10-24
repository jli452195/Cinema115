import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'

const source = await readFile(new URL('../src/utils/organizeSelection.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
const { directoryFileRows, directoryGroups, rowExcluded, setRangeSelection } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
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
