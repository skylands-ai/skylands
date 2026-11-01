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
  /** The tool calls waiting on a person, oldest first. */
  pending: Permission[]
  /** How many daemon restarts the session has lived through, and how its agent last came back. */
  resumes?: number
  resumed?: Resumed
  startedAt: string
  endedAt?: string
  /** When the session's latest event happened. */
  updatedAt: string
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
