const tmdbImagePattern = /^\/t\/p\/(w185|w300|w342|w500|w1280)\/([A-Za-z0-9_-]+\.(?:jpe?g|png|webp))$/i

export function tmdbImageURL(value: string) {
  if (!value || value.startsWith('/api/tmdb/image/')) return value
  try {
    const url = new URL(value)
    if (url.protocol !== 'https:' || url.hostname !== 'image.tmdb.org' || url.search || url.hash) return value
    const match = url.pathname.match(tmdbImagePattern)
    return match ? `/api/tmdb/image/${match[1]}/${encodeURIComponent(match[2])}` : value
  } catch {
    return value
  }
}
