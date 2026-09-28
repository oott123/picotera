/**
 * Tree model behind `JsonViewer`.
 *
 * The viewer never renders a component tree: it flattens the *currently visible*
 * nodes into one array and hands that to a virtualizer, so the work is bounded by
 * the number of expanded nodes and the rows on screen, never by the payload size.
 * A collapsed container costs exactly one row no matter how large its subtree is,
 * and a string's preview is escaped from a bounded slice rather than the whole
 * value, so a multi-megabyte string never gets stringified to fill one row.
 */

export type JsonKind = 'object' | 'array' | 'string' | 'number' | 'boolean' | 'null'

/** A row in the flattened list. `path` doubles as the identity key. */
export interface JsonRow {
  /** Stable display path, e.g. `$.messages[0].role`. Also the virtualizer key. */
  path: string
  key: string | null
  /** 0-based array index, `null` for object members and the root. */
  index: number | null
  depth: number
  kind: JsonKind
  /** The raw value — held by reference, never a copy. */
  value: unknown
  /** Object/array member count; string length for strings; 0 otherwise. */
  size: number
  /** One-line preview: escaped string body (truncated), primitive text, or a
   *  container placeholder. Precomputed during the walk so render is free. */
  preview: string
  /** True when `preview` was cut short by the preview limit. */
  truncated: boolean
  expandable: boolean
  expanded: boolean
}

const IDENT_RE = /^[A-Za-z_$][A-Za-z0-9_$]*$/

/** Characters shown before a string preview is cut. */
export const PREVIEW_LIMIT = 160

/** The cut in soft-wrap mode: a wrapped row has room for more than one line,
 *  but still stays a few lines tall rather than a screenful. */
export const WRAP_PREVIEW_LIMIT = 1000

/** Rows the walk will produce before it gives up, so a pathological payload
 *  cannot freeze the tab. Reported to the user through `limited`. */
export const ROW_BUDGET = 20000

/** Nodes `searchPaths` will visit before giving up. */
export const SEARCH_BUDGET = 300000

export function kindOf(value: unknown): JsonKind {
  if (value === null || value === undefined) return 'null'
  if (Array.isArray(value)) return 'array'
  switch (typeof value) {
    case 'object':
      return 'object'
    case 'number':
      return 'number'
    case 'boolean':
      return 'boolean'
    default:
      return 'string'
  }
}

export function isContainer(kind: JsonKind): boolean {
  return kind === 'object' || kind === 'array'
}

/** `$.a.b`, `$["odd key"]`, `$.list[3]` — a JS/JSONPath-flavoured accessor. A
 *  key that is not a plain identifier is bracket-quoted, so a key holding
 *  brackets, dots or quotes can never be confused with an index. Every child
 *  path extends its parent string, which is what makes the ancestor scan in
 *  `ancestorsOf` sound. */
export function childPath(parent: string, key: string, index: number | null): string {
  if (index !== null) return `${parent}[${index}]`
  if (IDENT_RE.test(key)) return `${parent}.${key}`
  return `${parent}[${JSON.stringify(key)}]`
}

/** Member count, computed without materialising children — the common case is a
 *  huge *collapsed* container, where enumerating the entries would be pure waste. */
function sizeOf(value: unknown, kind: JsonKind): number {
  if (kind === 'array') return (value as unknown[]).length
  if (kind === 'object') return Object.keys(value as object).length
  if (kind === 'string') return (value as string).length
  return 0
}

function containerSummary(kind: JsonKind, size: number): string {
  if (size === 0) return kind === 'array' ? '[]' : '{}'
  // An array's length says something about the data; an object's key count
  // mostly doesn't, and the keys show up as soon as it is expanded.
  return kind === 'array' ? `[${size} 项]` : '{…}'
}

/** Escape a bounded slice instead of the whole value: `JSON.stringify` over a
 *  multi-megabyte string, just to show 160 characters of it, is the single most
 *  expensive thing a naive viewer does. */
function stringPreview(text: string, limit: number): { preview: string; truncated: boolean } {
  if (text.length <= limit) {
    return { preview: JSON.stringify(text), truncated: false }
  }
  let chunk = text.slice(0, limit)
  // Never split a surrogate pair — the lone half renders as a replacement box.
  const last = chunk.charCodeAt(chunk.length - 1)
  if (last >= 0xd800 && last <= 0xdbff) chunk = chunk.slice(0, -1)
  return { preview: `${JSON.stringify(chunk).slice(1, -1)}…`, truncated: true }
}

function previewOf(
  kind: JsonKind,
  value: unknown,
  size: number,
  limit: number,
): { preview: string; truncated: boolean } {
  switch (kind) {
    case 'object':
    case 'array':
      return { preview: containerSummary(kind, size), truncated: false }
    case 'null':
      return { preview: 'null', truncated: false }
    case 'string':
      return size === 0
        ? { preview: '""', truncated: false }
        : stringPreview(value as string, limit)
    default:
      return { preview: String(value), truncated: false }
  }
}

export interface FlattenResult {
  rows: JsonRow[]
  /** The walk hit `ROW_BUDGET`; the list is a prefix of the real one. */
  limited: boolean
}

export interface FlattenOptions {
  expanded: ReadonlySet<string>
  /** When set (search mode), only these paths are listed, and every container on
   *  the way to a match renders expanded regardless of `expanded`. */
  visible?: ReadonlySet<string> | null
  /** Characters of a string shown before its preview is cut. */
  previewLimit?: number
}

/**
 * Walk the value depth-first, emitting a row per visible node.
 *
 * Iterative rather than recursive: JSON nested a few thousand levels deep is a
 * legal payload, and a recursive walk would blow the call stack on it.
 */
export function flatten(value: unknown, options: FlattenOptions): FlattenResult {
  const { expanded, visible = null, previewLimit = PREVIEW_LIMIT } = options
  const searchMode = visible !== null

  const rows: JsonRow[] = []
  let limited = false

  interface Frame {
    key: string | null
    index: number | null
    depth: number
    path: string
    value: unknown
    kind: JsonKind
  }

  // A non-empty root container gets no row of its own: it is always open, so a
  // row for it would only add a blank first line and a level of indentation.
  // Its members start at depth 0. A scalar or empty root still shows as one row.
  const rootKind = kindOf(value)
  const rootHidden = isContainer(rootKind) && sizeOf(value, rootKind) > 0
  const stack: Frame[] = [
    { key: null, index: null, depth: rootHidden ? -1 : 0, path: '$', value, kind: rootKind },
  ]

  while (stack.length > 0) {
    const frame = stack.pop()!
    if (searchMode && !visible.has(frame.path)) continue

    const { kind } = frame
    const size = sizeOf(frame.value, kind)
    const container = isContainer(kind)
    const expandable = container && size > 0
    const preview = previewOf(kind, frame.value, size, previewLimit)
    const hidden = rootHidden && frame.path === '$'
    // Under a search term every surviving container opens, so a match nested
    // deeper than the user's expand state is actually reachable.
    const expandedHere = expandable && (hidden || searchMode || expanded.has(frame.path))

    if (!hidden) {
      rows.push({
        path: frame.path,
        key: frame.key,
        index: frame.index,
        depth: frame.depth,
        kind,
        value: frame.value,
        size,
        // `{…}` only stands in for hidden members; once they are listed below it
        // says nothing. An array keeps its `[N 项]`, since the length still helps.
        preview: expandedHere && kind === 'object' ? '' : preview.preview,
        truncated: preview.truncated,
        expandable,
        expanded: expandedHere,
      })

      if (rows.length >= ROW_BUDGET) {
        limited = true
        break
      }
    }

    if (!expandedHere) continue

    const depth = frame.depth + 1
    const parent = frame.path
    if (kind === 'array') {
      const items = frame.value as unknown[]
      for (let i = items.length - 1; i >= 0; i--) {
        const item = items[i]
        stack.push({
          key: String(i),
          index: i,
          depth,
          path: `${parent}[${i}]`,
          value: item,
          kind: kindOf(item),
        })
      }
    } else {
      const record = frame.value as Record<string, unknown>
      const keys = Object.keys(record)
      for (let i = keys.length - 1; i >= 0; i--) {
        const k = keys[i]!
        const item = record[k]
        stack.push({
          key: k,
          index: null,
          depth,
          path: childPath(parent, k, null),
          value: item,
          kind: kindOf(item),
        })
      }
    }
  }

  return { rows, limited }
}

/** Every proper prefix of a path that ends on a segment boundary — the string
 *  form of "this node's ancestors", derived without keeping parent pointers. The
 *  result is ordered outermost-first, so the *parent* is the last entry and the
 *  node itself is never included. A quoted segment is skipped over so a key
 *  holding `.` or `[` cannot produce a phantom ancestor. */
export function ancestorsOf(path: string): string[] {
  const out: string[] = []
  let inString = false
  let escaped = false
  for (let i = 0; i < path.length; i++) {
    const ch = path[i]
    if (inString) {
      if (escaped) escaped = false
      else if (ch === '\\') escaped = true
      else if (ch === '"') inString = false
      continue
    }
    if (ch === '"') {
      inString = true
      continue
    }
    if (ch === '.' || ch === '[') out.push(path.slice(0, i))
  }
  return out
}

/** The parent path of `path`, or `null` for the root. `ancestorsOf` ends with
 *  the path's own parent, so this is just its last entry — but reading it
 *  through a named helper keeps the off-by-one out of the call sites. */
export function parentPathOf(path: string): string | null {
  return ancestorsOf(path).at(-1) ?? null
}

export interface SearchResult {
  /** Matches, their ancestors, and everything *inside* a matched container. */
  visible: Set<string>
  matched: Set<string>
  /** The scan stopped at `SEARCH_BUDGET` visited nodes; matches may be missing. */
  limited: boolean
}

/**
 * Full-depth scan for keys and leaf values containing `query`.
 *
 * Unlike `flatten` this cannot stop early — a match may sit under a collapsed
 * subtree — so it is bounded by a visited-node budget instead. Visibility is
 * decided on the way *out* of each node, so a container survives exactly when it
 * matched or something below it did, and a matched container keeps its subtree.
 */
export function searchPaths(value: unknown, query: string, budget = SEARCH_BUDGET): SearchResult {
  const needle = query.trim().toLowerCase()
  const visible = new Set<string>()
  const matched = new Set<string>()
  let limited = false
  let visited = 0

  interface Node {
    key: string | null
    index: number | null
    path: string
    value: unknown
    kind: JsonKind
    parent: Node | null
    /** An ancestor matched, so this whole subtree is on show. */
    underMatch: boolean
    selfMatch: boolean
    childHit: boolean
  }

  const root: Node = {
    key: null,
    index: null,
    path: '$',
    value,
    kind: kindOf(value),
    parent: null,
    underMatch: false,
    selfMatch: false,
    childHit: false,
  }

  // Enter/exit markers give the post-order decision without recursion.
  type Step = { enter: true; node: Node } | { enter: false; node: Node }
  const stack: Step[] = [{ enter: true, node: root }]

  while (stack.length > 0) {
    const step = stack.pop()!

    if (!step.enter) {
      const node = step.node
      // Visible when it matched, when it leads to a match, or when an ancestor
      // matched. A container nobody under matched drops out with its children.
      if (node.selfMatch || node.childHit || node.underMatch) {
        visible.add(node.path)
        if (node.parent && !node.parent.selfMatch) node.parent.childHit = true
      }
      continue
    }

    const node = step.node
    if (visited++ >= budget) {
      limited = true
      break
    }

    if (node.key !== null) node.selfMatch = node.key.toLowerCase().includes(needle)
    if (!node.selfMatch && node.index !== null) {
      node.selfMatch = String(node.index).includes(needle)
    }
    const container = isContainer(node.kind)
    if (!node.selfMatch && !container) {
      const text = node.kind === 'string' ? (node.value as string) : String(node.value)
      node.selfMatch = text.toLowerCase().includes(needle)
    }
    if (node.selfMatch && container) {
      matched.add(node.path)
      visible.add(node.path)
    } else if (node.selfMatch) {
      matched.add(node.path)
    }

    stack.push({ enter: false, node })
    if (!container) continue

    const underMatch = node.underMatch || node.selfMatch
    const make = (
      key: string | null,
      index: number | null,
      path: string,
      child: unknown,
    ): Node => ({
      key,
      index,
      path,
      value: child,
      kind: kindOf(child),
      parent: node,
      underMatch,
      selfMatch: false,
      childHit: false,
    })

    if (node.kind === 'array') {
      const items = node.value as unknown[]
      for (let i = items.length - 1; i >= 0; i--) {
        stack.push({ enter: true, node: make(String(i), i, `${node.path}[${i}]`, items[i]) })
      }
    } else {
      const record = node.value as Record<string, unknown>
      const keys = Object.keys(record)
      for (let i = keys.length - 1; i >= 0; i--) {
        const k = keys[i]!
        stack.push({ enter: true, node: make(k, null, childPath(node.path, k, null), record[k]) })
      }
    }
  }

  return { visible, matched, limited }
}

/** Every expandable path under `value`, for "expand subtree" and "expand all". */
export function collectExpandable(
  value: unknown,
  fromPath: string,
  limit: number,
): { paths: string[]; limited: boolean } {
  const paths: string[] = []
  let limited = false

  interface Frame {
    value: unknown
    path: string
  }
  const stack: Frame[] = [{ value, path: fromPath }]

  while (stack.length > 0) {
    const frame = stack.pop()!
    const kind = kindOf(frame.value)
    if (!isContainer(kind)) continue
    const size = sizeOf(frame.value, kind)
    if (size === 0) continue
    if (paths.length >= limit) {
      limited = true
      break
    }
    paths.push(frame.path)
    if (kind === 'array') {
      const items = frame.value as unknown[]
      for (let i = items.length - 1; i >= 0; i--) {
        stack.push({ value: items[i], path: `${frame.path}[${i}]` })
      }
    } else {
      const record = frame.value as Record<string, unknown>
      const keys = Object.keys(record)
      for (let i = keys.length - 1; i >= 0; i--) {
        const k = keys[i]!
        stack.push({ value: record[k], path: childPath(frame.path, k, null) })
      }
    }
  }
  return { paths, limited }
}

/** Text a row's whole value copies as: the bare string for a string, the compact
 *  JSON for anything else. Objects and arrays are rebuilt from the live value,
 *  so this is only ever called on demand — never during a walk. */
export function rawTextOf(value: unknown): string {
  if (typeof value === 'string') return value
  if (value === undefined) return 'undefined'
  return JSON.stringify(value) ?? String(value)
}

/** Pretty-printed JSON, or `null` when the value cannot be serialised (a cycle,
 *  a BigInt, a function) — the caller reports that rather than throwing. */
export function prettyJsonOf(value: unknown, indent = 2): string | null {
  try {
    const text = JSON.stringify(value, null, indent)
    return text === undefined ? null : text
  } catch {
    return null
  }
}

/** Compact one-line JSON, used for row tooltips and single-line previews. */
export function compactJsonOf(value: unknown): string | null {
  try {
    const text = JSON.stringify(value)
    return text === undefined ? null : text
  } catch {
    return null
  }
}

/** Whether a string leaf holds a JSON document worth offering a parsed view of.
 *  Called only when the user asks for it, so the parse cost is theirs to accept. */
export function parseJsonText(text: string): { ok: true; value: unknown } | { ok: false } {
  const first = text.trimStart()[0]
  if (first !== '{' && first !== '[') return { ok: false }
  try {
    return { ok: true, value: JSON.parse(text) }
  } catch {
    return { ok: false }
  }
}

/** Human-readable byte/char count for the row meta column. */
export function formatChars(count: number): string {
  if (count < 1000) return String(count)
  if (count < 10000) return `${(count / 1000).toFixed(1)}k`
  if (count < 1000000) return `${Math.round(count / 1000)}k`
  return `${(count / 1000000).toFixed(1)}M`
}
