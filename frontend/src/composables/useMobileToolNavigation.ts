import type { ComputedRef, InjectionKey } from 'vue'
import type { AccountLink } from '../utils/accountNavigation'

export interface MobileToolBackTarget { label: string; to: string }

interface MobileToolNavigation {
  tool: ComputedRef<AccountLink | undefined>
  toolBack: ComputedRef<MobileToolBackTarget>
}

export const mobileToolNavigationKey: InjectionKey<MobileToolNavigation> = Symbol('mobileToolNavigation')
