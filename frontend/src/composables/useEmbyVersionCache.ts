import { provide, ref, type InjectionKey } from 'vue'
import type { EmbyVersionPage } from '../types/embyVersion'

const MAX_ENTRIES = 24
const CACHE_TTL = 5 * 60_000

function createVersionCache() {
  const generation = ref(0)
  const entries = new Map<string, { page: EmbyVersionPage; expires: number }>()
  return {
    generation,
    read(key: string) {
      const entry = entries.get(key)
      if (!entry) return
      entries.delete(key)
      if (entry.expires <= Date.now()) return
      entries.set(key, entry)
      return entry.page
    },
    write(key: string, page: EmbyVersionPage, requestGeneration: number) {
      if (requestGeneration !== generation.value) return
      entries.delete(key)
      entries.set(key, { page, expires: Date.now() + CACHE_TTL })
      while (entries.size > MAX_ENTRIES) entries.delete(entries.keys().next().value!)
    },
    clear() { entries.clear(); generation.value++ },
  }
}

export const embyVersionCacheKey: InjectionKey<ReturnType<typeof createVersionCache>> = Symbol('embyVersionCache')

/** Each detail page owns its cache; responses never survive its authenticated lifecycle. */
export function provideEmbyVersionCache() {
  const cache = createVersionCache()
  provide(embyVersionCacheKey, cache)
  return cache
}
