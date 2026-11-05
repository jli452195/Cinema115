import { p115Request } from './p115-api'

export function createDriveSTRMAPI(token: () => string, signal: AbortSignal) {
  const requestSignal = () => AbortSignal.any([signal, AbortSignal.timeout(60000)])
  const request = <T,>(path = '', init: RequestInit = {}) => p115Request<T>(token(), 'drive-strm' + path, { ...init, signal: requestSignal() })
  const accounts = () => p115Request<{ accounts: { id: string; primary: boolean; configured: boolean }[] }>(token(), 'accounts', { signal: requestSignal() })
  return { request, accounts }
}
