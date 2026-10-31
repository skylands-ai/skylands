import '@fontsource/barlow/400.css'
import '@fontsource/barlow/500.css'
import '@fontsource/barlow/600.css'
import '@fontsource/barlow/700.css'
import '@fontsource/jetbrains-mono/400.css'
import '@fontsource/jetbrains-mono/500.css'
import '@fontsource/sora/700.css'
import './style.css'

import { byId, el } from './dom'
import { renderHeader, type NavActions } from './header'
import { renderFilePanel } from './panels/file'
import { renderIslandPanel } from './panels/island'
import { renderTerracePanel } from './panels/terrace'
import { renderWorldPanel } from './panels/world'
import { citadelLabelStatus, clockText, durationText, islandStatus, isStale, secondsSince } from './status'
import { islandList, level, reposOn, Store, type State } from './store'
import { World } from './world/world'
import { connectToCitadel } from './ws'

const stage = byId<HTMLElement>('stage')
const canvas = byId<HTMLCanvasElement>('world')
const labelsEl = byId<HTMLElement>('labels')
const panelEl = byId<HTMLElement>('panel')
const crumbsEl = byId<HTMLElement>('crumbs')
const connectedEl = byId<HTMLElement>('connected')
const linkEl = byId<HTMLElement>('link')
const veilEl = byId<HTMLElement>('veil')
const veilMsgEl = byId<HTMLElement>('veilmsg')

const store = new Store()

let world: World | null = null
try {
  world = new World(stage, canvas, labelsEl)
} catch (err) {
  console.error('world: WebGL unavailable', err)
  stage.appendChild(el('div', { class: 'nogl', text: 'This view needs WebGL, and it isn’t available in this browser right now.' }))
}

// Navigation goes through the world when it runs, so the camera moves, and
// straight to the store otherwise.
const nav: NavActions & { selectFile(host: string, terrace: string, idx: number): void } = {
  overview: () => (world ? world.overview() : store.update({ selected: 'cit', terrace: null, file: null })),
  select: (host) => (world ? world.select(host) : store.update({ selected: host, terrace: null, file: null })),
  land: (host, terrace) => (world ? world.land(host, terrace) : store.update({ selected: host, terrace, file: null })),
  selectFile: (host, terrace, idx) => (world ? world.selectFile(host, terrace, idx) : store.update({ selected: host, terrace, file: idx })),
}

function renderVeil(s: State): void {
  // Only a lost link veils the world. Before the first contact there is nothing stale to show.
  veilEl.hidden = s.link !== 'lost'
  if (veilEl.hidden) return
  veilMsgEl.textContent = ''
  const since = s.lastContact ? `for ${durationText(secondsSince(s.lastContact))}` : ''
  veilMsgEl.append(
    `Citadel unreachable ${since}`.trim(),
    el('span', {
      text: s.lastContact
        ? `The world shows the last state heard, at ${clockText(s.lastContact)}. This page keeps retrying.`
        : 'This page keeps retrying.',
    }),
  )
}

function renderPanel(s: State, stale: boolean): void {
  const island = s.selected !== 'cit' ? s.islands[s.selected] : undefined
  const repo = s.terrace ? s.repos[s.terrace] : undefined
  switch (level(s)) {
    case 'file':
      if (island && repo && s.file !== null) {
        renderFilePanel(panelEl, repo, island, s.file, () => nav.land(island.host, repo.id))
        return
      }
      break
    case 'terrace':
      if (island && repo) {
        const shown = world?.buildingCount(island.host, repo.id) ?? repo.files.length
        renderTerracePanel(panelEl, repo, island, stale, shown, (idx) => nav.selectFile(island.host, repo.id, idx))
        return
      }
      break
    case 'island':
      if (island) {
        renderIslandPanel(panelEl, island, reposOn(s, island.host), stale, s.lastContact, (terrace) => nav.land(island.host, terrace))
        return
      }
      break
  }
  renderWorldPanel(panelEl, s, nav.select)
}

store.subscribe((s) => {
  const stale = isStale(s)
  renderHeader(crumbsEl, connectedEl, linkEl, s, nav)
  renderVeil(s)
  if (world) {
    for (const i of islandList(s)) {
      world.upsertIsland(i.host, i.reachable)
      world.setRepos(i.host, reposOn(s, i.host))
      world.setLabel(i.host, i.host, islandStatus(i, stale), 0)
    }
    world.setLabel('cit', 'Citadel', citadelLabelStatus(s), 0)
    world.setBeacon(false)
    world.setStale(stale)
    world.setCitadelCrystal(!!s.server?.connected)
  }
  renderPanel(s, stale)
})

// A handle for debugging and screenshots: skylands.world.rig.flyTo(...).
;(window as unknown as { skylands: unknown }).skylands = { world, store }

if (world) {
  world.onNav = (n) => store.update(n)
  byId<HTMLButtonElement>('overview').addEventListener('click', nav.overview)
  window.addEventListener('keydown', (e) => {
    const t = e.target as HTMLElement | null
    const typing = t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)
    if (!typing && e.key === 'Escape') world?.up()
  })
  world.start()
}

// Times in the panel tick.
setInterval(() => store.touch(), 1000)

connectToCitadel(store)
