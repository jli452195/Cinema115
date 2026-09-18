// Public share aliases are normalized before entering the shared STRM task
// flow. The backend repeats this validation and canonicalization.
export function normalize115Share(value: string): string {
  if (value.length > 4096) return ''
  try {
    const url = new URL(value)
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.port) return ''
    if (!['115.com', 'www.115.com', '115cdn.com', 'www.115cdn.com', 'anxia.com', 'www.anxia.com'].includes(url.hostname.toLowerCase())) return ''
    const match = /^\/s\/([a-zA-Z0-9_-]+)\/?$/.exec(url.pathname)
    if (!match) return ''
    const canonical = new URL(`https://115cdn.com/s/${match[1]}`)
    const password = url.searchParams.get('password')?.trim()
    if (password) canonical.searchParams.set('password', password)
    return canonical.toString()
  } catch { return '' }
}

export function is115Share(value: string): boolean { return normalize115Share(value) !== '' }
