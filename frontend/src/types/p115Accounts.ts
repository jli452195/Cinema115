export interface P115Account {
  pool?: { state: string; mode: string; sessions: number; preparing: number; active?: number; residual: number; retryAt: number; message?: string; checkedAt?: number }
	summary?: { vip: '' | 'vip' | 'normal'; spaceKnown: boolean; remainingBytes: number; checkedAt: string }
	loginClient?: string
  id: string; name: string; uid: string; primary: boolean; configured: boolean; revision: number; playbackParentCid?: string }
