import { p115Request } from './p115-api'

export interface ShareTransferTarget {
  id: string
  name: string
  accountId: string
  targetCID: string
  targetPath: string
}
export interface ShareTransferLink { url: string; password?: string }
export interface ShareTransferResource { load: () => Promise<ShareTransferLink[]> }

export function shareTransferTargets(token: string, signal: AbortSignal) {
  return p115Request<{ accountId: string; profiles: ShareTransferTarget[] }>(token, 'share-transfer/targets', { signal })
}

export function transferShare(token: string, target: ShareTransferTarget, link: ShareTransferLink, signal: AbortSignal) {
  return p115Request<{ receipt: { targetPath: string; itemCount: number; acceptedItems: number } }>(token, 'share-transfer', {
    method: 'POST', body: JSON.stringify({ target, ...link }), signal,
  })
}
