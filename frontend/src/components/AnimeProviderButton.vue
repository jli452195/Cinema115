<script setup lang="ts">
import type { AnimeProviderID } from '../types/animeCatalog'
import mikanMascot from '../assets/anime-providers/mikan-mascot.webp'
import mikanWordmark from '../assets/mikan/mikan-text.svg'
import anibtLogo from '../assets/anime-providers/anibt-logo.webp'
import gardenLogo from '../assets/anime-providers/garden.svg'

defineProps<{ provider: AnimeProviderID; name: string; available: boolean; selected: boolean; compact?: boolean }>()
defineEmits<{ select: [] }>()
</script>

<template>
  <button type="button" class="anime-provider-button" :class="[`provider-${provider}`, { 'provider-compact': compact }]" :aria-label="available ? name : `${name} 待接入`" :aria-pressed="selected" :disabled="!available" :title="available ? `${compact ? '选择' : '浏览'}${name}` : `${name}将在后续步骤接入`" @click="$emit('select')">
    <span class="provider-brand" aria-hidden="true">
      <template v-if="provider === 'mikan'"><img :src="mikanMascot" class="provider-mikan-mascot" width="36" height="36" alt="" /><img :src="mikanWordmark" class="provider-mikan-wordmark" width="80" height="20" alt="" /></template>
      <img v-else-if="provider === 'anibt'" :src="anibtLogo" class="provider-anibt-logo" width="82" height="42" alt="" />
      <template v-else><img :src="gardenLogo" class="provider-garden-logo" width="28" height="28" alt="" /><span class="provider-garden-name">Anime<span>Garden</span></span></template>
    </span>
    <span class="provider-status" :aria-hidden="available ? 'true' : undefined">{{ available ? '' : '待接入' }}</span>
  </button>
</template>

<style scoped>
.anime-provider-button { --provider-accent: 247 162 46; position: relative; display: inline-flex; flex-direction: column; align-items: center; justify-content: center; min-width: 156px; min-height: 70px; padding: 6px 16px 8px; border-radius: 15px; background: rgb(148 163 184 / .065); color: rgb(var(--color-slate-500)); transition: background 180ms ease, box-shadow 180ms ease; }
.provider-anibt { --provider-accent: 235 119 156; }
.provider-garden { --provider-accent: 250 117 158; }
.provider-brand { display: inline-flex; height: 40px; min-width: 0; align-items: center; justify-content: center; gap: 7px; transition: transform 180ms ease; }
.provider-anibt .provider-brand { transition: transform 400ms cubic-bezier(.34, 1.56, .64, 1); }
.provider-brand img { display: block; flex: none; object-fit: contain; }
.provider-mikan-mascot { width: 36px; height: 36px; }
.provider-mikan-wordmark { width: 80px; height: 20px; }
.provider-anibt-logo { width: 82px; height: 42px; }
.provider-garden-logo { width: 28px; height: 28px; }
.provider-garden-name { display: flex; flex-direction: column; align-items: flex-start; font-size: 12px; font-weight: 700; line-height: 14px; color: #8f405c; letter-spacing: .015em; }
.provider-status { display: block; min-height: 12px; margin-top: 2px; font-size: 10px; font-weight: 500; line-height: 12px; }
.anime-provider-button[aria-pressed="true"] { background: rgb(var(--provider-accent) / .105); box-shadow: 0 3px 12px -7px rgb(var(--provider-accent) / .55); }
.anime-provider-button[aria-pressed="true"]::after { content: ''; position: absolute; bottom: 6px; left: calc(50% - 12px); width: 24px; height: 3px; border-radius: 3px; background: rgb(var(--provider-accent) / .8); }
.anime-provider-button:disabled { cursor: default; }
.anime-provider-button:disabled .provider-brand { opacity: .65; filter: saturate(.65); }
.anime-provider-button:focus-visible { outline: 2px solid rgb(var(--provider-accent)); outline-offset: 3px; }
.dark .anime-provider-button { background: rgb(148 163 184 / .065); color: rgb(var(--color-slate-400)); }
.dark .anime-provider-button[aria-pressed="true"] { background: rgb(var(--provider-accent) / .12); }
.dark .provider-garden-name { color: #f2b0c7; }
@media (hover: hover) and (pointer: fine) { .anime-provider-button:hover:not(:disabled) { background: rgb(var(--provider-accent) / .15); } }
@media (hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference) {
  .anime-provider-button:hover:not(:disabled) .provider-brand { transform: scale(1.04); }
  .provider-anibt:hover:not(:disabled) .provider-brand { transform: rotate(-3deg) scale(1.04); }
}
@media (prefers-reduced-motion: no-preference) { .provider-anibt:focus-visible:not(:disabled) .provider-brand { transform: rotate(-3deg) scale(1.04); } }
@media (max-width: 639px) {
  .anime-provider-button { width: 100%; min-width: 0; min-height: 66px; padding-inline: 7px; border-radius: 13px; }
  .provider-brand { gap: 4px; }
  .provider-mikan-mascot { width: 28px; height: 28px; }
  .provider-mikan-wordmark { width: 58px; height: 16px; }
  .provider-anibt-logo { width: 74px; height: 38px; }
  .provider-garden-logo { width: 25px; height: 25px; }
  .provider-garden-name { font-size: 11px; line-height: 13px; }
}
@media (max-width: 359px) { .provider-mikan-mascot { width: 25px; height: 25px; } .provider-mikan-wordmark { width: 48px; height: 14px; } .provider-anibt-logo { width: 68px; height: 35px; } .provider-garden-logo { width: 22px; height: 22px; } .provider-garden-name { font-size: 10px; line-height: 12px; } }
@media (prefers-reduced-motion: reduce) { .anime-provider-button, .anime-provider-button .provider-brand { transition: none; transform: none; } }
.anime-provider-button.provider-compact { min-width: 0; width: 116px; min-height: 44px; padding: 4px 10px; border-radius: 10px; }
.provider-compact .provider-brand { height: 32px; gap: 4px; }
.provider-compact .provider-mikan-mascot { width: 25px; height: 25px; }
.provider-compact .provider-mikan-wordmark { width: 55px; height: 16px; }
.provider-compact .provider-anibt-logo { width: 65px; height: 32px; }
.provider-compact .provider-garden-logo { width: 21px; height: 21px; }
.provider-compact .provider-garden-name { font-size: 10px; line-height: 12px; }
.provider-compact .provider-status { display: none; }
.provider-compact[aria-pressed="true"]::after { bottom: 3px; height: 2px; }
@media (max-width: 639px) { .anime-provider-button.provider-compact { width: 100%; padding-inline: 5px; } .provider-compact .provider-mikan-mascot { width: 21px; height: 21px; } .provider-compact .provider-mikan-wordmark { width: 47px; height: 14px; } }
</style>
