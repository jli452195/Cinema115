<script setup lang="ts">
import { ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import PortalInvitesPanel from '../components/PortalInvitesPanel.vue'
import MemberPlaybackAdmin from '../components/MemberPlaybackAdmin.vue'
defineProps<{token:string}>()
defineEmits<{logout:[]}>()
const tab=ref('invites')
</script>
<template>
 <ManagementLayout title="邀请与播放设置" description="使用 Emby 账号登录个人中心，通过邀请选择播放开通方式。" @logout="$emit('logout')">
 <div class="mb-6 flex gap-4 text-sm"><button @click="tab='invites'" :class="tab==='invites'?'text-blue-600':'text-slate-500'">注册邀请</button><button @click="tab='playback'" :class="tab==='playback'?'text-blue-600':'text-slate-500'">播放设置</button><RouterLink to="/emby/users" class="text-blue-600">Emby 用户管理</RouterLink></div>
 <PortalInvitesPanel v-if="tab==='invites'" :token="token"/><MemberPlaybackAdmin v-else :token="token"/>
 </ManagementLayout>
</template>
