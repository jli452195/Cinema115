export type MemberEmbyState = 'active' | 'disabled' | 'missing' | 'deleted' | 'other_server' | 'unknown'
export function memberEmbyLabel(state?: MemberEmbyState) {
 return ({ active: '正常', disabled: 'Emby 已停用', missing: 'Emby 中未找到，待核实', deleted: 'Emby 已删除', other_server: '属于其他 Emby 服务', unknown: 'Emby 状态暂不可用' })[state || 'unknown']
}
export function memberNeedsReview(member: MemberPlayback) {
 return ['missing', 'deleted', 'other_server'].includes(member.embyState || '')
}
export interface MemberPlayback {
 embyState?: MemberEmbyState; embyCheckedAt?: string
	childUsed?: number
 playbackOnly?: boolean; ownerUserId?: string; ownerName?: string; childLimit?: number; childPaused?: boolean; childDisabled?: boolean; childRevoked?: boolean
 imageTag?: string
 pendingAccountId?: string; pendingDirectoryStarted?: boolean
	allowPersonalSwitch?: boolean
  userId: string; username: string; disabled: boolean; administrator?: boolean; embyId: string; embyName: string; embyUrl: string
  source: string; accountId: string; playbackAllowed?: boolean; downloadAllowed?: boolean; stage: string; message: string
  driveStatus?: { state: string; message: string; checkedAt?: string }
  createdByCinema?: boolean; directoryStarted: boolean; directoryId: string; suspended?: boolean; checkedAt?: string; checkError?: string
  personalDrive?: { uid: string; connected: boolean; directoryId: string }
}
export interface MemberSettings { enabled: boolean; libraryIds: string[]; allowDownload: boolean }
export async function memberRequest(token: string, signal: AbortSignal, path = '', body?: object) {
  const response = await fetch(`/api/admin/member-playback${path}`, { method: body ? 'POST' : 'GET', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, signal: AbortSignal.any([signal, AbortSignal.timeout(95000)]), body: body ? JSON.stringify(body) : undefined })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(data?.error || '播放管理操作失败')
  return data
}
export function memberStage(member: MemberPlayback) {
 if (member.childRevoked) return '共享已撤销'
 if (member.childDisabled) return '子账号已停用'
 if (member.childPaused) return '共享已暂停'
 if (member.suspended) return '个人网盘已解绑'
 if (member.source === 'personal' && !member.personalDrive?.connected) return '个人网盘未绑定'
 if (member.checkError) return '检查未通过'
 if (!member.checkedAt) return '尚未检查'
 return member.stage === 'ready' ? '最近检查通过' : '网盘待就绪'
}
