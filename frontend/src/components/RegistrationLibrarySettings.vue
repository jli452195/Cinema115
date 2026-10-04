<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { memberRequest } from '../types/memberPlayback'
const props = defineProps<{ token: string }>()
const initialLibraryIds = ref<string[]>([])
const libraryIds = ref<string[]>([]), libraries = ref<{ ItemId: string; Name: string; CollectionType: string }[]>([])
const busy = ref(false), loaded = ref(false), libraryLoading = ref(false), libraryLoaded = ref(false)
const error = ref(''), notice = ref('')
const lifecycle = new AbortController()

const missingLibraries = computed(() => libraryLoaded.value ? libraryIds.value.filter(id => !libraries.value.some(l => l.ItemId === id)) : [])
const isDirty = computed(() => {
  if (libraryIds.value.length !== initialLibraryIds.value.length) return true
  const a = [...libraryIds.value].sort()
  const b = [...initialLibraryIds.value].sort()
  return a.some((val, index) => val !== b[index])
})

function reset() {
  libraryIds.value = [...initialLibraryIds.value]
  notice.value = ''
}

async function loadLibraries() {
 if (libraryLoading.value) return
 libraryLoading.value = true; error.value = ''
 try { libraries.value = (await memberRequest(props.token, lifecycle.signal, '/libraries')).libraries || []; libraryLoaded.value = true }
 catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '媒体库读取失败' }
 finally { libraryLoading.value = false }
}
async function save() {
 if (busy.value || !loaded.value) return
 busy.value = true; error.value = ''; notice.value = ''
 try { 
    const data = await memberRequest(props.token, lifecycle.signal, '/registration-settings', { libraryIds: libraryIds.value }); 
    libraryIds.value = [...data.settings.libraryIds]; 
    initialLibraryIds.value = [...data.settings.libraryIds];
    notice.value = '已保存，仅用于之后创建的新账号。已有用户权限保持不变。' 
 }
 catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '保存失败' }
 finally { busy.value = false }
}
onMounted(async () => {
 try { 
   const data = await memberRequest(props.token, lifecycle.signal, '/settings'); 
   libraryIds.value = [...data.settings.libraryIds]; 
   initialLibraryIds.value = [...data.settings.libraryIds];
   loaded.value = true; 
   await loadLibraries() 
 }
 catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取失败' }
})
onBeforeUnmount(() => lifecycle.abort())
</script>
<template>
 <form class="mb-6 space-y-4 rounded-2xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900" @submit.prevent="save">
  <h2 class="font-semibold text-slate-900 dark:text-slate-100">新注册用户的默认媒体库</h2>
  <p class="text-sm leading-6 text-slate-500">仅在邀请注册和创建播放子账号时应用一次，后续由管理员在 Emby 中调整。修改模板不会改写已有用户权限，也不影响 302 启动。</p>
      <fieldset class="space-y-3" :disabled="busy || !loaded">
        <legend class="text-sm text-slate-700 dark:text-slate-300">新账号可访问的媒体库</legend>
        <div class="flex flex-wrap gap-4 text-sm">
          <button type="button" :disabled="libraryLoading" class="text-blue-600 disabled:opacity-40 dark:text-blue-300" @click="loadLibraries">{{ libraryLoading ? '正在读取…' : '刷新媒体库' }}</button>
          <button type="button" :disabled="!libraryLoaded" class="text-blue-600 disabled:opacity-40 dark:text-blue-300" @click="libraryIds = libraries.map(library => library.ItemId)">选择当前全部</button>
          <button type="button" class="text-slate-500 dark:text-slate-400" @click="libraryIds = []">清空选择</button>
        </div>
        <div class="flex flex-wrap gap-2.5 pt-2">
          <label v-for="library in libraries" :key="library.ItemId" 
            class="relative flex cursor-pointer items-center gap-2 rounded-xl border px-4 py-2.5 transition-colors hover:bg-slate-50 dark:hover:bg-slate-800"
            :class="libraryIds.includes(library.ItemId) ? 'border-blue-500 bg-blue-50 text-blue-900 dark:border-blue-400/50 dark:bg-blue-900/20 dark:text-blue-100' : 'border-slate-200 bg-white text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300'">
            <input v-model="libraryIds" type="checkbox" :value="library.ItemId" class="sr-only" />
            <span class="text-sm font-medium">{{ library.Name }}</span>
            <span class="text-[11px] opacity-60">{{ library.CollectionType }}</span>
          </label>
          <p v-if="libraryLoaded && !libraries.length" class="text-sm text-slate-500 dark:text-slate-400">Emby 尚未返回媒体库，请添加媒体库后刷新。</p>
        </div>
        <p class="text-xs text-slate-500 dark:text-slate-400">已选 {{ libraryIds.length }} 个。未选择媒体库时暂停邀请注册和新建子账号，已有用户不受影响。</p>
        <p v-if="missingLibraries.length" class="text-sm text-amber-700 dark:text-amber-300">有 {{ missingLibraries.length }} 个已选媒体库不在当前服务中。<button type="button" class="underline" @click="libraryIds = libraryIds.filter(id => !missingLibraries.includes(id))">移除失效选择</button></p>
      </fieldset>
  <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p><p v-if="notice" role="status" class="text-sm text-emerald-700">{{ notice }}</p>
  
  <Teleport to="body">
    <Transition
      enter-active-class="transition duration-300 ease-out origin-bottom"
      enter-from-class="translate-y-10 scale-95 opacity-0"
      enter-to-class="translate-y-0 scale-100 opacity-100"
      leave-active-class="transition duration-200 ease-in origin-bottom"
      leave-from-class="translate-y-0 scale-100 opacity-100"
      leave-to-class="translate-y-10 scale-95 opacity-0"
    >
      <div v-if="isDirty" class="fixed bottom-6 left-1/2 z-50 flex w-[90%] max-w-2xl -translate-x-1/2 items-center justify-between gap-4 rounded-2xl border border-slate-700 bg-slate-900 p-3 pl-6 shadow-2xl dark:bg-slate-800 sm:w-max">
        <span class="text-sm font-medium text-slate-200">您有未保存的媒体库更改</span>
        <div class="flex shrink-0 gap-2">
          <button type="button" :disabled="busy" class="rounded-lg px-4 py-2 text-sm font-medium text-slate-300 hover:bg-slate-800 hover:text-white disabled:opacity-50" @click="reset">放弃</button>
          <button type="button" :disabled="busy || !!missingLibraries.length" class="rounded-lg bg-blue-600 px-5 py-2 text-sm font-medium text-white hover:bg-blue-500 disabled:opacity-50" @click="save">{{ busy ? '保存中...' : '保存更改' }}</button>
        </div>
      </div>
    </Transition>
  </Teleport>
 </form>
</template>
