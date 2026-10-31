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
  status: 'running' | 'ended' | 'failed'
  stopReason?: string
  error?: string
  startedAt: string
  endedAt?: string
  currentFile?: string
  currentAction?: string
  recent: ToolCall[]
  files: Record<string, string>
  plan?: PlanEntry[]
  used?: number
  size?: number
}

export interface Message {
  type: string
  server?: ServerInfo
  islands?: Island[]
  island?: Island
  repos?: Repo[]
  repo?: Repo
  sessions?: Session[]
  session?: Session
}
