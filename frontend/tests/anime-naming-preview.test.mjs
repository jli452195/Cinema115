import assert from 'node:assert/strict'
import fs from 'node:fs'
import { createRequire } from 'node:module'
import test from 'node:test'
import ts from 'typescript'

const filename = new URL('../src/utils/animeNamingPreview.ts', import.meta.url)
const compiled = ts.transpileModule(fs.readFileSync(filename, 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
const mod = { exports: {} }
new Function('module', 'exports', 'require', compiled)(mod, mod.exports, createRequire(import.meta.url))
const { projectAnimeNaming, animeFilenamePart, animeNamingPath, animeNamingFolder } = mod.exports
const fixture = JSON.parse(fs.readFileSync(new URL('../../backend/internal/model/testdata/anime_naming_preview.json', import.meta.url), 'utf8'))

for (const example of fixture.cases) test(`Go/TS naming contract: ${example.name}`, () => {
  const mapping = fixture.plan.sources.find(source => source.sourceRef === example.sourceRef) || null
  assert.deepEqual(projectAnimeNaming(fixture.plan.target, mapping, example.parse, example.group), example.expected)
})
for (const [index, example] of fixture.filenameCases.entries()) test(`Go/TS filename cleaning contract ${index + 1}`, () => assert.equal(animeFilenamePart(example.input), example.expected))

test('UTF-8 byte limits match actual organization, including extension reservation', () => {
  const target = { ...fixture.plan.target, title: '界'.repeat(100) }, group = '组'.repeat(40)
  const folder = `${'界'.repeat(60)} (2026) {tmdb-335790}`
  assert.equal(animeNamingFolder(target), folder)
  assert.equal(animeNamingPath(target, 1, 1, group), `${folder}/Season 01/${'界'.repeat(38)}.S01E01.${'组'.repeat(26)}`)
  assert.equal(animeNamingPath(target, 1, 1, group, '.mkv'), `${folder}/Season 01/${'界'.repeat(36)}.S01E01.${'组'.repeat(26)}.mkv`)
  assert.equal(animeFilenamePart('𠮷'.repeat(121)), '𠮷'.repeat(120), 'truncate Unicode code points, not UTF-16 units')
})
