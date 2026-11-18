import type { ComputedRef, InjectionKey } from 'vue'

export interface MobileToolBackTarget { label: string; to: string }

interface MobileToolNavigation {
  enabled: ComputedRef<boolean>
  opened: ComputedRef<boolean>
  back: ComputedRef<MobileToolBackTarget>
  controls: string
  openTools: (event: MouseEvent) => void
}

export const mobileToolNavigationKey: InjectionKey<MobileToolNavigation> = Symbol('mobileToolNavigation')
