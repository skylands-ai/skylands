import { el } from './dom'
import type { Permission, Rule } from './protocol'

/** The tool call kinds a rule can name, as ACP reports them. */
export const KINDS = ['read', 'edit', 'delete', 'move', 'search', 'execute', 'think', 'fetch', 'other', '*']

/** What a host allows when no rule applies. Shown, not editable. */
export const HOST_DEFAULTS: Rule[] = [
  { decision: 'allow', kind: 'read', source: 'host' },
  { decision: 'allow', kind: 'search', source: 'host' },
  { decision: 'allow', kind: 'think', source: 'host' },
]

/** A rule on one line: "allow execute go test *". */
export function ruleText(r: Rule): string {
  return `${r.decision} ${r.kind}${r.pattern ? ` ${r.pattern}` : ''}`
}

/** What a pattern means, for a placeholder or a hint. */
export function patternHint(kind: string): string {
  switch (kind) {
    case 'execute':
      return 'command pattern, such as go test *'
    case 'fetch':
      return 'URL pattern, such as https://github.com/*'
    case 'think':
    case 'search':
    case 'other':
      return 'leave empty'
    default:
      return 'worktree, or a path pattern such as web/*'
  }
}

/**
 * The rule a request suggests: the first two words of a command and
 * anything after, the worktree for an edit inside it or the file itself
 * outside, the site for a fetch. The person edits it before keeping it.
 */
export function proposeRule(p: Permission, parsedInput: unknown, decision: 'allow' | 'deny'): Rule {
  const kind = p.kind || '*'
  const input = (parsedInput && typeof parsedInput === 'object' ? parsedInput : {}) as Record<string, unknown>
  let pattern = ''
  if (kind === 'execute') {
    const command = (typeof input.command === 'string' && input.command) || p.title
    const words = command.trim().split(/\s+/)
    pattern = words.length > 2 ? `${words[0]} ${words[1]} *` : `${words.join(' ')} *`
  } else if (kind === 'fetch') {
    const url = (typeof input.url === 'string' && input.url) || p.title
    try {
      pattern = `${new URL(url).origin}/*`
    } catch {
      pattern = url
    }
  } else if (p.locations?.length) {
    pattern = p.locations.every((l) => !l.startsWith('/')) ? 'worktree' : p.locations[0]
  }
  return { decision, kind, pattern: pattern || undefined }
}

export interface RulesSheetOptions {
  /** Rules the host applies after these, shown greyed. */
  defaults?: Rule[]
  /** What a copied rule says it came from. */
  copiedFrom?: string
  /** Replaces the whole list. Resolves with an error message, or empty. */
  onChange(rules: Rule[]): Promise<string>
  /** Whether the list can be changed at all. */
  readOnly?: boolean
}

/**
 * The rules sheet: one row per rule with where it came from and a way to
 * remove it, a form to add one, and the host's defaults underneath. The
 * same sheet shows a repository's rules on its terrace and a session's
 * rules in its conversation.
 */
export function rulesSheet(rules: Rule[], opts: RulesSheetOptions): HTMLElement {
  const box = el('div', { class: 'rules', role: 'group', 'aria-label': 'Rules' })
  const note = el('span', { class: 'sub' })
  const change = async (next: Rule[]): Promise<void> => {
    note.textContent = 'Saving\u2026'
    note.style.color = ''
    const err = await opts.onChange(next)
    if (err) {
      note.textContent = err
      note.style.color = 'var(--bad)'
    } else note.textContent = ''
  }
  const row = (r: Rule, i: number | null): HTMLElement => {
    const from = r.source === 'host' ? 'host default' : r.source === 'repo' ? `from ${opts.copiedFrom ?? 'the repository'}` : i === null ? '' : opts.copiedFrom ? 'this session' : ''
    return el('div', { class: `rule ${r.decision}` }, [
      el('span', { class: 'pill', text: r.decision }),
      el('span', { class: 'kind', text: r.kind }),
      r.pattern ? el('code', { text: r.pattern }) : el('span', { class: 'muted', text: 'anything' }),
      el('span', { class: 'from muted', text: from }),
      i !== null && !opts.readOnly
        ? el('button', { type: 'button', class: 'btn small ghost', text: 'Remove', 'aria-label': `Remove the rule ${ruleText(r)}`, onclick: () => void change(rules.filter((_, j) => j !== i)) })
        : null,
    ])
  }
  if (!rules.length) box.appendChild(el('p', { class: 'sub', text: 'No rules yet. Everything but the host defaults asks you.' }))
  for (const [i, r] of rules.entries()) box.appendChild(row(r, i))
  for (const r of opts.defaults ?? []) box.appendChild(row(r, null))
  if (!opts.readOnly) {
    const decision = el('select', { 'aria-label': 'Decision' }, ['allow', 'ask', 'deny'].map((d) => el('option', { value: d, text: d }))) as HTMLSelectElement
    const kind = el('select', { 'aria-label': 'Kind' }, KINDS.map((k) => el('option', { value: k, text: k === '*' ? 'any kind' : k }))) as HTMLSelectElement
    const pattern = el('input', { type: 'text', 'aria-label': 'Pattern', placeholder: patternHint(kind.value) }) as HTMLInputElement
    kind.addEventListener('change', () => (pattern.placeholder = patternHint(kind.value)))
    const add = el('button', {
      type: 'button',
      class: 'btn small',
      text: 'Add a rule',
      onclick: () => void change([...rules, { decision: decision.value as Rule['decision'], kind: kind.value, pattern: pattern.value.trim() || undefined }]),
    })
    box.appendChild(el('div', { class: 'acts' }, [decision, kind, pattern, add, note]))
  } else box.appendChild(note)
  return box
}
