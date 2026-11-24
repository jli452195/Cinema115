<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { memberRequest } from '../types/memberPlayback'
import { p115MembershipLabel, type PersonalDriveStatus } from '../types/p115Accounts'

const props = defineProps<{ token: string }>()
const drives = ref<PersonalDriveStatus[]>()
const busy = ref(false), checking = ref(''), error = ref('')
const lifecycle = new AbortController()
let timer: ReturnType<typeof setInterval>

async function refresh(userId = '') {
  if (busy.value) return
  busy.value = true
  checking.value = userId
  try {
    const suffix = userId ? `/${encodeURIComponent(userId)}/check` : ''
    const signal = AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)])
    const data = await memberRequest(props.token, signal, '/personal-drives' + suffix, userId ? {} : undefined) as { drives: PersonalDriveStatus[] }
    if (lifecycle.signal.aborted) return
    drives.value = data.drives
    error.value = ''
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取个人网盘状态失败'
  } finally {
    busy.value = false
    checking.value = ''
  }
}
defineExpose({ refresh, busy })
onMounted(() => {
  void refresh()
  timer = setInterval(() => { if (!document.hidden) void refresh() }, 10000)
})
onBeforeUnmount(() => { lifecycle.abort(); clearInterval(timer) })
</script>

<template>
  <section aria-labelledby="personal-drive-status-title" class="rounded-2xl border border-slate-200 bg-white dark:border-slate-700 dark:bg-slate-900">
    <header class="px-5 py-4">
      <h2 id="personal-drive-status-title" class="font-semibold text-slate-900 dark:text-slate-100">个人网盘状态</h2>
      <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">已签发链接按未到期的播放会话统计，含子账号。</p>
    </header>
    <p v-if="error" role="alert" class="px-5 pb-4 text-sm text-red-600 dark:text-red-300">{{ error }}</p>
    <p v-if="!drives && !error" role="status" class="px-5 pb-4 text-sm text-slate-500 dark:text-slate-400">正在读取个人网盘状态…</p>
    <p v-if="drives && !drives.length" class="px-5 pb-4 text-sm text-slate-500 dark:text-slate-400">暂无已绑定的个人网盘</p>
    <div v-if="drives?.length" class="relative overflow-x-auto">
      <table class="w-full text-left text-sm text-slate-700 dark:text-slate-200" aria-labelledby="personal-drive-status-title">
        <thead class="bg-slate-50 text-xs text-slate-500 dark:bg-slate-800/60 dark:text-slate-300">
          <tr>
            <th scope="col" class="whitespace-nowrap px-5 py-3 font-medium">Emby 用户</th>
            <th scope="col" class="whitespace-nowrap px-4 py-3 font-medium">网盘检查</th>
            <th scope="col" class="whitespace-nowrap px-4 py-3 font-medium">115 会员</th>
            <th scope="col" class="whitespace-nowrap px-4 py-3 font-medium">已签发链接</th>
            <th scope="col" class="px-5 py-3"><span class="sr-only">操作</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="drive in drives" :key="drive.userId" class="border-t border-slate-200 dark:border-slate-700">
            <th scope="row" class="min-w-32 px-5 py-3 font-medium">
              <span class="break-all">{{ drive.username || '名称待核实' }}</span>
              <span class="block text-xs font-normal text-slate-500 dark:text-slate-400">绑定编号 {{ drive.binding }}</span>
            </th>
            <td class="min-w-32 px-4 py-3">
              {{ drive.driveStatus.message }}
              <time v-if="drive.driveStatus.checkedAt" :datetime="drive.driveStatus.checkedAt" class="block text-xs text-slate-500 dark:text-slate-400">{{ new Date(drive.driveStatus.checkedAt).toLocaleString('zh-CN') }}</time>
            </td>
            <td class="min-w-32 px-4 py-3">
              <span :class="{ 'text-blue-700 dark:text-blue-300': drive.vip === 'vip', 'text-amber-700 dark:text-amber-300': drive.vip === 'normal' }">{{ p115MembershipLabel(drive.vip) }}</span>
              <time v-if="drive.vipCheckedAt" :datetime="drive.vipCheckedAt" class="block text-xs text-slate-500 dark:text-slate-400">{{ new Date(drive.vipCheckedAt).toLocaleString('zh-CN') }}</time>
            </td>
            <td class="px-4 py-3 tabular-nums">{{ drive.issuedLinks }}</td>
            <td class="px-5 py-3 text-right">
              <button type="button" :disabled="busy" :aria-label="'检查 ' + (drive.username || drive.binding) + ' 的个人网盘'" class="whitespace-nowrap text-sm text-blue-600 disabled:opacity-50 dark:text-blue-300" @click="refresh(drive.userId)">{{ checking === drive.userId ? '检查中…' : '检查' }}</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
