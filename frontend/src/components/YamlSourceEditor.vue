<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { IconChevronDown, IconChevronRight } from '@tabler/icons-vue'
import { Compartment, EditorState, Text } from '@codemirror/state'
import { EditorView, highlightActiveLine, highlightActiveLineGutter, keymap, lineNumbers } from '@codemirror/view'
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import { bracketMatching, codeFolding, foldGutter, foldKeymap, HighlightStyle, indentUnit, syntaxHighlighting } from '@codemirror/language'
import { yaml } from '@codemirror/lang-yaml'
import { color, oneDark } from '@codemirror/theme-one-dark'
import { tags } from '@lezer/highlight'
import type { ClassificationDiagnostic } from '../types/classification'

const props = defineProps<{ modelValue: string; disabled?: boolean; diagnostic?: ClassificationDiagnostic }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; ready: [] }>()
const host = ref<HTMLElement>(), editorHost = ref<HTMLElement>(), foldIcons = ref<HTMLElement>(), plainInput = ref<HTMLTextAreaElement>()
const initializationFailed = ref(false)
const access = new Compartment()
let view: EditorView | undefined, source = props.modelValue, baseline = source, normalized = '', separator = '\n'
const permissions = () => [EditorState.readOnly.of(!!props.disabled), EditorView.editable.of(!props.disabled)]
function foldMarker(open: boolean) {
  const marker = document.createElement('span')
  marker.className = 'yaml-fold-marker'; marker.title = open ? '折叠' : '展开'
  marker.append(foldIcons.value!.querySelector(open ? '.fold-open' : '.fold-closed')!.cloneNode(true))
  return marker
}

function state(value: string) {
  baseline = value; normalized = value.replace(/\r\n?|\n/g, '\n'); separator = value.match(/\r\n?|\n/)?.[0] || '\n'
  return EditorState.create({
    doc: Text.of(normalized.split('\n')),
    extensions: [
      yaml(), lineNumbers(), highlightActiveLine(), highlightActiveLineGutter(), bracketMatching(),
      foldGutter({ markerDOM: foldMarker }), codeFolding({ placeholderDOM: (_view, onclick) => {
        const button = document.createElement('button')
        button.type = 'button'; button.className = 'yaml-fold-placeholder'; button.setAttribute('aria-label', '展开已折叠内容')
        button.append(foldMarker(false)); button.addEventListener('click', onclick); return button
      } }),
      indentUnit.of('  '), EditorState.tabSize.of(2), history(),
      keymap.of([...defaultKeymap, ...historyKeymap, ...foldKeymap, indentWithTab]), oneDark,
      // YAML plain values use `content` rather than the theme's string tag.
      syntaxHighlighting(HighlightStyle.define([{ tag: tags.content, color: color.sage }])),
      access.of(permissions()),
      EditorView.contentAttributes.of(() => ({
        role: 'textbox', 'aria-label': '分类 YAML 源码', 'aria-multiline': 'true',
        'aria-describedby': 'classification-source-help', 'aria-readonly': String(!!props.disabled),
        'aria-invalid': String(!!props.diagnostic), tabindex: '0', spellcheck: 'false',
        autocapitalize: 'off', autocomplete: 'off', autocorrect: 'off',
      })),
      EditorView.updateListener.of(update => {
        if (!update.docChanged) return
        source = update.state.doc.toString() === normalized ? baseline : update.state.doc.sliceString(0, undefined, separator)
        emit('update:modelValue', source)
      }),
    ],
  })
}

function editPlainSource(event: Event) {
  const value = (event.target as HTMLTextAreaElement).value
  source = value === normalized ? baseline : value.replace(/\r\n?|\n/g, separator)
  emit('update:modelValue', source)
}

watch(() => props.modelValue, value => {
  if (value === source) return
  source = value
  if (initializationFailed.value) {
    baseline = value; normalized = value.replace(/\r\n?|\n/g, '\n'); separator = value.match(/\r\n?|\n/)?.[0] || '\n'
  } else view?.setState(state(value))
})
watch(() => props.disabled, () => view?.dispatch({ effects: access.reconfigure(permissions()) }))
watch(() => props.diagnostic, () => view?.dispatch({}))
onMounted(() => {
  try { view = new EditorView({ parent: editorHost.value!, state: state(source) }) }
  catch { initializationFailed.value = true }
  emit('ready')
})
onBeforeUnmount(() => { view?.destroy(); view = undefined })

function focus() { if (initializationFailed.value) plainInput.value?.focus(); else view?.focus() }
function locate(line = 1, column = 1) {
  if (initializationFailed.value) {
    const input = plainInput.value
    if (!input) return
    const lines = input.value.split('\n'), row = Math.min(Math.max(1, Math.trunc(line) || 1), lines.length) - 1
    const anchor = lines.slice(0, row).reduce((total, text) => total + text.length + 1, 0) + Math.min(Math.max(0, Math.trunc(column) - 1 || 0), lines[row]!.length)
    input.focus(); input.setSelectionRange(anchor, Math.min(anchor + 1, input.value.length))
    input.scrollTop = Math.max(0, row * parseFloat(getComputedStyle(input).lineHeight) - input.clientHeight / 2)
    input.scrollIntoView({ block: 'nearest', behavior: 'instant' })
    return
  }
  if (!view) return
  const target = view.state.doc.line(Math.min(Math.max(1, Math.trunc(line) || 1), view.state.doc.lines))
  const anchor = target.from + Math.min(Math.max(0, Math.trunc(column) - 1 || 0), target.length)
  view.dispatch({ selection: { anchor, head: Math.min(anchor + 1, target.to) }, effects: EditorView.scrollIntoView(anchor, { y: 'center', x: 'nearest' }) })
  view.focus(); host.value?.scrollIntoView({ block: 'nearest', behavior: 'instant' })
}
defineExpose({ focus, locate, getValue: () => source })
</script>

<template>
  <div ref="host" class="yaml-source-editor" :class="{ 'is-readonly': disabled, 'is-invalid': diagnostic }">
    <span ref="foldIcons" hidden aria-hidden="true"><IconChevronDown class="fold-open" :size="14" aria-hidden="true" /><IconChevronRight class="fold-closed" :size="14" aria-hidden="true" /></span>
    <div v-if="!initializationFailed" ref="editorHost"></div>
    <template v-else>
      <p class="yaml-fallback-notice" role="status">代码编辑器暂不可用，已切换为纯文本编辑。</p>
      <textarea ref="plainInput" class="yaml-fallback-input" :value="modelValue" :readonly="disabled" :aria-readonly="!!disabled" :aria-invalid="!!diagnostic" aria-label="分类 YAML 源码" aria-describedby="classification-source-help" spellcheck="false" autocapitalize="off" autocomplete="off" autocorrect="off" wrap="off" @input="editPlainSource"></textarea>
    </template>
  </div>
</template>

<style scoped>
.yaml-source-editor { --scrollbar-thumb: #4b5260; --scrollbar-track: #282c34; min-width: 0; overflow: hidden; background: #282c34; color-scheme: dark; }
.yaml-source-editor:focus-within { outline: 1px solid #7d8799; outline-offset: -1px; }.yaml-source-editor.is-invalid { outline: 1px solid #e06c75; outline-offset: -1px; }.yaml-source-editor.is-readonly { opacity: .7; }
.yaml-source-editor :deep(.cm-editor) { height: clamp(300px, 55dvh, 560px); min-height: 260px; outline: none; font-size: 14px; }
.yaml-source-editor :deep(.cm-scroller) { overflow: auto; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; line-height: 1.8; }
.yaml-fallback-notice { margin: 0; padding: 10px 12px; color: #abb2bf; font-size: 13px; }
.yaml-fallback-input { display: block; width: 100%; height: clamp(300px, 55dvh, 560px); min-height: 260px; resize: vertical; overflow: auto; border: 0; border-radius: 0; outline: none; padding: 12px; background: #282c34; color: #abb2bf; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 14px; line-height: 1.8; tab-size: 2; }
.yaml-source-editor :deep(.cm-content) { min-width: 0; padding: 12px 0; }.yaml-source-editor :deep(.cm-line) { padding-inline: 12px; }
.yaml-source-editor :deep(.cm-lineNumbers .cm-gutterElement) { padding-inline: 10px 8px; min-width: 34px; }
.yaml-source-editor :deep(.cm-foldGutter .cm-gutterElement) { padding-inline: 3px; }.yaml-source-editor :deep(.yaml-fold-marker) { display: inline-flex; align-items: center; vertical-align: middle; cursor: pointer; }.yaml-source-editor :deep(.yaml-fold-placeholder) { display: inline-flex; align-items: center; margin-inline: 5px; padding: 2px 5px; border-radius: 4px; background: #353a42; color: #abb2bf; vertical-align: middle; }.yaml-source-editor :deep(.yaml-fold-placeholder:focus-visible) { outline: 1px solid #61afef; }
@media(max-width: 639px) { .yaml-source-editor :deep(.cm-editor), .yaml-fallback-input { height: 50dvh; font-size: 16px; }.yaml-source-editor :deep(.cm-line) { padding-inline: 8px; }.yaml-source-editor :deep(.cm-lineNumbers .cm-gutterElement) { padding-inline: 6px; } }
</style>
