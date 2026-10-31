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
export interface Session {
  host: string
  id: string
  repoId: string
  agent: string
  model?: string
  agentName?: string
  prompt: string
  branch: string
  /** running: the agent works on a turn. idle: it waits for a person. */
  status: 'running' | 'idle' | 'ended' | 'failed'
  stopReason?: string
  error?: string
  turns: number
  /** What the agent named the conversation, once it has. */
  title?: string
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
