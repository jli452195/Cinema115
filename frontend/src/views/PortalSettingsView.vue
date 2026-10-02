<script setup lang="ts">
import ManagementLayout from '../components/ManagementLayout.vue'
import RegistrationLibrarySettings from '../components/RegistrationLibrarySettings.vue'
import PortalInvitesPanel from '../components/PortalInvitesPanel.vue'
defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
</script>

<template>
 <ManagementLayout title="注册邀请" description="生成邀请并分享给朋友，凭有效邀请创建 Emby 账号。每个邀请仅可使用一次，可随时撤销未使用的邀请。" @logout="$emit('logout')">
  <template #actions><RouterLink to="/emby/proxy" class="text-sm text-blue-600">播放与下载设置</RouterLink><RouterLink to="/emby/users" class="text-sm text-blue-600">Emby 用户管理</RouterLink></template>
  <RegistrationLibrarySettings :token="token" />
  <PortalInvitesPanel :token="token" />
 </ManagementLayout>
</template>
