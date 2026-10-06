import { computed, ref, watch } from 'vue'
import { argbFromHex, Hct, hexFromArgb, SchemeTonalSpot, sourceColorFromImageBytes } from '@material/material-color-utilities'

const fallbackColor = argbFromHex('#64748b')

/** Keep artwork colors local to the detail view, including both theme modes. */
export function useMediaColors(imageURL: () => string) {
  const sourceColor = ref(fallbackColor)
  watch(imageURL, () => { sourceColor.value = fallbackColor }, { flush: 'sync' })

  function readPosterColors(event: Event) {
    const image = event.target
    if (!(image instanceof HTMLImageElement) || !image.naturalWidth ||
        image.src !== new URL(imageURL(), document.baseURI).href) return
    try {
      // Quantize a small sample of the already loaded poster; no extra request.
      const canvas = document.createElement('canvas')
      canvas.width = 48
      canvas.height = 72
      const context = canvas.getContext('2d', { willReadFrequently: true })
      if (!context) return
      context.drawImage(image, 0, 0, canvas.width, canvas.height)
      sourceColor.value = sourceColorFromImageBytes(context.getImageData(0, 0, canvas.width, canvas.height).data)
    } catch {
      // Missing canvas support or cross-origin artwork keeps the default palette.
      sourceColor.value = fallbackColor
    }
  }

  const mediaColorStyle = computed(() => {
    const colors: Record<string, string> = {}
    for (const mode of ['light', 'dark'] as const) {
      const scheme = new SchemeTonalSpot(Hct.fromInt(sourceColor.value), mode === 'dark', 0)
      const roles = {
        surface: scheme.surface,
        'on-surface': scheme.onSurface,
        'on-surface-variant': scheme.onSurfaceVariant,
        'surface-container-high': scheme.surfaceContainerHigh,
        'surface-container-highest': scheme.surfaceContainerHighest,
        'outline-variant': scheme.outlineVariant,
        primary: scheme.primary,
        'primary-container': scheme.primaryContainer,
        'on-primary-container': scheme.onPrimaryContainer,
      }
      for (const [role, color] of Object.entries(roles)) colors[`--detail-${mode}-${role}`] = hexFromArgb(color)
    }
    return colors
  })

  return { mediaColorStyle, readPosterColors }
}
