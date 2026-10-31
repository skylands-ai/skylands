import '@fontsource/barlow/400.css'
import '@fontsource/barlow/500.css'
import '@fontsource/barlow/600.css'
import '@fontsource/barlow/700.css'
import '@fontsource/jetbrains-mono/400.css'
import '@fontsource/jetbrains-mono/500.css'
import '@fontsource/sora/700.css'
import './style.css'

import { byId, el } from './dom'
import { renderHeader } from './header'
import { renderIslandPanel } from './panels/island'
import { renderWorldPanel } from './panels/world'
import { citadelLabelStatus, clockText, durationText, islandStatus, isStale, secondsSince } from './status'
import { islandList, Store, type State } from './store'
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

const select = (id: string): void => {
  if (world) world.select(id)
  else store.update({ selected: id })
}
const overview = (): void => {
  if (world) world.overview()
  else store.update({ selected: 'cit' })
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

store.subscribe((s) => {
  const stale = isStale(s)
  renderHeader(crumbsEl, connectedEl, linkEl, s, overview)
  renderVeil(s)
  const island = s.selected !== 'cit' ? s.islands[s.selected] : undefined
  if (island) renderIslandPanel(panelEl, island, stale, s.lastContact)
  else renderWorldPanel(panelEl, s, select)
  if (world) {
    for (const i of islandList(s)) {
      world.upsertIsland(i.host, i.reachable)
      world.setLabel(i.host, i.host, islandStatus(i, stale), 0)
    }
    world.setLabel('cit', 'Citadel', citadelLabelStatus(s), 0)
    world.setBeacon(false)
    world.setStale(stale)
  }
})

if (world) {
  world.onSelect = (id) => store.update({ selected: id })
  byId<HTMLButtonElement>('overview').addEventListener('click', overview)
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
