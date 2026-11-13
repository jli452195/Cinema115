import { shallowRef } from 'vue'
import { isPortalAdmin, portalToken } from './usePortalAuth'
import type { NetworkProxyConfig, NetworkProxyInput } from '../lib/network-proxy-api'

export interface NetworkConnectionTestRequest {
  token: string
  input?: NetworkProxyInput
  services?: NetworkProxyConfig['availableServices']
}

export const networkConnectionTest = shallowRef<NetworkConnectionTestRequest>()

// 表单只在本次测试期间保留快照，关闭即释放，不持久化代理凭据。
export function openNetworkConnectionTest(input?: NetworkProxyInput, services?: NetworkProxyConfig['availableServices']) {
  if (!isPortalAdmin.value || !portalToken.value || networkConnectionTest.value) return
  networkConnectionTest.value = {
    token: portalToken.value,
    input: input ? { ...input, services: [...input.services] } : undefined,
    services: services?.map(service => ({ ...service })),
  }
}

export function closeNetworkConnectionTest() {
  networkConnectionTest.value = undefined
}
