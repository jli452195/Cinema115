import type { OrganizeRow } from '../types/manualOrganize'

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
