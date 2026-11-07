import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { detailSearchSettingsRequest } from '../lib/detail-search-api'
import { defaultDetailSearchOrder, type DetailSearchSourceID } from '../utils/detailSearchSources'

export function useSearchSettingsEditor(token: () => string) {
  const order = ref<DetailSearchSourceID[]>([...defaultDetailSearchOrder])
  const baseline = ref(''), loaded = ref(false), busy = ref(false), error = ref(''), notice = ref('')
  const dirty = computed(() => loaded.value && order.value.join(',') !== baseline.value)
  let controller: AbortController | undefined
  let generation = 0
  function apply(value: DetailSearchSourceID[]) {
    order.value = [...value]; baseline.value = value.join(','); loaded.value = true
  }
  async function load() {
    controller?.abort()
    controller = new AbortController()
    const request = ++generation, signal = controller.signal
    busy.value = true; loaded.value = false; error.value = ''; notice.value = ''
    try {
      const config = await detailSearchSettingsRequest(token(), signal)
      if (request === generation && !signal.aborted) apply(config.order)
    } catch (reason) {
      if (request === generation && !signal.aborted) error.value = reason instanceof Error ? reason.message : '读取搜索设置失败'
    } finally { if (request === generation) busy.value = false }
  }
  async function save() {
    if (busy.value || !loaded.value || !dirty.value || !controller) return
    const request = generation, signal = controller.signal, snapshot = [...order.value]
    busy.value = true; error.value = ''; notice.value = ''
    try {
      const config = await detailSearchSettingsRequest(token(), signal, snapshot)
      if (request === generation && !signal.aborted) { apply(config.order); notice.value = '搜索顺序已保存，所有设备的详情页共用此顺序。' }
    } catch (reason) {
      if (request === generation && !signal.aborted) error.value = reason instanceof Error ? reason.message : '保存失败，当前排序已保留，可重试'
    } finally { if (request === generation) busy.value = false }
  }
  function reset() {
    if (!loaded.value || busy.value) return
    order.value = [...defaultDetailSearchOrder]
  }
  watch(order, () => { notice.value = ''; error.value = '' }, { deep: true, flush: 'sync' })
  watch(token, () => { void load() }, { immediate: true })
  onBeforeUnmount(() => { generation++; controller?.abort() })
  return { order, loaded, busy, dirty, error, notice, load, save, reset }
}
