import { p115Request } from './p115-api'
import type { ED2KSTRMImportResult, ED2KSTRMResource, ED2KSTRMSettings, ED2KSTRMState, ED2KSTRMTarget } from '../types/ed2kSTRM'

export function createED2KSTRMAPI(token: () => string, signal: AbortSignal) {
  const request = <T,>(path = '', init: RequestInit = {}) => p115Request<T>(token(), 'ed2k-strm' + path, {
    ...init,
    signal: AbortSignal.any([signal, AbortSignal.timeout(60000)]),
  })
  return {
    state: () => request<ED2KSTRMState>(),
    saveSettings: (settings: ED2KSTRMSettings) => request<ED2KSTRMSettings>('/settings', { method: 'PUT', body: JSON.stringify(settings) }),
    saveTarget: (target: Pick<ED2KSTRMTarget, 'name' | 'outputDir'>, id?: string) => request<ED2KSTRMTarget>(id ? `/targets/${encodeURIComponent(id)}` : '/targets', { method: id ? 'PUT' : 'POST', body: JSON.stringify(target) }),
    deleteTarget: (id: string) => request<void>(`/targets/${encodeURIComponent(id)}`, { method: 'DELETE' }),
    imports: (input: { targetId: string; requestId: string; links: string[] }) => request<ED2KSTRMImportResult>('/imports', { method: 'POST', body: JSON.stringify(input) }),
    retryTask: (id: string) => request(`/tasks/${encodeURIComponent(id)}/retry`, { method: 'POST' }),
    deleteTask: (id: string) => request<void>(`/tasks/${encodeURIComponent(id)}`, { method: 'DELETE' }),
    resumeCredentials: () => request<{ resumed: number }>('/resources/resume-credentials', { method: 'POST' }),
    resource: (resource: ED2KSTRMResource, action: 'check' | 'restore') => request(`/resources/${encodeURIComponent(resource.hash)}/${action}`, {
      method: 'POST', body: JSON.stringify({ size: resource.size }),
    }),
  }
}
