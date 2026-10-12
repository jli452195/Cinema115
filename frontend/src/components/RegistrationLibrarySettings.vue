<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { memberRequest } from '../types/memberPlayback'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ stateChange: [state: { loaded: boolean; selectedCount: number; error: string }] }>()
const initialLibraryIds = ref<string[]>([])
const libraryIds = ref<string[]>([])
const libraries = ref<{ ItemId: string; Name: string; CollectionType: string }[]>([])
const busy = ref(false), saving = ref(false), loaded = ref(false), libraryLoading = ref(false), libraryLoaded = ref(false)
const error = ref(''), notice = ref(''), query = ref('')
const dialog = ref<HTMLDialogElement>()
const closeButton = ref<HTMLButtonElement>()
const continueButton = ref<HTMLButtonElement>()
const editorOpen = ref(false), confirmClose = ref(false)
const lifecycle = new AbortController()
let previousFocus: HTMLElement | undefined
let previousOverflow = ''
let backdropPressed = false

const missingLibraries = computed(() => libraryLoaded.value ? libraryIds.value.filter(id => !libraries.value.some(library => library.ItemId === id)) : [])
const savedMissingCount = computed(() => libraryLoaded.value ? initialLibraryIds.value.filter(id => !libraries.value.some(library => library.ItemId === id)).length : 0)
const savedLibraries = computed(() => libraries.value.filter(library => initialLibraryIds.value.includes(library.ItemId)))
const filteredLibraries = computed(() => {
  const search = query.value.trim().toLocaleLowerCase()
  return libraries.value.filter(library => !search || `${library.Name} ${library.CollectionType}`.toLocaleLowerCase().includes(search))
})
const isDirty = computed(() => {
  if (libraryIds.value.length !== initialLibraryIds.value.length) return true
  const saved = new Set(initialLibraryIds.value)
  return libraryIds.value.some(id => !saved.has(id))
})
watch([loaded, libraryLoaded, initialLibraryIds, error], () => emit('stateChange', { loaded: loaded.value, selectedCount: initialLibraryIds.value.length, error: loaded.value && libraryLoaded.value ? '' : error.value }))

function reset() { libraryIds.value = [...initialLibraryIds.value]; confirmClose.value = false }
function openEditor() {
  if (editorOpen.value || !dialog.value) return
  reset(); query.value = ''; notice.value = ''
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : undefined
  previousOverflow = document.body.style.overflow
  editorOpen.value = true
  document.body.style.overflow = 'hidden'
  dialog.value.showModal()
}
function closeEditor() {
  dialog.value?.close()
  editorOpen.value = false; confirmClose.value = false
  document.body.style.overflow = previousOverflow
  if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true })
}
function requestClose() {
  if (saving.value) return
  if (confirmClose.value) { confirmClose.value = false; closeButton.value?.focus({ preventScroll: true }); return }
  if (isDirty.value) {
    confirmClose.value = true
    void nextTick(() => { if (editorOpen.value) continueButton.value?.focus({ preventScroll: true }) })
    return
  }
  closeEditor()
}
function discardAndClose() { reset(); closeEditor() }
function outsideDialog(event: MouseEvent) {
  if (event.target !== dialog.value) return
  const rect = dialog.value.getBoundingClientRect()
  return event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom
}
function backdropClick(event: MouseEvent) {
  const startedOutside = backdropPressed
  backdropPressed = false
  if (startedOutside && outsideDialog(event)) requestClose()
}
async function loadLibraries() {
  if (libraryLoading.value) return
  libraryLoading.value = true; error.value = ''
  try {
    const data = await memberRequest(props.token, lifecycle.signal, '/libraries')
    if (lifecycle.signal.aborted) return
    libraries.value = data.libraries || []; libraryLoaded.value = true
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '媒体库读取失败'
  } finally { libraryLoading.value = false }
}
async function load() {
  if (busy.value) return
  busy.value = true; error.value = ''
  try {
    const data = await memberRequest(props.token, lifecycle.signal, '/settings')
    if (lifecycle.signal.aborted) return
    initialLibraryIds.value = [...data.settings.libraryIds]; reset(); loaded.value = true
    await loadLibraries()
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '设置读取失败'
  } finally { busy.value = false }
}
async function save() {
  if (busy.value || !loaded.value || !libraryLoaded.value || missingLibraries.value.length || !isDirty.value) return
  busy.value = true; saving.value = true; error.value = ''; notice.value = ''
  try {
    const data = await memberRequest(props.token, lifecycle.signal, '/registration-settings', { libraryIds: [...libraryIds.value] })
    if (lifecycle.signal.aborted) return
    initialLibraryIds.value = [...data.settings.libraryIds]; reset()
    notice.value = '已保存，仅用于之后创建的新账号。'
    closeEditor()
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '保存失败'
  } finally { busy.value = false; saving.value = false }
}
defineExpose({ openEditor })
onMounted(() => { void load() })
onBeforeUnmount(() => { lifecycle.abort(); if (editorOpen.value) closeEditor() })
</script>

<template>
  <section class="invite-card invite-theme invite-library-summary" aria-labelledby="registration-library-heading">
    <div class="invite-section-heading"><h2 id="registration-library-heading" class="invite-title-with-icon"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M8 4v16M16 4v16M3 9h5M3 15h5M16 9h5M16 15h5"/></svg>默认媒体库</h2><button type="button" class="invite-button invite-button-text" aria-haspopup="dialog" @click="openEditor">编辑</button></div>
    <p class="invite-library-count">{{ loaded ? `已选 ${initialLibraryIds.length} 个媒体库` : error ? '设置暂不可用' : '正在读取设置' }}</p>
    <div v-if="savedLibraries.length" class="invite-library-preview"><span v-for="library in savedLibraries.slice(0, 6)" :key="library.ItemId">{{ library.Name }}</span><span v-if="savedLibraries.length > 6">另 {{ savedLibraries.length - 6 }} 个</span></div>
    <p class="invite-muted invite-caption">仅在邀请注册和创建播放子账号时应用一次。已有用户的权限由 Emby 管理。</p>
    <p v-if="loaded && !initialLibraryIds.length" class="invite-message invite-message-warning">尚未选择媒体库，邀请注册和新建子账号已暂停。</p>
    <p v-if="savedMissingCount" class="invite-message invite-message-warning">有 {{ savedMissingCount }} 个已选媒体库失效，请编辑检查。</p>
    <p v-if="error && !editorOpen" role="alert" class="invite-message invite-message-error">{{ error }}<button type="button" class="invite-button invite-button-text" :disabled="busy || libraryLoading" @click="loaded ? loadLibraries() : load()">重试</button></p>
    <p v-if="notice && !editorOpen" role="status" class="invite-message invite-message-success">{{ notice }}</p>
  </section>
  <Teleport to="body">
    <dialog ref="dialog" class="invite-dialog invite-theme" aria-labelledby="library-dialog-title" aria-describedby="library-dialog-description" @cancel.prevent="requestClose" @pointerdown="backdropPressed = !!outsideDialog($event)" @click="backdropClick">
      <form class="invite-dialog-form" @submit.prevent="save">
        <header class="invite-dialog-heading">
          <div><h2 id="library-dialog-title">新账号的默认媒体库</h2><p id="library-dialog-description" class="invite-muted invite-caption">仅用于新账号创建，后续权限由 Emby 管理。</p></div>
          <button ref="closeButton" autofocus type="button" class="invite-icon-button" aria-label="关闭媒体库设置" :disabled="saving" @click="requestClose"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="m6 6 12 12M18 6 6 18" /></svg></button>
        </header>
        <div class="invite-dialog-body">
          <label class="invite-search"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4.5 4.5"/></svg><input v-model="query" type="search" placeholder="搜索媒体库" aria-label="搜索媒体库" /></label>
          <fieldset class="invite-library-fieldset" :disabled="busy || !loaded || !libraryLoaded">
            <legend class="invite-sr-only">新账号可访问的媒体库</legend>
            <div class="invite-library-tools"><span class="invite-muted invite-caption">已选 {{ libraryIds.length }} / {{ libraries.length }}</span><div><button type="button" class="invite-button invite-button-text" :disabled="libraryLoading" @click="loadLibraries">{{ libraryLoading ? '读取中' : '刷新' }}</button><button type="button" class="invite-button invite-button-text" @click="libraryIds = libraries.map(library => library.ItemId)">选择当前全部</button><button type="button" class="invite-button invite-button-text" @click="libraryIds = []">清空</button></div></div>
            <div class="invite-library-grid">
              <label v-for="library in filteredLibraries" :key="library.ItemId" class="invite-library-option" :class="{ 'is-selected': libraryIds.includes(library.ItemId) }"><input v-model="libraryIds" type="checkbox" :value="library.ItemId" class="invite-sr-only" /><span class="invite-library-name">{{ library.Name }}</span><span class="invite-library-bottom"><span class="invite-library-type">{{ library.CollectionType }}</span><span class="invite-selection-label" aria-hidden="true">已选</span></span></label>
            </div>
            <p v-if="libraryLoaded && !libraries.length" class="invite-muted invite-caption">Emby 尚未返回媒体库，请添加媒体库后刷新。</p>
            <p v-else-if="libraryLoaded && !filteredLibraries.length" class="invite-muted invite-caption">没有匹配的媒体库，试试其他关键词。</p>
            <p v-if="missingLibraries.length" class="invite-message invite-message-warning">有 {{ missingLibraries.length }} 个已选媒体库不在当前服务中。<button type="button" class="invite-button invite-button-text" @click="libraryIds = libraryIds.filter(id => !missingLibraries.includes(id))">移除失效选择</button></p>
            <p v-if="!libraryIds.length && loaded" class="invite-message invite-message-warning">保存空选择会暂停邀请注册和新建子账号，已有用户不受影响。</p>
          </fieldset>
          <p v-if="error" role="alert" class="invite-message invite-message-error">{{ error }}<button type="button" class="invite-button invite-button-text" :disabled="busy || libraryLoading" @click="loaded ? loadLibraries() : load()">重试</button></p>
          <p v-if="busy && !loaded" role="status" class="invite-muted invite-caption">正在读取设置</p>
        </div>
        <footer class="invite-dialog-footer">
          <template v-if="confirmClose"><p role="status" class="invite-caption">有未保存的修改，是否放弃？</p><div class="invite-dialog-actions"><button ref="continueButton" type="button" class="invite-button invite-button-secondary" @click="confirmClose = false; closeButton?.focus({ preventScroll: true })">继续编辑</button><button type="button" class="invite-button invite-button-danger" @click="discardAndClose">放弃并关闭</button></div></template>
          <template v-else><p class="invite-muted invite-caption">{{ isDirty ? '未保存，注册仍使用已保存的模板' : '修改不会影响已有用户权限' }}</p><div class="invite-dialog-actions"><button type="button" class="invite-button invite-button-secondary" :disabled="saving" @click="requestClose">取消</button><button type="submit" class="invite-button invite-button-primary" :disabled="busy || !loaded || !libraryLoaded || !!missingLibraries.length || !isDirty">{{ saving ? '保存中' : !libraryIds.length && loaded ? '保存并暂停注册' : '保存更改' }}</button></div></template>
        </footer>
      </form>
    </dialog>
  </Teleport>
</template>
