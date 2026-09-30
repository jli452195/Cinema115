export interface MemberPlayback {
 imageTag?: string
 pendingAccountId?: string; pendingDirectoryStarted?: boolean
	allowPersonalSwitch?: boolean
  userId: string; username: string; disabled: boolean; administrator?: boolean; embyId: string; embyName: string; embyUrl: string
  source: string; accountId: string; assignedAccountId: string; policyEnabled: boolean; stage: string; message: string
  adminBlocked: boolean; skipLibrarySync?: boolean; directoryStarted: boolean; directoryId: string; appliedRevision: number; syncError: string
  personalDrive?: { uid: string; connected: boolean; directoryId: string }
}
export interface MemberSettings { enabled: boolean; revision: number; libraryIds: string[]; allowDownload: boolean }
export async function memberRequest(token: string, signal: AbortSignal, path = '', body?: object) {
  const response = await fetch(`/api/admin/member-playback${path}`, { method: body ? 'POST' : 'GET', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, signal: AbortSignal.any([signal, AbortSignal.timeout(95000)]), body: body ? JSON.stringify(body) : undefined })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(data?.error || '播放管理操作失败')
  return data
}
export function memberStage(member: MemberPlayback, settings: MemberSettings) {
  if (!member.source) return '等待播放授权'
  if (member.disabled) return 'Emby 已停用'
  if (member.adminBlocked) return '播放已停用'
  if (!settings.enabled) return '自助播放已关闭'
  if (member.syncError) return '权限同步失败'
  if (member.embyId && member.appliedRevision !== settings.revision) return '权限待同步'
  if (member.stage === 'ready' && !member.policyEnabled) return '播放待开通'
  return ({ ready: '播放已开通', pending: '播放待开通', unbound: '待注册 Emby', bound: '待注册 Emby', creating: '注册待确认', provisioning: '正在配置', paused: '播放已暂停' } as Record<string, string>)[member.stage] || '状态待确认'
}
