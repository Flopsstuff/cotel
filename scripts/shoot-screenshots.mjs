// Re-take the README screenshots against a seeded throwaway instance.
//
//   npx playwright-core@latest --help >/dev/null   # once, to populate the cache
//   BASE=http://localhost:8080 node scripts/shoot-screenshots.mjs
//
// Needs playwright-core resolvable from here and a chromium at CHROMIUM (default
// /usr/bin/chromium). Full recipe: docs/operations/screenshots.md.

import { chromium } from 'playwright-core'

const BASE = process.env.BASE || 'http://localhost:8080'
const OUT = process.env.OUT || 'docs/assets'
const CHROMIUM = process.env.CHROMIUM || '/usr/bin/chromium'

// Each shot is cut at the bottom of a real element rather than at a pixel
// count, so a row height or chart size change does not slice a card in half.
const STAT_SECTIONS = `[...document.querySelectorAll('div')].filter(d => typeof d.className === 'string' && /card/i.test(d.className) && d.querySelector(':scope > div > button[aria-expanded]'))`

const SHOTS = [
  {
    name: 'dashboard-overview',
    path: '/',
    wait: '.recharts-surface',
    end: `(${STAT_SECTIONS}).find(d => /^activity & cost/i.test(d.textContent.trim()))`,
  },
  { name: 'dashboard-users', path: '/users', wait: 'table', end: `document.querySelector('table')` },
  { name: 'dashboard-tools', path: '/tools', wait: 'table', end: `document.querySelector('table')` },
  { name: 'dashboard-sessions', path: '/sessions', wait: 'table', end: `document.querySelectorAll('tbody tr')[8]` },
  {
    name: 'dashboard-costs',
    path: '/costs',
    wait: '.recharts-surface',
    end: `[...document.querySelectorAll('div')].find(d => typeof d.className === 'string' && /card/i.test(d.className) && /^cost by model/i.test(d.textContent.trim()))`,
  },
]

const WIDTH = 1440
const MAX_HEIGHT = 1400
const PAD = 8

const browser = await chromium.launch({ executablePath: CHROMIUM })
const ctx = await browser.newContext({
  viewport: { width: WIDTH, height: MAX_HEIGHT },
  deviceScaleFactor: 2,
  colorScheme: 'dark',
})
const page = await ctx.newPage()

for (const shot of SHOTS) {
  await page.goto(BASE + shot.path, { waitUntil: 'networkidle' })
  await page.waitForSelector(shot.wait, { timeout: 15000 })
  await page.waitForTimeout(2500) // chart entry animations

  const bottom = await page.evaluate(`(() => { const el = ${shot.end}; if (!el) return null;
    const r = el.getBoundingClientRect(); return Math.ceil(r.bottom + window.scrollY) })()`)
  if (!bottom) throw new Error(`${shot.name}: could not locate the element to crop at`)

  const height = Math.min(bottom + PAD, MAX_HEIGHT)
  await page.screenshot({
    path: `${OUT}/${shot.name}.png`,
    clip: { x: 0, y: 0, width: WIDTH, height },
  })
  console.log(`${shot.name}.png (${WIDTH}x${height} @2x)`)
}

await browser.close()
