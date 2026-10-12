<script setup lang="ts">
import { ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import RegistrationLibrarySettings from '../components/RegistrationLibrarySettings.vue'
import PortalInvitesPanel from '../components/PortalInvitesPanel.vue'
import '../styles/portalInvites.css'

defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const librarySettings = ref<InstanceType<typeof RegistrationLibrarySettings>>()
const registrationPaused = ref(false)
const registrationError = ref('')
</script>

<template>
  <ManagementLayout class="portal-invites-page" title="注册邀请" description="分享邀请，创建 Emby 账号。每个邀请仅可使用一次，未使用的邀请可随时撤销。" @logout="$emit('logout')">
    <template #actions><RouterLink to="/emby/proxy" class="invite-header-link">播放与下载设置</RouterLink><RouterLink to="/emby/users" class="invite-header-link">Emby 用户管理</RouterLink></template>
    <PortalInvitesPanel :token="token" :registration-paused="registrationPaused" :registration-error="registrationError" @edit-libraries="librarySettings?.openEditor()">
      <template #settings><RegistrationLibrarySettings ref="librarySettings" :token="token" @state-change="registrationPaused = $event.loaded && !$event.selectedCount; registrationError = $event.error" /></template>
    </PortalInvitesPanel>
  </ManagementLayout>
</template>
