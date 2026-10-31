export type ClassificationKind = 'movie' | 'tv'
export type ClassificationCondition = 'genres' | 'countries' | 'languages'
export interface ClassificationRule {
  key: string
  name: string
  match: Partial<Record<ClassificationCondition, (string | number)[]>>
}
export interface ClassificationSection { fallback: string; rules: ClassificationRule[] }
export type ClassificationConfig = Record<ClassificationKind, ClassificationSection>
export interface ClassificationDiagnostic { field: string; line: number; column: number; message: string }
export interface ClassificationRecord { yaml: string; revision: string; activeRevision: string; loadError?: string; diagnostic?: ClassificationDiagnostic }
export interface ClassificationSettings { record: ClassificationRecord; defaultYAML: string }
