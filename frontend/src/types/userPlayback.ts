export interface UserPlaybackTarget {
 accountId?: string
 accountName?: string
 source?: string
 issuedAt?: number
}
export interface UserPlaybackSession {
 key: string
 userId: string
 serverId?: string
 item: string
 generation: number
 issued: boolean
 updatedAt: number
 expiresAt: number
 target?: UserPlaybackTarget
}
export interface PlaybackSessionsSnapshot {
 sessions: UserPlaybackSession[]
 loaded: boolean
 stale: boolean
}
export interface PlaybackCardSummary {
 label: string
 name?: string
 suffix?: string
 detail: string
}
export function playbackTargetName(target: UserPlaybackTarget) {
 return target.accountName?.trim() || target.accountId || '配置名称待核实'
}
export function playbackTargetSource(source?: string) {
 return ({ primary: '主网盘', assigned: '动态共享池', personal: '个人网盘', personal_shared: '所有者个人网盘' })[source || ''] || '来源待核实'
}
