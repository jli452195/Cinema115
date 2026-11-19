<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { IconInfoCircle } from '@tabler/icons-vue'
import FollowDialog from './FollowDialog.vue'
import ManagementLayout from './ManagementLayout.vue'
import '../styles/subscriptionLists.css'

defineProps<{ title: string; description: string; mobileTitle?: string; hideMobileInfo?: boolean }>()
defineEmits<{ logout: [] }>()
const infoOpen = ref(false)
const compactViewport = window.matchMedia('(max-width: 639px)')
const compact = ref(compactViewport.matches)
function syncCompact() {
  compact.value = compactViewport.matches
  if (!compact.value) infoOpen.value = false
}
onMounted(() => compactViewport.addEventListener('change', syncCompact))
onBeforeUnmount(() => compactViewport.removeEventListener('change', syncCompact))
</script>

<template>
  <ManagementLayout :title="title" :mobile-title="mobileTitle" :description="description" @logout="$emit('logout')">
    <template #mobile-actions>
      <button v-if="!hideMobileInfo" type="button" class="subscription-toolbar-action" :aria-label="(mobileTitle || title) + '说明'" aria-haspopup="dialog" @click="infoOpen = true">
        <IconInfoCircle :size="20" aria-hidden="true" />
      </button>
      <slot name="mobile-actions" />
    </template>
    <template v-if="!compact" #actions><slot name="actions" /></template>
    <slot />
    <FollowDialog v-if="infoOpen" :title="mobileTitle || title" @close="infoOpen = false"><p class="text-sm leading-6 text-slate-600 dark:text-slate-300">{{ description }}</p></FollowDialog>
  </ManagementLayout>
</template>
