import { encode } from 'uqr'

const SVG = 'http://www.w3.org/2000/svg'

/** A QR code for text, drawn dark on light whatever the page's theme, since scanners need the contrast. */
export function qrCode(text: string, label: string): SVGSVGElement {
  const { data } = encode(text, { ecc: 'M', border: 2 })
  const size = data.length
  const svg = document.createElementNS(SVG, 'svg')
  svg.setAttribute('viewBox', `0 0 ${size} ${size}`)
  svg.setAttribute('role', 'img')
  svg.setAttribute('aria-label', label)
  svg.setAttribute('shape-rendering', 'crispEdges')
  svg.setAttribute('class', 'qr')
  const bg = document.createElementNS(SVG, 'rect')
  bg.setAttribute('width', String(size))
  bg.setAttribute('height', String(size))
  bg.setAttribute('fill', '#ffffff')
  let d = ''
  data.forEach((row, y) =>
    row.forEach((on, x) => {
      if (on) d += `M${x} ${y}h1v1h-1z`
    }),
  )
  const dots = document.createElementNS(SVG, 'path')
  dots.setAttribute('d', d)
  dots.setAttribute('fill', '#000000')
  svg.append(bg, dots)
  return svg
}
