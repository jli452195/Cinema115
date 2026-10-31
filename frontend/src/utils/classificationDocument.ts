import { isMap, isNode, isScalar, isSeq, LineCounter, parseDocument, type Document, type YAMLMap } from 'yaml'
import type { ClassificationCondition, ClassificationConfig, ClassificationDiagnostic, ClassificationKind, ClassificationRule } from '../types/classification'

export const classificationConditions: { key: ClassificationCondition; label: string }[] = [
  { key: 'genres', label: '类型' }, { key: 'countries', label: '国家/地区' }, { key: 'languages', label: '原始语言' },
]
export interface ClassificationEditorDocument {
  source: string
  document: Document
  config: ClassificationConfig
  nodes: Map<string, YAMLMap>
  lineCounter: LineCounter
}

function problem(lineCounter: LineCounter, node: unknown, field: string, message: string, offset?: number): never {
  const position = lineCounter.linePos(offset ?? (isNode(node) ? node.range?.[0] ?? 0 : 0))
  const diagnostic: ClassificationDiagnostic = { field, line: Math.max(1, position.line), column: Math.max(1, position.col), message }
  throw Object.assign(new Error(message), { diagnostic })
}

// Go's YAML decoder accepts legacy octal/binary/underscore notation. Resolve the
// same integer value rather than treating 016 as decimal 16 or a string.
function integerValue(node: unknown): number | undefined {
  if (!isScalar(node) || node.anchor || node.tag && node.tag !== 'tag:yaml.org,2002:int' || node.tag === undefined && node.type !== 'PLAIN') return
  const text = node.source?.replace(/_/g, '').replace(/^\+/, '')
  if (!text || !/^(?:0[xX][\da-fA-F]+|0[oO][0-7]+|0[bB][01]+|0[0-7]*|[1-9]\d*)$/.test(text)) return
  const normalized = /^0[0-7]+$/.test(text) ? `0o${text.slice(1)}` : text
  try {
    const value = BigInt(normalized)
    if (value > 0 && value <= BigInt(Number.MAX_SAFE_INTEGER)) return Number(value)
  } catch { /* Invalid integer notation is reported at the caller's field. */ }
}

function readDocument(source: string) {
  const lineCounter = new LineCounter()
  if (new TextEncoder().encode(source).length > 256 * 1024) problem(lineCounter, undefined, '$', '分类 YAML 不能超过 256 KiB')
  const document = parseDocument(source, { uniqueKeys: true, strict: true, lineCounter })
  const syntaxError = document.errors[0]
  if (syntaxError) problem(lineCounter, undefined, '$', syntaxError.message, syntaxError.pos[0])
  const fail = (node: unknown, field: string, message: string): never => problem(lineCounter, node, field, message)
  const mapping = (node: unknown, field: string, keys: string[] | undefined, required: string[] = []) => {
    if (!isMap(node) || node.anchor || node.tag && node.tag !== 'tag:yaml.org,2002:map') return fail(node, field, '分类配置须使用普通 YAML 映射，不支持锚点或自定义标签')
    for (const pair of node.items) {
      if (!isScalar(pair.key) || pair.key.anchor || pair.key.tag && pair.key.tag !== 'tag:yaml.org,2002:str' || typeof pair.key.value !== 'string') fail(pair.key, field, '分类字段名须为普通文本')
      const key = String((pair.key as { value: string }).value)
      if (keys && !keys.includes(key)) fail(pair.key, `${field}.${key}`, '分类配置包含不支持的字段')
    }
    for (const key of required) if (!node.has(key)) fail(node, `${field}.${key}`, `分类配置缺少 ${key}`)
    return node
  }
  const text = (node: unknown, field: string): string => {
    if (!isScalar(node) || node.anchor || node.tag && node.tag !== 'tag:yaml.org,2002:str' || typeof node.value !== 'string') return fail(node, field, '分类名称与目录须为普通文本')
    return node.value
  }
  const list = (node: unknown, field: string) => {
    if (!isSeq(node) || node.anchor || node.tag && node.tag !== 'tag:yaml.org,2002:seq') return fail(node, field, '分类规则与条件须为普通列表')
    return node
  }
  const checkWarnings = () => {
    const warning = document.warnings[0]
    if (warning) problem(lineCounter, undefined, '$', warning.message, warning.pos[0])
  }
  return { document, lineCounter, fail, mapping, text, list, checkWarnings }
}

// Keep the source and editable projection together. Semantic validation is
// separate so incomplete visual edits can still be represented without loss.
export function parseClassification(source: string): ClassificationEditorDocument {
  const { document, lineCounter, fail, mapping, text, list, checkWarnings } = readDocument(source)
  const root = mapping(document.contents, '$', ['movie', 'tv'], ['movie', 'tv'])
  const config = {} as ClassificationConfig, nodes = new Map<string, YAMLMap>()
  for (const kind of ['movie', 'tv'] as const) {
    const section = mapping(root.get(kind, true), kind, ['fallback', 'rules'], ['fallback', 'rules'])
    const sequence = list(section.get('rules', true), `${kind}.rules`)
    config[kind] = { fallback: text(section.get('fallback', true), `${kind}.fallback`), rules: sequence.items.map((item, index) => {
      const field = `${kind}.rules[${index}]`
      const rule = mapping(item, field, ['name', 'match'], ['name', 'match'])
      const match = mapping(rule.get('match', true), `${field}.match`, classificationConditions.map(item => item.key), [])
      const parsed: ClassificationRule = { key: `${kind}-${index}`, name: text(rule.get('name', true), `${field}.name`).trim(), match: {} }
      for (const { key } of classificationConditions) {
        if (!match.has(key)) continue
        const valueField = `${field}.match.${key}`, values = list(match.get(key, true), valueField)
        const seen = new Set<string | number>()
        parsed.match[key] = values.items.map((node, valueIndex) => {
          const itemField = `${valueField}[${valueIndex}]`
          let value: string | number
          if (key === 'genres') {
            const integer = integerValue(node)
            if (integer === undefined) return fail(node, itemField, '类型 ID 须为有效正整数')
            value = integer
          } else {
            const code = text(node, itemField).trim()
            if (!/^[a-z]{2}$/i.test(code)) return fail(node, itemField, '国家和语言须为两位字母代码')
            value = key === 'languages' ? code.toLowerCase() : code.toUpperCase()
          }
          if (seen.has(value)) fail(node, itemField, '同一条件不能包含重复值')
          seen.add(value)
          return value
        })
      }
      nodes.set(parsed.key, rule)
      return parsed
    }) }
  }
  checkWarnings()
  return { source, document, config, nodes, lineCounter }
}

export const classificationDirectory = (kind: ClassificationKind, name: string) => `${kind === 'movie' ? '电影' : '剧集'}/${name.trim()}`

export function classificationSignature(config: ClassificationConfig) {
  return JSON.stringify(['movie', 'tv'].map(kind => {
    const section = config[kind as ClassificationKind]
    return [section.fallback, section.rules.map(rule => [rule.name, classificationConditions.map(({ key }) => rule.match[key])])]
  }))
}

function directoryError(value: string): boolean {
  return !value || value.split('/').some(part => {
    const base = part.split('.')[0]!.trimEnd()
    return !part || part !== part.trim() || part.startsWith('.') || part.endsWith('.')
      || /[\\:*?"<>|\u0000-\u001f\u007f-\u009f]/.test(part)
      || /^(?:CON|PRN|AUX|NUL|CONIN\$|CONOUT\$|(?:COM|LPT)[1-9¹²³])$/i.test(base)
      || new TextEncoder().encode(part).length > 240
  })
}

export function classificationIssue(config: ClassificationConfig): { kind: ClassificationKind; key?: string; field: string; message: string } | undefined {
  for (const kind of ['movie', 'tv'] as const) {
    if (directoryError(config[kind].fallback)) return { kind, field: `${kind}.fallback`, message: `请填写有效的兜底分类名称，例如 ${kind === 'movie' ? '其他电影' : '其他剧集'}` }
    const names = new Set<string>()
    for (const [index, rule] of config[kind].rules.entries()) {
      const name = rule.name.trim(), prefix = `${kind}.rules[${index}]`
      const emptyCondition = classificationConditions.find(({ key }) => rule.match[key] && !rule.match[key]!.length)
      const issue = !name ? { field: `${prefix}.name`, message: '请填写分类名称' }
        : names.has(name) ? { field: `${prefix}.name`, message: '当前类型存在同名分类' }
        : directoryError(name) ? { field: `${prefix}.name`, message: '分类名称须为有效相对目录，不能包含绝对路径、上级目录或非法目录段' }
        : !Object.keys(rule.match).length ? { field: `${prefix}.match`, message: '至少选择一项匹配条件' }
        : emptyCondition ? { field: `${prefix}.match.${emptyCondition.key}`, message: '每项条件至少选择一个匹配值，或移除该条件' }
        : undefined
      if (issue) return { kind, key: rule.key, ...issue }
      names.add(name)
    }
  }
}

// Source mode uses the same semantic checks as the visual form, now positioned
// against the exact parsed document rather than generated or saved YAML.
export function validateClassification(editor: ClassificationEditorDocument): void {
  const issue = classificationIssue(editor.config)
  if (!issue) return
  const path = issue.field.replace(/\[(\d+)\]/g, '.$1').split('.').map(part => /^\d+$/.test(part) ? Number(part) : part)
  let node: unknown = editor.document.getIn(path, true)
  while (!isNode(node) && path.length) { path.pop(); node = editor.document.getIn(path, true) }
  problem(editor.lineCounter, node, issue.field, issue.message)
}

function conditionList(document: Document, values: (string | number)[]) {
  const node = document.createNode(values)
  node.flow = true
  return node
}

export function writeClassification(editor: ClassificationEditorDocument, config: ClassificationConfig): string {
  if (classificationSignature(editor.config) === classificationSignature(config)) return editor.source
  const document = editor.document.clone()
  const setText = (node: YAMLMap, key: string, value: string) => {
    const scalar = node.get(key, true)
    if (isScalar(scalar)) scalar.value = value
    else node.set(key, value)
  }
  for (const kind of ['movie', 'tv'] as const) {
    const section = document.get(kind, true) as YAMLMap
    setText(section, 'fallback', config[kind].fallback)
    const sequence = section.get('rules', true)
    if (!isSeq(sequence)) throw new Error('分类规则列表无效')
    sequence.items = config[kind].rules.map(rule => {
      const node = (editor.nodes.get(rule.key)?.clone() || document.createNode({ name: '', match: {} })) as YAMLMap
      setText(node, 'name', rule.name.trim())
      const match = node.get('match', true)
      if (!isMap(match)) throw new Error('分类匹配条件无效')
      for (const { key } of classificationConditions) {
        const values = rule.match[key]
        if (values === undefined) { match.delete(key); continue }
        const previous = match.get(key, true)
        if (isSeq(previous)) {
          previous.items = values.map(value => {
            const retained = previous.items.find(item => isScalar(item) && (key === 'genres' ? integerValue(item) === value : String(item.value).trim().toLowerCase() === String(value).toLowerCase()))
            if (isScalar(retained)) { if (key === 'genres') retained.value = value; return retained }
            return document.createNode(value)
          })
          previous.flow = true
        }
        else match.set(key, conditionList(document, values))
      }
      return node
    })
  }
  return document.toString({ flowCollectionPadding: false })
}

// Import once into the editable native document; runtime only reads that format.
export function importMoviePilotClassification(source: string): string {
  const { document, fail, mapping, text, list, checkWarnings } = readDocument(source)
  const root = mapping(document.contents, '$', ['movie', 'tv'], ['movie', 'tv'])
  root.flow = false
  const fields: Record<string, ClassificationCondition> = { genre_ids: 'genres', original_language: 'languages', production_countries: 'countries', origin_country: 'countries' }
  const nativeComment = (value: string) => value.replace(/\b(?:genre_ids|original_language|production_countries|origin_country)\b/g, key => fields[key]!)
  const comments = (from: unknown, to: unknown, before: unknown = to) => {
    if (!isNode(from) || !isNode(to) || !isNode(before)) return
    if (from.commentBefore) before.commentBefore = [before.commentBefore, nativeComment(from.commentBefore)].filter(Boolean).join('\n')
    if (from.comment) to.comment = [to.comment, nativeComment(from.comment)].filter(Boolean).join('\n')
    if (from.spaceBefore) before.spaceBefore = true
  }
  for (const kind of ['movie', 'tv'] as const) {
    const categories = mapping(root.get(kind, true), kind, undefined)
    const section = document.createNode({ fallback: '', rules: [] }) as YAMLMap
    const rules = list(section.get('rules', true), `${kind}.rules`)
    const names = new Set<string>()
    let fallback = false
    for (const [index, category] of categories.items.entries()) {
      const name = text(category.key, kind).trim(), field = `${kind}.${name}`
      if (directoryError(name)) fail(category.key, field, '分类名称须为有效相对目录')
      if (names.has(name)) fail(category.key, field, '当前类型存在同名分类')
      names.add(name)
      const value = category.value
      const empty = value === null || isScalar(value) && value.value === null && !value.anchor && (!value.tag || value.tag === 'tag:yaml.org,2002:null')
      const conditions = empty ? undefined : mapping(value, field, ['genre_ids', 'original_language', kind === 'movie' ? 'production_countries' : 'origin_country'])
      if (!conditions?.items.length) {
        if (fallback) fail(category.key, field, '每种作品只支持一个兜底分类')
        if (index !== categories.items.length - 1) fail(category.key, field, '兜底分类须放在末尾')
        fallback = true
        const node = document.createNode(name)
        section.set('fallback', node)
        const key = section.items[0]!.key
        comments(category.key, node, key); comments(value, node, key)
        continue
      }
      const rule = document.createNode({ name, match: {} }) as YAMLMap
      comments(category.key, rule); comments(value, rule)
      const match = mapping(rule.get('match', true), field, [])
      for (const condition of conditions.items) {
        const key = String((condition.key as { value: string }).value), itemField = `${field}.${key}`
        const target = fields[key]!
        const seen = new Set<string | number>()
        const values = text(condition.value, itemField).split(',').map(raw => {
          const value = raw.trim()
          let normalized: string | number
          if (target === 'genres') {
            if (!/^[1-9]\d*$/.test(value) || !Number.isSafeInteger(Number(value))) fail(condition.value, itemField, '类型 ID 须为逗号分隔的正整数')
            normalized = Number(value)
          } else {
            if (!/^[a-z]{2}$/i.test(value)) fail(condition.value, itemField, '国家和语言须为逗号分隔的两位字母代码')
            normalized = target === 'languages' ? value.toLowerCase() : value.toUpperCase()
          }
          if (seen.has(normalized)) fail(condition.value, itemField, '同一条件不能包含重复值')
          seen.add(normalized)
          return normalized
        })
        const node = conditionList(document, values)
        const nativeKey = document.createNode(target)
        match.add({ key: nativeKey, value: node })
        comments(condition.key, node, nativeKey); comments(condition.value, node, nativeKey)
      }
      rules.items.push(rule)
    }
    if (!fallback) fail(categories, kind, '请在末尾提供一个空分类作为兜底分类')
    comments(categories, section)
    document.set(kind, section)
  }
  checkWarnings()
  const yaml = document.toString({ lineWidth: 0, flowCollectionPadding: false })
  validateClassification(parseClassification(yaml))
  return yaml
}
