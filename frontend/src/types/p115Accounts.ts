export interface P115Account {
	summary?: { vip: '' | 'vip' | 'normal'; spaceKnown: boolean; remainingBytes: number; checkedAt: string }
	loginClient?: string
  userCount?: number; users?: string[]; id: string; name: string; uid: string; primary: boolean; configured: boolean; revision: number; playbackParentCid?: string }
