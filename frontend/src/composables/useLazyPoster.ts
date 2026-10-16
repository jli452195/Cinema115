import { computed, onBeforeUnmount, onMounted, ref, watch, type Ref } from 'vue'
import { tmdbImageURL } from '../utils/tmdbImage'

const missingPosters = new Map<string, number>()

// Fetch once so the poster and its ambient background share the same decoded asset.
export function useLazyPoster(element: Ref<HTMLElement | undefined>, source: () => string, token: () => string) {
  const visible = ref(false), imageURL = ref('')
  const thumbnail = computed(() => tmdbImageURL(source()).replace(/^\/api\/tmdb\/image\/(?:w185|w300|w342|w500|w1280)\//, '/api/tmdb/image/w185/'))
  let observer: IntersectionObserver | undefined
  onMounted(() => {
    if (!element.value) return
    if (typeof IntersectionObserver === 'undefined') { visible.value = true; return }
    observer = new IntersectionObserver(entries => {
      if (entries.some(entry => entry.isIntersecting)) {
        visible.value = true
        observer?.disconnect()
      }
    }, { rootMargin: '100px' })
    observer.observe(element.value)
  })
  onBeforeUnmount(() => observer?.disconnect())

  watch([visible, thumbnail, token], async ([show, address, credential], _previous, onCleanup) => {
    const reader = new AbortController()
    let objectURL = ''
    onCleanup(() => {
      reader.abort()
      if (objectURL) URL.revokeObjectURL(objectURL)
    })
    imageURL.value = ''
    if (!show || !address || (missingPosters.get(address) || 0) > Date.now()) return
    missingPosters.delete(address)
    const authenticated = address.startsWith('/api/admin/emby/posters/')
    if (authenticated && !credential) return
    try {
      const response = await fetch(address, {
        headers: authenticated ? { Authorization: `Bearer ${credential}` } : undefined,
        signal: AbortSignal.any([reader.signal, AbortSignal.timeout(20000)]),
        referrerPolicy: 'no-referrer',
      })
      if (reader.signal.aborted) return
      if (response.status === 404) {
        if (missingPosters.size >= 512) missingPosters.delete(missingPosters.keys().next().value!)
        missingPosters.set(address, Date.now() + 5 * 60 * 1000)
      }
      if (!response.ok) return
      const data = await response.blob()
      if (reader.signal.aborted || !data.type.startsWith('image/')) return
      objectURL = URL.createObjectURL(data)
      imageURL.value = objectURL
    } catch {
      // Images are optional. A failed/cancelled image never replaces the detection result.
    }
  }, { immediate: true })
  return imageURL
}
