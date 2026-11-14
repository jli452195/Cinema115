export type RecognitionKind = 'groups' | 'platforms'
export interface RecognitionTexts { groups: string; platforms: string }
export interface RecognitionDiagnostic { kind: RecognitionKind; line: number; column: number; message: string }
export interface RecognitionRecord extends RecognitionTexts {
  revision: string
  activeRevision: string
  loadError?: string
  diagnostic?: RecognitionDiagnostic
}
export interface RecognitionSettings { record: RecognitionRecord }
export interface RecognitionMatch { kind: RecognitionKind; line: number; raw: string; value: string; start: number; end: number }
export interface RecognitionConflict { kind: RecognitionKind; raw: string; lines: number[]; values?: string[]; message?: string }
export interface RecognitionItem {
  name: string
  releaseGroups: string[]
  platforms: string[]
  matches: RecognitionMatch[]
  conflicts: RecognitionConflict[]
}
export interface RecognitionPreview { items: RecognitionItem[] }
