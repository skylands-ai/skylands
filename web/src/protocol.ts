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

/** The world as the citadel knows it. */
export interface WorldInfo {
  /** Whether the citadel has looked for the world since it reached NATS. */
  checked: boolean
  /** Whether the world has been set up. */
  ready: boolean
  name?: string
  /** The owner key's public nkey, which a daemon on another host pins with --world. */
  owner?: string
  /** The address daemons on other hosts dial, when the citadel knows it. */
  leafUrl?: string
  /** NATS settings the world needs that are missing. */
  problems?: string[]
  natsSettingsUrl?: string
}

/** One approver key, as the world's registry decides it. */
export interface ApproverInfo {
  key: string
  name: string
  enrolled: string
  /** "owner" for an approver the owner key enrolled, or the key of the approver whose link it used. */
  by: string
  /** The claim key of the link it used, if it used one. */
  voucher?: string
  /** True while it approves: not revoked, and added through approvers that aren't, back to the owner key. */
  valid: boolean
  revoked?: string
  /** "owner" or the key of the approver that revoked it. */
  revokedBy?: string
  /** The valid approvers added through this one, which revoking it takes along. */
  through?: string[]
}

/** What a browser sees of an "Add an approver" link: who made it, until when it works, and whether it was used. */
export interface VoucherInfo {
  claim: string
  by: string
  byName: string
  expires: string
  expired: boolean
  usedBy?: string
  /** True while the approver that made it approves. */
  valid: boolean
}

export interface DaemonInfo {
  id: string
  version: string
  os: string
  arch: string
  started: string
}

/** One check that failed or warned for an agent, with what to do about it. */
export interface AvailabilityNote {
  check: string
  failed?: boolean
  found: string
  fix?: string
}

/**
 * Whether a host can launch one agent type, as its daemon last checked by
 * starting the agent without a session, in the daemon's own environment.
 */
export interface AgentAvailability {
  name: string
  available: boolean
  /** What the agent calls itself, with its version. */
  agent?: string
  /** The login the agent reported, such as "Claude Max". */
  login?: string
  /** The checks that failed or warned; a failed one is why it can't run. */
  notes?: AvailabilityNote[]
  /**
   * Whether the daemon read the models the agent offers this host's login,
   * from its model setting; with no models, the agent has no model setting.
   */
  modelsRead?: boolean
  /** The models the agent offered, in its order. */
  models?: AgentModel[]
  /** The model a new session started on. */
  defaultModel?: string
}

/** One model an agent offers: the id a session's model is set to, and what the agent calls it. */
export interface AgentModel {
  id: string
  name?: string
  description?: string
}

/** One host as the citadel last saw it. Islands are never removed, only marked unreachable. */
export interface Island {
  host: string
  reachable: boolean
  firstSeen: string
  lastSeen: string
  daemon: DaemonInfo
  /** The agents the host can launch; absent until its daemon has recorded a check. */
  agents?: AgentAvailability[]
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

/** Tokens counted by kind, for one request or summed over many. Input leaves out the cached counts; total is the agent's own sum. */
export interface TokenUsage {
  input: number
  output: number
  cachedRead?: number
  cachedWrite?: number
  thought?: number
  total: number
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
  /** A person interrupted the turn and the agent has not stopped yet. */
  cancelling?: boolean
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
  /** The latest reading of the context window: used tokens of size. */
  used?: number
  size?: number
  /** What the session has cost so far: every turn's tokens and the title request's, summed. */
  tokens: TokenUsage
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

/** One piece of what a tool call produced: text such as a command's output, a diff, or a terminal reference. */
export interface ToolCallContent {
  type: string
  text?: string
  path?: string
  oldText?: string
  newText?: string
  terminalId?: string
}

/** Everything the host kept about one tool call: the exact input, what came back, and when. */
export interface ToolCallDetail {
  toolCallId: string
  title: string
  kind?: string
  status: string
  locations?: string[]
  input?: unknown
  content?: ToolCallContent[]
  output?: string
  truncated?: boolean
  startedAt: string
  updatedAt: string
  endedAt?: string
}

export interface Message {
  type: string
  host?: string
  id?: string
  live?: Live
  server?: ServerInfo
  world?: WorldInfo
  approvers?: ApproverInfo[]
  islands?: Island[]
  island?: Island
  repos?: Repo[]
  repo?: Repo
  sessions?: Session[]
  session?: Session
}
