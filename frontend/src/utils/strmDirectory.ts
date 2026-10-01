// An explicit relative root avoids confusing a selected root with no selection.
export const STRM_ROOT = '.'

export function validSTRMDirectory(value: unknown): value is string {
  return value === STRM_ROOT || (typeof value === 'string' && !/[<>:"\\|?*\x00-\x1f]/.test(value) && value.split('/').every(part => part.trim() === part && part !== '' && part !== '.' && part !== '..'))
}

export function strmDirectoryPath(value: string): string {
  return value === STRM_ROOT ? '/strm' : value ? `/strm/${value}` : '未选择'
}

export function strmDirectoryName(value: string): string {
  return value === STRM_ROOT ? 'STRM 根目录' : value.split('/').filter(Boolean).at(-1) ?? '未选择'
}
