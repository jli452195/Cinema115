export interface TelegramRelayPeer {
  id: string
  title: string
  username?: string
  avatarId?: string
  kind: 'channel' | 'group' | 'bot'
  source: boolean
  target: boolean
}
export interface TelegramRelayPeerPage { peers: TelegramRelayPeer[]; selectionToken: string; nextCursor: string }
export interface TelegramRelayRule {
  id: string
  name: string
  sourceId: string
  sourceTitle: string
  targetId: string
  targetTitle: string
  enabled: boolean
  linkTypes: string[]
  includePattern: string
  excludePattern: string
}
export type TelegramRelayInput = TelegramRelayRule & { sourceSelectionToken?: string; targetSelectionToken?: string }
export interface TelegramRelaySettings {
  rules: TelegramRelayRule[]
  peers: TelegramRelayPeer[]
  status: { connected: boolean; error?: string }
  account: { hasSession: boolean; name: string; username?: string; avatarId?: string }
}
