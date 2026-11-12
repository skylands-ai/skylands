import type { KeyPair } from '@nats-io/nkeys'
import { sha256 } from '@noble/hashes/sha2.js'
import { bytesToHex } from '@noble/hashes/utils.js'
import { toBase64url } from './approver'
import { canonicalize } from './canonical'
import type { QuestionField, Rule } from './protocol'

/** The version of the decision formats, which a daemon checks. */
export const DECISION_VERSION = 1

/**
 * What an approver signs to allow or deny one tool call, as
 * internal/approval's ToolCall. The signature covers the SHA-256 of its
 * canonical JSON; the daemon builds the same decision from its own record
 * of the request, so the signature verifies only if they agree.
 */
export interface ToolCallDecision {
  v: number
  kind: 'toolcall'
  /** The world's owner key. */
  world: string
  host: string
  session: string
  request: string
  decision: 'allow' | 'deny'
  /** The option id an approval chose, and "" for a denial. */
  option: string
  /** The SHA-256 of the tool call's exact input, in lowercase hex. */
  input: string
}

/** An answer to a question, or a decline, as internal/approval's Answer. */
export interface AnswerDecision {
  v: number
  kind: 'answer'
  world: string
  host: string
  session: string
  question: string
  /** formHash of the question as shown. */
  form: string
  declined: boolean
  /** answersHash of the answers, and "" for a decline. */
  answers: string
}

/** Turning a session's trust on or off, over the revision it replaces. */
export interface TrustDecision {
  v: number
  kind: 'trust'
  world: string
  host: string
  session: string
  trusted: boolean
  revision: number
}

/** Setting a session's rules: rulesHash of the whole list, over the revision it replaces. */
export interface SessionRulesDecision {
  v: number
  kind: 'session-rules'
  world: string
  host: string
  session: string
  rules: string
  revision: number
}

/** Setting a repository's rules, as SessionRulesDecision. */
export interface RepoRulesDecision {
  v: number
  kind: 'repo-rules'
  world: string
  host: string
  repo: string
  rules: string
  revision: number
}

/**
 * Starting a session with its first prompt, as internal/approval's Start.
 * The browser picks the session's id, so a signed start starts one session.
 */
export interface StartDecision {
  v: number
  kind: 'start'
  world: string
  host: string
  session: string
  repo: string
  agent: string
  /** As the command names them: empty for the adapter's first mode and the agent's own model. */
  mode: string
  model: string
  inCheckout: boolean
  /** textHash of the prompt. */
  prompt: string
}

/** Another prompt to a session, at the session's revision, which the prompt moves on. */
export interface PromptDecision {
  v: number
  kind: 'prompt'
  world: string
  host: string
  session: string
  revision: number
  prompt: string
}

/** A commit, merge, pull request, or removal of a session's worktree, at its revision. */
export interface ChangeDecision {
  v: number
  kind: 'change'
  world: string
  host: string
  session: string
  revision: number
  action: 'commit' | 'merge' | 'pull-request' | 'remove'
  /** textHash of a commit's message, pullRequestHash of a pull request's title and body, and "" for a merge or a removal. */
  text: string
}

/** Opening a shell in a session's worktree. The browser picks the terminal's id, so a signed opening opens one terminal. */
export interface TerminalDecision {
  v: number
  kind: 'terminal'
  world: string
  host: string
  session: string
  terminal: string
}

/**
 * Typing or pasting into a terminal. attach names this browser's view of
 * the terminal and seq counts its inputs from one, which the daemon takes
 * only in increasing order, so a signature can't be used again.
 */
export interface TerminalInputDecision {
  v: number
  kind: 'terminal-input'
  world: string
  host: string
  session: string
  terminal: string
  attach: string
  seq: number
  /** bytesHash of the input. */
  data: string
}

export type Decision = ToolCallDecision | AnswerDecision | TrustDecision | SessionRulesDecision | RepoRulesDecision | StartDecision | PromptDecision | ChangeDecision | TerminalDecision | TerminalInputDecision

/** An approver's signature over a decision, which the command through the citadel carries. */
export interface DecisionSignature {
  approver: string
  sig: string
}

/** The SHA-256 of the decision's canonical JSON, which is what is signed. */
export function decisionDigest(d: Decision): Uint8Array {
  return sha256(new TextEncoder().encode(canonicalize(d)))
}

export function signDecision(kp: KeyPair, d: Decision): DecisionSignature {
  return { approver: kp.getPublicKey(), sig: toBase64url(kp.sign(decisionDigest(d))) }
}

/** The SHA-256 of a text's UTF-8 bytes, in lowercase hex. */
export function textHash(text: string): string {
  return bytesHash(new TextEncoder().encode(text))
}

/** The SHA-256 of some bytes, in lowercase hex. */
export function bytesHash(b: Uint8Array): string {
  return bytesToHex(sha256(b))
}

/** The SHA-256 of a pull request's title and body as canonical JSON. */
export function pullRequestHash(title: string, body: string): string {
  return hashJSON({ title, body })
}

/** The SHA-256 of a value's canonical JSON, in lowercase hex. */
export function hashJSON(v: unknown): string {
  return bytesToHex(sha256(new TextEncoder().encode(canonicalize(v))))
}

/**
 * The SHA-256 of a question's canonical JSON: its message and its fields,
 * each with every property and an absent one empty, as the daemon hashes
 * the question it recorded.
 */
export function formHash(message: string, fields: QuestionField[]): string {
  return hashJSON({
    message,
    fields: fields.map((f) => ({
      key: f.key,
      title: f.title ?? '',
      description: f.description ?? '',
      type: f.type,
      options: (f.options ?? []).map((o) => ({ value: o.value, title: o.title ?? '', description: o.description ?? '' })),
      required: !!f.required,
    })),
  })
}

/** The SHA-256 of answers' canonical JSON. */
export function answersHash(answers: Record<string, unknown>): string {
  return hashJSON(answers)
}

/**
 * A rule list as the daemon keeps it: decisions and kinds lowercased and
 * trimmed, an empty kind or "any" meaning any kind, patterns trimmed, and
 * no source on a repository's own rules. The browser sends and signs the
 * list in this form, so the signature is over what the daemon applies.
 */
export function normalizeRules(rules: Rule[], forRepo: boolean): Rule[] {
  return rules.map((r) => {
    let kind = r.kind.trim().toLowerCase()
    if (kind === '' || kind === 'any') kind = '*'
    const out: Rule = { decision: r.decision.trim().toLowerCase() as Rule['decision'], kind }
    const pattern = r.pattern?.trim()
    if (pattern) out.pattern = pattern
    if (!forRepo && r.source) out.source = r.source
    return out
  })
}

/** The SHA-256 of a rule list's canonical JSON, each rule with every property and an absent one empty. */
export function rulesHash(rules: Rule[]): string {
  return hashJSON(rules.map((r) => ({ decision: r.decision, kind: r.kind, pattern: r.pattern ?? '', source: r.source ?? '' })))
}
