/// <reference lib="es2022.intl" />

import type { OrganizeRow } from '../types/organize'

export const parentDirectory = (filePath: string) => filePath.slice(0, filePath.lastIndexOf('/')) || '/'
export const inDirectory = (directory: string, root: string) => directory === root || directory.startsWith(root === '/' ? '/' : `${root}/`)
export const rowExcluded = (row: OrganizeRow, excluded: string[]) => excluded.some(root => inDirectory(parentDirectory(row.sourcePath), root))
const naturalCompare = (a: string, b: string) => a.localeCompare(b, 'zh-CN', { numeric: true })

export interface OrganizeDirectoryGroup {
  path: string
  label: string
  name: string
  depth: number
  rows: OrganizeRow[]
  descendants: OrganizeRow[]
  children: OrganizeDirectoryGroup[]
}

export function directoryGroups(rows: OrganizeRow[], root: string) {
  const paths = new Set([root])
  for (const row of rows) {
    let directory = parentDirectory(row.sourcePath)
    while (inDirectory(directory, root)) {
      paths.add(directory)
      if (directory === root || directory === '/') break
      directory = parentDirectory(directory)
    }
  }
  const ordered: string[] = []
  function visit(directory: string) {
    ordered.push(directory)
    for (const child of [...paths].filter(p => p !== directory && parentDirectory(p) === directory).sort(naturalCompare)) visit(child)
  }
  visit(root)
  const groups: OrganizeDirectoryGroup[] = ordered.map(directory => ({
    path: directory,
    label: directory === root ? '当前目录' : directory.slice(root === '/' ? 1 : root.length + 1),
    name: directory === root ? '当前目录' : directory.slice(directory.lastIndexOf('/') + 1),
    depth: directory === root ? 0 : directory.slice(root === '/' ? 1 : root.length + 1).split('/').length,
    rows: rows.filter(row => parentDirectory(row.sourcePath) === directory).sort((a, b) => naturalCompare(a.name, b.name)),
    descendants: rows.filter(row => inDirectory(parentDirectory(row.sourcePath), directory)),
    children: [],
  }))
  const byPath = new Map(groups.map(group => [group.path, group]))
  for (const group of groups) if (group.path !== root) byPath.get(parentDirectory(group.path))?.children.push(group)
  return groups
}

// Match the folder-first display order for range selection and numbering.
export function directoryFileRows(group: OrganizeDirectoryGroup): OrganizeRow[] {
  return [...group.children.flatMap(directoryFileRows), ...group.rows]
}

export function setRangeSelection(rows: OrganizeRow[], anchor: string, target: string, selected: boolean) {
  const end = rows.findIndex(row => row.id === target)
  if (end < 0) return
  const start = rows.findIndex(row => row.id === anchor)
  for (const row of rows.slice(Math.min(start < 0 ? end : start, end), Math.max(start < 0 ? end : start, end) + 1)) row.selected = selected
}

export interface OrganizeCompactName {
  head: string
  tail: string
  extension: string
  prefixOmitted: boolean
  tailStartsWithMarker: boolean
}

export type MobileDirectoryName = { kind: 'same-as-parent' } | { kind: 'suffix'; text: string }

const videoSuffix = /\.(?:mp4|mkv|ts|iso|rmvb|avi|mov|mpeg|mpg|wmv|3gp|asf|m4v|m2ts|webm|strm)$/i
const bracketPairs = new Map(Object.entries({ '[': ']', '(': ')', '【': '】', '（': '）', '「': '」', '『': '』', '《': '》' }))
const nameSeparator = /[\s._\-–—·、|,\])】）」』》]/u
const graphemes = typeof Intl.Segmenter === 'function' ? new Intl.Segmenter(undefined, { granularity: 'grapheme' }) : undefined

// 这些标记只用于保留可辨识的文本，不参与季集识别或命名。
const explicitFileMarker = /第\s*[\d０-９]+\s*[话話集回]|(?<![\p{L}\p{N}])(?:S\d{1,4}(?:[ ._-]*E[ ._-]*\d+|[ ._-]+\d+)|(?:EP?|VOL|PART|DISC|CD)[ ._-]*\d+|(?:NCOP|NCED|OVA|OAD|SP|OP|ED)(?:[ ._-]*\d+)?)(?![\p{L}\p{N}])/iu
const numberedFileMarker = /(?<![\p{L}\p{N}])[\d０-９]{1,3}(?:\s*[-~～–—]\s*[\d０-９]{1,3})?(?![\p{L}\p{N}])/gu
const explicitDirectoryMarker = /第\s*[\d０-９一二三四五六七八九十百零〇两]+\s*[季期卷]|特典|(?<![\p{L}\p{N}])(?:(?:SEASON|S|VOL(?:UME)?|PART|DISC|CD)[ ._-]*\d+|(?:SPECIALS?|EXTRAS?|OVA|OAD|NCOP|NCED)(?:[ ._-]*\d+)?)(?![\p{L}\p{N}])/giu

function includeOpeningBracket(stem: string, index: number | undefined) {
  if (index === undefined) return stem.length
  return bracketPairs.has(stem[index - 1]) ? index - 1 : index
}

function fileMarkerStart(stem: string) {
  const explicit = explicitFileMarker.exec(stem)
  const index = explicit?.index ?? Array.from(stem.matchAll(numberedFileMarker)).pop()?.index
  return includeOpeningBracket(stem, index)
}

function directoryMarkerStart(name: string, sharedLength = 0) {
  // 跳过同级目录已共享的季名，使后面的卷号或版本仍能露出。
  for (const match of name.matchAll(explicitDirectoryMarker)) {
    if (match.index + match[0].length > sharedLength) return includeOpeningBracket(name, match.index)
  }
  return name.length
}

function sharedNamePrefix(stems: string[], markerStart: (stem: string, sharedLength: number) => number, directory = false) {
  const first = stems[0] || ''
  let length = first.length
  for (const stem of stems) {
    let i = 0
    while (i < length && i < stem.length && first[i] === stem[i]) i++
    length = i
  }
  if (stems.length < 2 || length < 8) return 0
  const hasBaseName = stems.some(stem => stem.length === length)
  // 基础目录可以保留原名，其余目录只在完整词或括号边界后省略前缀。
  if (hasBaseName && (!directory || stems.some(stem => stem.length > length &&
    !nameSeparator.test(stem[length]) && !bracketPairs.has(stem[length])))) return 0
  const limit = stems.reduce((end, stem) => Math.min(end, markerStart(stem, length)), length)
  const expectedClosers: string[] = []
  let offset = 0
  let boundary = 0
  for (const char of first.slice(0, limit)) {
    offset += char.length
    const closer = bracketPairs.get(char)
    if (closer) expectedClosers.push(closer)
    else if (char === expectedClosers[expectedClosers.length - 1]) expectedClosers.pop()
    if (!expectedClosers.length && nameSeparator.test(char)) boundary = offset
  }
  // 中文标题可以紧贴季名，已识别的标记起点也是安全的切分位置。
  if (directory && !expectedClosers.length && (hasBaseName || limit < length)) boundary = limit
  if (boundary < 8) return 0
  const differences = stems.map(stem => stem.slice(boundary).trimStart())
  if (new Set(differences).size !== stems.length || (!directory && differences.some(difference => Array.from(difference).length < 2))) return 0
  return boundary
}

function compactName(stem: string, extension: string, prefix: number, markerStart: (stem: string) => number): OrganizeCompactName {
  if (prefix) {
    return { head: stem.slice(prefix).trimStart(), tail: '', extension, prefixOmitted: true, tailStartsWithMarker: false }
  }
  const marker = markerStart(stem)
  if (marker < stem.length) {
    return { head: stem.slice(0, marker), tail: stem.slice(marker), extension, prefixOmitted: false, tailStartsWithMarker: true }
  }
  const segments = graphemes ? Array.from(graphemes.segment(stem)) : []
  const middle = segments[Math.floor(segments.length / 2)]?.index ?? stem.length
  return { head: stem.slice(0, middle), tail: stem.slice(middle), extension, prefixOmitted: false, tailStartsWithMarker: false }
}

export function mobileFileNames(groups: OrganizeDirectoryGroup[], flat = false) {
  const result = new Map<string, OrganizeCompactName>()
  for (const group of groups) {
    const files = group.rows.map(row => {
      const extension = row.titleOnly ? '' : (row.name.match(videoSuffix)?.[0] ?? '')
      return { row, extension, stem: row.name.slice(0, row.name.length - extension.length) }
    })
    const realFiles = files.filter(({ row }) => !row.titleOnly)
    const distinct = new Set(realFiles.map(({ row }) => row.name)).size === realFiles.length
    const prefix = !flat && distinct ? sharedNamePrefix(realFiles.map(({ stem }) => stem), fileMarkerStart) : 0
    for (const { row, stem, extension } of files) {
      result.set(row.id, compactName(stem, extension, row.titleOnly ? 0 : prefix, fileMarkerStart))
    }
  }
  return result
}

export function mobileDirectoryNames(groups: OrganizeDirectoryGroup[]) {
  const result = new Map<string, MobileDirectoryName>()
  for (const { children, depth, name } of groups) {
    const prefix = sharedNamePrefix(children.map(child => child.name), directoryMarkerStart, true)
    for (const child of children) {
      if (depth > 0 && child.name === name) {
        result.set(child.path, { kind: 'same-as-parent' })
      } else if (prefix) {
        const text = child.name.slice(prefix).trimStart()
        if (text) result.set(child.path, { kind: 'suffix', text })
      }
    }
  }
  return result
}
