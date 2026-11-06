export interface AppVersionInfo {
  version: string
  revision: string
  created: string
  displayVersion: string
  title: string
}

// 读取 HTML meta 中的应用构建版本元数据
export function readAppVersion(): AppVersionInfo {
  let version = ''
  let revision = ''
  let created = ''

  if (typeof document !== 'undefined') {
    version = document.querySelector('meta[name="cinema115-version"]')?.getAttribute('content')?.trim() || ''
    revision = document.querySelector('meta[name="cinema115-revision"]')?.getAttribute('content')?.trim() || ''
    created = document.querySelector('meta[name="cinema115-created"]')?.getAttribute('content')?.trim() || ''
  }

  const isDev = !version || version.toLowerCase() === 'dev' || version.toLowerCase() === 'development'
  const displayVersion = isDev ? '开发版' : version

  const details: string[] = []
  if (revision) details.push(`提交: ${revision}`)
  if (created) details.push(`构建: ${created}`)

  const title = details.length > 0
    ? `Cinema115 ${displayVersion}\n${details.join('\n')}`
    : `Cinema115 ${displayVersion}`

  return {
    version,
    revision,
    created,
    displayVersion,
    title,
  }
}

