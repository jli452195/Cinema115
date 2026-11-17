import { p115Request } from './p115-api'
import type { ED2KSTRMImportResult, ED2KSTRMResource, ED2KSTRMSettings, ED2KSTRMState } from '../types/ed2kSTRM'

export function createED2KSTRMAPI(token: () => string, signal: AbortSignal) {
  const request = <T,>(path = '', init: RequestInit = {}) => p115Request<T>(token(), 'ed2k-strm' + path, {
    ...init,
    signal: AbortSignal.any([signal, AbortSignal.timeout(60000)]),
  })
  return {
    state: () => request<ED2KSTRMState>(),
    saveSettings: (settings: ED2KSTRMSettings) => request<ED2KSTRMSettings>('/settings', { method: 'PUT', body: JSON.stringify(settings) }),
    imports: (links: string[]) => request<ED2KSTRMImportResult>('/imports', { method: 'POST', body: JSON.stringify({ links }) }),
    resource: (resource: ED2KSTRMResource, action: 'check' | 'restore') => request(`/resources/${encodeURIComponent(resource.hash)}/${action}`, {
      method: 'POST', body: JSON.stringify({ size: resource.size }),
    }),
  }
}
