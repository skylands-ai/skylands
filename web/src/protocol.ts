// Mirrors internal/citadel/web/protocol.go and internal/citadel/projections.

export interface ServerInfo {
  connected: boolean
  url?: string
  name?: string
  id?: string
  version?: string
  cluster?: string
  servers?: number
  domain?: string
}

export interface DaemonInfo {
  id: string
  version: string
  os: string
  arch: string
  started: string
}

/** One host as the citadel last heard it. Islands are never removed, only marked unreachable. */
export interface Island {
  host: string
  reachable: boolean
  firstSeen: string
  lastSeen: string
  daemon: DaemonInfo
}

export interface TerrainFile {
  path: string
  size: number
}

/**
 * A rule settles tool calls before they reach a person: a decision, a tool
 * call kind or '*' for any, and an optional pattern. A session's rule copied
 * from its repository has source 'repo'; one set on the session has none.
 */
export interface Rule {
  decision: 'allow' | 'ask' | 'deny'
  kind: string
  pattern?: string
  source?: string
}

/** One registered repository on one host: a terrace, with a building for each of its largest files. */
export interface Repo {
  host: string
  id: string
  name: string
  path: string
  commit?: string
  /** Every tracked file, while files holds at most the largest 64. */
  totalFiles: number
  files: TerrainFile[]
  /** The rules a new session on this repository starts with. */
  rules: Rule[]
  registeredAt: string
  snapshottedAt: string
}

export interface PlanEntry {
  content: string
  priority?: string
  status: string
}

export interface ToolCall {
  id: string
  title: string
  kind?: string
  status: string
  locations?: string[]
  at: string
}

/** One agent run on one repository, as the citadel projects it. */
/** One answer the agent accepts for a permission request. */
export interface PermissionOption {
  id: string
  name: string
  /** allow_once, allow_always, reject_once, or reject_always. */
  kind: string
}

/** A tool call waiting on a person, with the exact input the agent proposed. */
export interface Permission {
  requestId: string
  toolCallId: string
  title: string
  kind?: string
  locations?: string[]
  input?: unknown
  options: PermissionOption[]
  at: string
}

/** How a session's agent came back after a daemon restart. */
export interface Resumed {
  at: string
  restartedAt: string
  /** 'load' when the agent loaded its own session, 'seed' when a new one was given the transcript. */
  how: 'load' | 'seed'
  /** The restart cut a turn short and the agent picked it up again. */
  turnResumed?: boolean
}

/** One choice a question field offers. */
export interface QuestionOption {
  value: string
  title?: string
  description?: string
}

/** One field of a question's form: a choice when it has options, free text otherwise, several at once when its type is array. */
export interface QuestionField {
  key: string
  title?: string
  description?: string
  type: 'string' | 'number' | 'integer' | 'boolean' | 'array'
  options?: QuestionOption[]
  required?: boolean
}

/** A question the agent is waiting on a person to answer. */
export interface Question {
  questionId: string
  toolCallId?: string
  message: string
  fields: QuestionField[]
  at: string
}

export interface Session {
  host: string
  id: string
  repoId: string
  agent: string
  model?: string
  agentName?: string
  prompt: string
  /** What the agent works on: its own branch, cut from base, or the checkout's when inCheckout is set. */
  branch: string
  base?: string
  inCheckout?: boolean
  /** The latest commit made through Skylands, and how many there were. */
  commit?: string
  commits?: number
  /** The latest merge into the checkout, the pull request opened from the branch, and whether the worktree is gone. */
  mergedInto?: string
  mergedCommit?: string
  pullRequest?: string
  worktreeRemoved?: boolean
  /** running: the agent works on a turn. idle: it waits for a person. */
  status: 'running' | 'idle' | 'ended' | 'failed'
  stopReason?: string
  error?: string
  turns: number
  /** What the agent named the conversation, once it has. */
  title?: string
  /** Whether the session's tool calls are allowed without asking. */
  trusted: boolean
  /** The rules that settle this session's tool calls: the repository's as copied, and its own. */
  rules: Rule[]
  /** The tool calls waiting on a person, oldest first, and the questions waiting on an answer. */
  pending: Permission[]
  questions: Question[]
  /** How many daemon restarts the session has lived through, and how its agent last came back. */
  resumes?: number
  resumed?: Resumed
  startedAt: string
  endedAt?: string
  /** When the session's latest event of any kind happened: when fetched text about it went stale. */
  updatedAt: string
  /** When the agent or a person last did something in the conversation. A restart, a title, a trust switch, or a rule change does not move it. */
  activeAt: string
  currentFile?: string
  currentAction?: string
  recent: ToolCall[]
  files: Record<string, string>
  plan?: PlanEntry[]
  used?: number
  size?: number
}

/** Where a session's changes stand on the host's disk, as git sees them right now. */
export interface Changes {
  branch: string
  base?: string
  inCheckout?: boolean
  /** Files with uncommitted changes. */
  changed: string[]
  /** Commits on the branch that its base lacks. Zero for a checkout session. */
  ahead: number
  worktreeRemoved?: boolean
}

/** One piece of text appended to a transcript, at byte offset off. */
export interface Live {
  off: number
  text: string
}

export interface Message {
  type: string
  host?: string
  id?: string
  live?: Live
  server?: ServerInfo
  islands?: Island[]
  island?: Island
  repos?: Repo[]
  repo?: Repo
  sessions?: Session[]
  session?: Session
}
