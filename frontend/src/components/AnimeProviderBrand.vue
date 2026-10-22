<script setup lang="ts">
import type { AnimeProviderID } from '../types/animeCatalog'
import mikanMascot from '../assets/anime-providers/mikan-mascot.webp'
import mikanWordmark from '../assets/mikan/mikan-text.svg'
import anibtLogo from '../assets/anime-providers/anibt-logo.webp'
import gardenLogo from '../assets/anime-providers/garden.svg'

defineProps<{ provider: AnimeProviderID; compact?: boolean }>()
</script>

<template>
  <span class="provider-brand" :class="{ 'brand-compact': compact }" aria-hidden="true">
    <template v-if="provider === 'mikan'"><img :src="mikanMascot" class="provider-mikan-mascot" width="36" height="36" alt="" /><img :src="mikanWordmark" class="provider-mikan-wordmark" width="80" height="20" alt="" /></template>
    <img v-else-if="provider === 'anibt'" :src="anibtLogo" class="provider-anibt-logo" width="82" height="42" alt="" />
    <template v-else><img :src="gardenLogo" class="provider-garden-logo" width="28" height="28" alt="" /><span class="provider-garden-name">Anime<span>Garden</span></span></template>
  </span>
</template>

<style scoped>
.provider-brand { display: inline-flex; height: 40px; min-width: 0; align-items: center; justify-content: center; gap: 7px; }
.provider-brand img { display: block; flex: none; object-fit: contain; }
.provider-mikan-mascot { width: 36px; height: 36px; }
.provider-mikan-wordmark { width: 80px; height: 20px; }
.provider-anibt-logo { width: 82px; height: 42px; }
.provider-garden-logo { width: 28px; height: 28px; }
.provider-garden-name { display: flex; flex-direction: column; align-items: flex-start; font-size: 12px; font-weight: 700; line-height: 14px; color: #8f405c; letter-spacing: .015em; }
.dark .provider-garden-name { color: #f2b0c7; }
@media (max-width: 639px) {
  .provider-brand { gap: 4px; }
  .provider-mikan-mascot { width: 28px; height: 28px; }
  .provider-mikan-wordmark { width: 58px; height: 16px; }
  .provider-anibt-logo { width: 74px; height: 38px; }
  .provider-garden-logo { width: 25px; height: 25px; }
  .provider-garden-name { font-size: 11px; line-height: 13px; }
}
@media (max-width: 359px) { .provider-mikan-mascot { width: 25px; height: 25px; } .provider-mikan-wordmark { width: 48px; height: 14px; } .provider-anibt-logo { width: 68px; height: 35px; } .provider-garden-logo { width: 22px; height: 22px; } .provider-garden-name { font-size: 10px; line-height: 12px; } }
.brand-compact { height: 32px; gap: 4px; }
.brand-compact .provider-mikan-mascot { width: 25px; height: 25px; }
.brand-compact .provider-mikan-wordmark { width: 55px; height: 16px; }
.brand-compact .provider-anibt-logo { width: 65px; height: 32px; }
.brand-compact .provider-garden-logo { width: 21px; height: 21px; }
.brand-compact .provider-garden-name { font-size: 10px; line-height: 12px; }
@media (max-width: 639px) { .brand-compact .provider-mikan-mascot { width: 21px; height: 21px; } .brand-compact .provider-mikan-wordmark { width: 47px; height: 14px; } }
</style>
