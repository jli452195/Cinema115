<script setup lang="ts">
import { ref } from 'vue'
import { IconInfoCircle } from '@tabler/icons-vue'
import FollowDialog from './FollowDialog.vue'
import ManagementLayout from './ManagementLayout.vue'
import '../styles/subscriptionLists.css'

defineProps<{ title: string; description: string }>()
defineEmits<{ logout: [] }>()
const infoOpen = ref(false)
</script>

<template>
  <ManagementLayout :title="title" :description="description" mobile-section-heading @logout="$emit('logout')">
    <template #mobile-title-tools>
      <button type="button" class="subscription-info" :aria-label="title + '说明'" aria-haspopup="dialog" @click="infoOpen = true">
        <IconInfoCircle :size="19" aria-hidden="true" />
      </button>
    </template>
    <template #mobile-actions><slot name="mobile-actions" /></template>
    <template #actions><slot name="actions" /></template>
    <slot />
    <FollowDialog v-if="infoOpen" :title="title" @close="infoOpen = false"><p class="text-sm leading-6 text-slate-600 dark:text-slate-300">{{ description }}</p></FollowDialog>
  </ManagementLayout>
</template>

<style scoped>
.subscription-info { display: grid; width: 44px; height: 44px; place-items: center; border-radius: 50%; color: rgb(var(--content-secondary)); }
.subscription-info:hover { color: rgb(var(--content-primary)); }
</style>
