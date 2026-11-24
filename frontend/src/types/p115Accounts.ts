export interface P115Account {
  pool?: { state: string; mode: string; sessions: number; preparing: number; active?: number; residual: number; retryAt: number; message?: string; checkedAt?: number }
	summary?: { vip: '' | 'vip' | 'normal'; spaceKnown: boolean; remainingBytes: number; checkedAt: string }
	loginClient?: string
  id: string; name: string; uid: string; primary: boolean; configured: boolean; revision: number; playbackParentCid?: string }

export interface P115Membership {
  vip?: 'vip' | 'normal'
  vipCheckedAt?: string
}

export interface PersonalDriveStatus extends P115Membership {
  userId: string
  username: string
  binding: string
  driveStatus: { state: string; message: string; checkedAt?: string }
  issuedLinks: number
}

export function p115MembershipLabel(vip: P115Membership['vip']) {
  if (vip === 'vip') return '115 VIP'
  if (vip === 'normal') return '非 VIP'
  return '会员状态未知'
}
