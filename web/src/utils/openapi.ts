/** 极简 OpenAPI 3.0 解析工具（仅用于站内 API 文档渲染） */

export interface OASchema {
  $ref?: string
  type?: string
  format?: string
  description?: string
  enum?: unknown[]
  default?: unknown
  nullable?: boolean
  minLength?: number
  maximum?: number
  minimum?: number
  items?: OASchema
  properties?: Record<string, OASchema>
  required?: string[]
  allOf?: OASchema[]
  oneOf?: OASchema[]
  anyOf?: OASchema[]
  additionalProperties?: boolean | OASchema
}

export interface OAParameter {
  $ref?: string
  name: string
  in: string
  required?: boolean
  description?: string
  schema?: OASchema
}

export interface OAMediaType {
  schema?: OASchema
}

export interface OARequestBody {
  $ref?: string
  required?: boolean
  description?: string
  content?: Record<string, OAMediaType>
}

export interface OAResponse {
  $ref?: string
  description?: string
  content?: Record<string, OAMediaType>
}

export interface OAOperation {
  tags?: string[]
  summary?: string
  description?: string
  operationId?: string
  parameters?: OAParameter[]
  requestBody?: OARequestBody
  responses?: Record<string, OAResponse>
  security?: Record<string, string[]>[]
  deprecated?: boolean
}

export interface OADocument {
  openapi?: string
  info?: { title?: string; version?: string; description?: string }
  servers?: { url: string }[]
  security?: Record<string, string[]>[]
  tags?: { name: string; description?: string }[]
  paths?: Record<string, Record<string, OAOperation | OAParameter[] | undefined>>
  components?: Record<string, Record<string, unknown>>
}

export const HTTP_METHODS = ['get', 'post', 'put', 'patch', 'delete', 'head', 'options'] as const

export interface OpEntry {
  id: string
  method: string
  path: string
  op: OAOperation
  parameters: OAParameter[]
  isPublic: boolean
}

/** 解析 #/components/... 形式的本地 $ref */
export function resolveRef<T>(doc: OADocument, ref: string): T | undefined {
  if (!ref.startsWith('#/')) return undefined
  let cur: unknown = doc
  for (const raw of ref.slice(2).split('/')) {
    const key = raw.replace(/~1/g, '/').replace(/~0/g, '~')
    if (cur && typeof cur === 'object' && key in (cur as Record<string, unknown>)) {
      cur = (cur as Record<string, unknown>)[key]
    } else return undefined
  }
  return cur as T
}

export function deref<T extends { $ref?: string }>(doc: OADocument, v: T | undefined): T | undefined {
  let cur = v
  let guard = 0
  while (cur?.$ref && guard++ < 10) cur = resolveRef<T>(doc, cur.$ref)
  return cur
}

export function refName(ref: string): string {
  return ref.split('/').pop() ?? ref
}

export interface SchemaField {
  name: string
  type: string
  required: boolean
  description?: string
  enum?: unknown[]
  default?: unknown
  nullable?: boolean
  constraints?: string
  children: SchemaField[]
}

export interface SchemaNode {
  type: string
  description?: string
  children: SchemaField[]
  enum?: unknown[]
}

function constraintsOf(s: OASchema): string | undefined {
  const c: string[] = []
  if (s.minLength !== undefined) c.push(`最短 ${s.minLength}`)
  if (s.minimum !== undefined) c.push(`≥ ${s.minimum}`)
  if (s.maximum !== undefined) c.push(`≤ ${s.maximum}`)
  return c.length ? c.join('，') : undefined
}

/** 合并 allOf，并解析 $ref */
function flatten(doc: OADocument, s: OASchema | undefined, seen: Set<string>): { schema: OASchema; refLabel?: string } {
  if (!s) return { schema: {} }
  let refLabel: string | undefined
  let cur = s
  if (cur.$ref) {
    refLabel = refName(cur.$ref)
    if (seen.has(cur.$ref)) return { schema: { type: 'object', description: cur.description }, refLabel }
    const r = resolveRef<OASchema>(doc, cur.$ref)
    cur = { ...(r ?? {}), description: s.description ?? r?.description }
  }
  if (cur.allOf?.length) {
    const merged: OASchema = { type: 'object', properties: {}, required: [], description: cur.description }
    for (const part of cur.allOf) {
      const f = flatten(doc, part, seen).schema
      Object.assign(merged.properties!, f.properties ?? {})
      merged.required!.push(...(f.required ?? []))
      if (!merged.description && f.description) merged.description = f.description
    }
    cur = merged
  }
  return { schema: cur, refLabel }
}

function typeLabel(doc: OADocument, s: OASchema | undefined, seen: Set<string>): string {
  if (!s) return 'any'
  const { schema, refLabel } = flatten(doc, s, seen)
  if (schema.type === 'array') return `${typeLabel(doc, schema.items, seen)}[]`
  let t = refLabel ?? schema.type ?? (schema.properties ? 'object' : 'any')
  if (refLabel && schema.type && schema.type !== 'object') t = `${schema.type} (${refLabel})`
  if (!refLabel && schema.format) t += ` <${schema.format}>`
  return t
}

function fieldsOf(doc: OADocument, s: OASchema | undefined, seen: Set<string>, depth: number): SchemaField[] {
  if (!s || depth > 6) return []
  const nextSeen = new Set(seen)
  if (s.$ref) nextSeen.add(s.$ref)
  const { schema } = flatten(doc, s, seen)
  if (schema.type === 'array') return fieldsOf(doc, schema.items, nextSeen, depth)
  const props = schema.properties ?? {}
  const req = new Set(schema.required ?? [])
  return Object.entries(props).map(([name, p]) => {
    const { schema: ps } = flatten(doc, p, nextSeen)
    const itemSchema = ps.type === 'array' ? flatten(doc, ps.items, nextSeen).schema : undefined
    return {
      name,
      type: typeLabel(doc, p, nextSeen),
      required: req.has(name),
      description: ps.description,
      enum: ps.enum ?? itemSchema?.enum,
      default: ps.default,
      nullable: ps.nullable,
      constraints: constraintsOf(ps),
      children: p.$ref && nextSeen.has(p.$ref) ? [] : fieldsOf(doc, p, nextSeen, depth + 1),
    }
  })
}

export function buildSchemaNode(doc: OADocument, s: OASchema | undefined): SchemaNode {
  const seen = new Set<string>()
  const { schema } = flatten(doc, s, seen)
  return {
    type: typeLabel(doc, s, seen),
    description: schema.description,
    enum: schema.enum,
    children: fieldsOf(doc, s, seen, 0),
  }
}

export function collectOperations(doc: OADocument): OpEntry[] {
  const out: OpEntry[] = []
  for (const [path, item] of Object.entries(doc.paths ?? {})) {
    const shared = (item.parameters as OAParameter[] | undefined) ?? []
    for (const m of HTTP_METHODS) {
      const op = item[m] as OAOperation | undefined
      if (!op) continue
      const params = [...shared, ...(op.parameters ?? [])]
        .map((p) => deref(doc, p))
        .filter((p): p is OAParameter => !!p)
      const security = op.security ?? doc.security ?? []
      out.push({
        id: `${m}-${path}`,
        method: m.toUpperCase(),
        path,
        op,
        parameters: params,
        isPublic: security.length === 0 || security.every((s) => Object.keys(s).length === 0),
      })
    }
  }
  return out
}
