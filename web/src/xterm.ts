// The terminal emulator, in a chunk of its own: only a page that opens a
// terminal downloads it.
import '@xterm/xterm/css/xterm.css'
import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'

export { FitAddon, Terminal }

/**
 * The terminal's colors. A terminal is dark in either page theme: what
 * programs print assumes a dark screen, and yellow on white can't be read.
 */
export const THEME = {
  background: '#0F1A26',
  foreground: '#E7EEF4',
  cursor: '#FFD54A',
  cursorAccent: '#0F1A26',
  selectionBackground: '#2D5BC780',
  black: '#1D3043',
  red: '#FF7A6E',
  green: '#6FD08C',
  yellow: '#FFD54A',
  blue: '#8DA9FF',
  magenta: '#D7A2F5',
  cyan: '#A9E8F5',
  white: '#C9D6E2',
  brightBlack: '#5D758C',
  brightRed: '#FFA79E',
  brightGreen: '#9BE3B0',
  brightYellow: '#FFE38A',
  brightBlue: '#B5C7FF',
  brightMagenta: '#E8C7FA',
  brightCyan: '#CFF4FB',
  brightWhite: '#FFFFFF',
}
