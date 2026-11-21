<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { IconCheck, IconChevronDown } from '@tabler/icons-vue'
import { memberRequest } from '../types/memberPlayback'
import { useSlidingIndicator } from '../composables/useSlidingIndicator'

const props = defineProps<{ token: string }>()
const policy = ref({ mode: 'parallel', silenceSeconds: 60 })
const saved = ref({ ...policy.value })
const loaded = ref(false), busy = ref(false), error = ref(''), notice = ref('')
const lifecycle = new AbortController()
const dirty = computed(() => loaded.value && JSON.stringify(policy.value) !== JSON.stringify(saved.value))
const silenceId = useId(), silenceHelpId = useId()
const choices = ref<HTMLElement>()
const { indicatorStyle } = useSlidingIndicator(choices, '.is-selected', [() => policy.value.mode], { durationMs: 260 })
async function load() {
  if (busy.value) return
  busy.value = true; error.value = ''
  try {
    const data = await memberRequest(props.token, lifecycle.signal, '/device-policy')
    policy.value = { mode: 'parallel', silenceSeconds: 60, ...data }
    saved.value = { ...policy.value }; loaded.value = true
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取设备策略失败' }
  finally { busy.value = false }
}
async function save() {
  if (!loaded.value || busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    policy.value = await memberRequest(props.token, lifecycle.signal, '/device-policy', policy.value)
    saved.value = { ...policy.value }; notice.value = '设备策略已保存，对后续取链请求生效。'
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '保存设备策略失败' }
  finally { busy.value = false }
}
function restore() {
  if (!dirty.value || busy.value) return
  policy.value = { ...saved.value }
  error.value = ''
  notice.value = ''
}
defineExpose({ dirty })
onMounted(load); onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <section class="proxy-card" aria-labelledby="proxy-device-title">
    <header class="proxy-card-heading">
      <h2 id="proxy-device-title" class="proxy-card-title">播放设备策略</h2>
      <span class="proxy-form-status" :data-tone="dirty ? 'warning' : undefined">
        <IconCheck v-if="loaded && !dirty" :size="14" aria-hidden="true" />
        {{ dirty ? '待保存' : loaded ? '已同步' : error ? '读取失败' : '读取中…' }}
      </span>
    </header>
    <form class="proxy-form" @submit.prevent="save">
      <div class="proxy-card-body">
        <p v-if="error" role="alert" class="proxy-alert is-error">
          {{ error }}
          <button v-if="!loaded" type="button" :disabled="busy" class="underline" @click="load">重试读取</button>
        </p>
        <fieldset :disabled="!loaded || busy" class="proxy-fields">
          <legend class="sr-only">同一 Emby 用户的设备使用方式</legend>
          <div ref="choices" class="proxy-segment">
            <span class="proxy-segment-indicator" :style="indicatorStyle" aria-hidden="true"></span>
            <label :class="{ 'is-selected': policy.mode === 'parallel' }"><input v-model="policy.mode" name="playback-device-policy" type="radio" value="parallel" />多设备并行</label>
            <label :class="{ 'is-selected': policy.mode === 'heartbeat_takeover' }"><input v-model="policy.mode" name="playback-device-policy" type="radio" value="heartbeat_takeover" />单设备心跳熔断</label>
          </div>
          <p v-if="policy.mode === 'parallel'" class="proxy-help">同一用户可在多个设备播放，各设备独立使用播放池名额。</p>
          <template v-else-if="policy.mode === 'heartbeat_takeover'">
            <div class="proxy-field">
              <label :for="silenceId" class="proxy-field-label">心跳静默时间（秒）</label>
              <input :id="silenceId" v-model.number="policy.silenceSeconds" type="number" min="30" max="180" step="1" required :aria-describedby="silenceHelpId" class="proxy-input proxy-input-compact tabular-nums" />
              <p :id="silenceHelpId" class="proxy-help">原设备持续上报时拒绝其他设备；静默超时后，新设备成功授权才接替。默认 60 秒，可设为 30 至 180 秒。暂停仍上报时继续占位，同设备切集与预加载可复用。</p>
            </div>
            <details class="proxy-details">
              <summary class="proxy-summary">
                <span>查看接替规则</span>
                <IconChevronDown :size="18" class="proxy-chevron" aria-hidden="true" />
              </summary>
              <div class="proxy-details-body">
                <p class="proxy-help">优先沿用原设备健康小号并借用原名额，正常停止后的名额也可继承；成功后事务转交，旧会话不再占用容量。跨 UID 时新小号仍需空位，失败保留原设备。缺少可信设备信息必须通过反代重新登录。切换策略不会主动停止已有播放，已签发直链不能撤回。逻辑名额不代表真实视频连接数。</p>
              </div>
            </details>
          </template>
        </fieldset>
      </div>
      <footer class="proxy-card-footer">
        <p v-if="notice && !dirty" role="status" class="proxy-inline-success">{{ notice }}</p>
        <div class="proxy-footer-actions">
          <button v-if="dirty" type="button" :disabled="busy" class="proxy-secondary-button" @click="restore">还原修改</button>
          <button type="submit" :disabled="!loaded || busy" class="proxy-button">{{ busy ? '正在保存…' : '保存设备策略' }}</button>
        </div>
      </footer>
    </form>
  </section>
</template>
