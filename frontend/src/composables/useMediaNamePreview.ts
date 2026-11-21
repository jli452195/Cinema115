import { shallowRef } from 'vue'
import { isPortalAdmin, portalToken } from './usePortalAuth'

export const mediaNamePreview = shallowRef<{ token: string }>()

export function openMediaNamePreview() {
  const token = portalToken.value
  if (!isPortalAdmin.value || !token || mediaNamePreview.value) return
  mediaNamePreview.value = { token }
}

export function closeMediaNamePreview() {
  mediaNamePreview.value = undefined
}
