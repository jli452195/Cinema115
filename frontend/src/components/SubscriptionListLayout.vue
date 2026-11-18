<script setup lang="ts">
import { ref } from 'vue'
import { IconInfoCircle } from '@tabler/icons-vue'
import FollowDialog from './FollowDialog.vue'
import ManagementLayout from './ManagementLayout.vue'
import '../styles/subscriptionLists.css'

defineProps<{ title: string; description: string; mobileTitle?: string }>()
defineEmits<{ logout: [] }>()
const infoOpen = ref(false)
</script>

<template>
  <ManagementLayout :title="title" :mobile-title="mobileTitle" :description="description" @logout="$emit('logout')">
    <template #actions>
      <button type="button" class="subscription-toolbar-action sm:hidden" :aria-label="(mobileTitle || title) + '说明'" aria-haspopup="dialog" @click="infoOpen = true">
        <IconInfoCircle :size="20" aria-hidden="true" />
      </button>
      <slot name="actions" />
    </template>
    <slot />
    <FollowDialog v-if="infoOpen" :title="mobileTitle || title" @close="infoOpen = false"><p class="text-sm leading-6 text-slate-600 dark:text-slate-300">{{ description }}</p></FollowDialog>
  </ManagementLayout>
</template>
