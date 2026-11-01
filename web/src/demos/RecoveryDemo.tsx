import { Count, Roll, Stage, Text, type Act, type Layout } from '../stageshow/kit'
import './RecoveryDemo.css'

// Acnologia's service page for a SerpAPI pool, cropped to the stat tiles and
// the key list. No cursor: the system does the work. Beats:
//  1. Traffic flows on every key (each key's "today" count ticks up).
//  2. acct-2 is rate limited: its pill turns to "cooling 0:08" and counts
//     down; the COOLING tile rolls 0 to 1, AVAILABLE 5 to 4.
//  3. acct-4 runs out: its quota drains to 0, the pill turns to
//     "out of quota" with the time to its reset; OUT OF QUOTA rolls 0 to 1.
//  4. The cooldown ends: acct-2 is available again and takes traffic.
//  5. The quota resets: acct-4 refills and comes back. The pool is whole
//     and holds, traffic still flowing, then the loop begins again on the
//     same picture.
//
// Everything is derived from one clock, `t` (ms into the loop), so the
// last frame and the first are the same picture. The "today" counts only
// ever rise: they live outside `initial`, so they carry on across loops and
// never rewind in view.
//
// Real strings: the tile labels and counts, the column heads, each key's
// label, hint and count, its status and quota. Last used is filler.

type S = { t: number; served?: number[] }
const initial: S = { t: 0 }
// Both keys parked, both countdowns running: the frame that tells the story.
const poster: S = { t: 6200 }

const STEP = 100
const LOOP = 14200
const TRAFFIC = 300

// The timeline, in ms. Each change leads, its dependents follow.
const COOL_AT = 1800 // acct-2 rate limited, Retry-After 8s
const COOL_END = COOL_AT + 8000
const DRAIN_AT = 4000 // acct-4's quota runs out
const OUT_AT = DRAIN_AT + 450 // its pill follows the empty bar
const RESET_AT = 11200 // acct-4's quota resets
const BACK_AT = RESET_AT + 250 // its pill follows the refill
const TILE_LAG = 300 // the tiles count a beat after the row

const KEYS = [
  { label: 'acct-1', hint: '...a1f3', today: 1204, quota: 612 },
  { label: 'acct-2', hint: '...b7e2', today: 1086, quota: 845 },
  { label: 'acct-3', hint: '...c04d', today: 1317, quota: 377 },
  { label: 'acct-4', hint: '...d9a6', today: 1042, quota: 1000 },
  { label: 'acct-5', hint: '...e51c', today: 1128, quota: 930 },
]
const LIMIT = 1000

type Status = 'active' | 'cooling' | 'exhausted'

function statusAt(i: number, t: number): Status {
  if (i === 1 && t >= COOL_AT && t < COOL_END) return 'cooling'
  if (i === 3 && t >= OUT_AT && t < BACK_AT) return 'exhausted'
  return 'active'
}

// Seconds left on a parked key's pill. Held at its opening value while the
// countdown is closed, and never shows 0:00 (the product drops it at zero).
function secondsAt(i: number, t: number): number {
  if (i === 1) return t < COOL_AT ? 8 : Math.max(1, Math.ceil((COOL_END - t) / 1000))
  if (i === 3) return t < OUT_AT ? Math.ceil((RESET_AT - OUT_AT) / 1000) : Math.max(1, Math.ceil((RESET_AT - t) / 1000))
  return 0
}

function quotaAt(i: number, t: number): number {
  if (i === 3 && t >= DRAIN_AT && t < RESET_AT) return 0
  return KEYS[i]!.quota
}

function tilesAt(t: number) {
  const at = t - TILE_LAG
  const st = KEYS.map((_, i) => statusAt(i, at))
  const cooling = st.filter((s) => s === 'cooling').length
  const exhausted = st.filter((s) => s === 'exhausted').length
  return { available: KEYS.length - cooling - exhausted, cooling, exhausted }
}

async function script(a: Act<S>) {
  let next = 0
  for (let t = 0; t <= LOOP; t += STEP) {
    if (t === COOL_AT) a.mark('cool')
    if (t === DRAIN_AT) a.mark('drain')
    if (t === COOL_END) a.mark('back')
    if (t === RESET_AT) a.mark('reset')
    // One request every TRAFFIC ms, round robin over the keys that can take it
    if (t % TRAFFIC === 0) {
      const open = KEYS.map((_, i) => i).filter((i) => statusAt(i, t) === 'active' && quotaAt(i, t) > 0)
      const pick = open[next++ % open.length]!
      a.put((s) => {
        const served = s.served ? [...s.served] : KEYS.map(() => 0)
        served[pick]! += 1
        return { t: t === LOOP ? 0 : t, served }
      })
    } else {
      a.put({ t: t === LOOP ? 0 : t })
    }
    if (t < LOOP) await a.wait(STEP)
  }
  // The last frame is the first: settle the clock so the seam sees it
  await a.set({ t: 0 })
}

const num = (n: number) => n.toLocaleString('en-US')
const clock = (n: number) => `0:${String(n).padStart(2, '0')}`
const LABEL: Record<Status, string> = { active: 'available', cooling: 'cooling', exhausted: 'out of quota' }

function Pill({ status, secs }: { status: Status; secs: number }) {
  return (
    <span className="acr-pill" data-status={status}>
      <span className="acr-dot" aria-hidden />
      <Roll variant="fade" align="start">{LABEL[status]}</Roll>
      <span className="acr-cd" data-open={status !== 'active' || undefined}>
        <span className="acr-cd-in">
          <Count value={secs} format={clock} duration={450} />
        </span>
      </span>
    </span>
  )
}

function Quota({ rem, wide }: { rem: number; wide: boolean }) {
  const pct = (rem / LIMIT) * 100
  const tone = pct > 40 ? 'ok' : pct > 10 ? 'low' : 'out'
  return (
    <div className="acr-quota" data-tone={tone}>
      <div className="acr-qtext">
        <span className="acr-qrem"><Roll align="end">{num(rem)}</Roll></span>
        <span>/ {num(LIMIT)}{wide ? ' searches' : ''}</span>
      </div>
      <div className="acr-bar"><i style={{ width: `${pct}%` }} /></div>
    </div>
  )
}

function Tile({ label, value, tone }: { label: string; value: number; tone: 'green' | 'amber' | 'none' }) {
  return (
    <div className="acr-tile" data-tone={value > 0 ? tone : 'none'}>
      <div className="acr-tile-label">{label}</div>
      <div className="acr-tile-value"><Count value={value} duration={500} /></div>
    </div>
  )
}

function Scene({ s, layout }: { s: S; layout: Layout }) {
  const wide = layout === 'wide'
  const tiles = tilesAt(s.t)
  return (
    <div className="acr-page">
      <div className="acr-tiles">
        <Tile label="Available" value={tiles.available} tone="green" />
        {wide && <Tile label="Busy" value={0} tone="none" />}
        <Tile label="Cooling" value={tiles.cooling} tone="amber" />
        <Tile label="Out of quota" value={tiles.exhausted} tone="amber" />
        {wide && <Tile label="Invalid" value={0} tone="none" />}
      </div>
      <div className="acr-card">
        {wide && (
          <div className="acr-row acr-head">
            <span>Key</span>
            <span>Status</span>
            <span>Quota</span>
            <span>Last used</span>
          </div>
        )}
        {KEYS.slice(0, wide ? KEYS.length : 4).map((k, i) => {
          const status = statusAt(i, s.t)
          const today = k.today + (s.served?.[i] ?? 0)
          const meta = (
            <span className="acr-meta">
              <span>{k.hint}</span>
              <span aria-hidden>·</span>
              <span className="acr-today"><Count value={today} duration={420} /><span>today</span></span>
            </span>
          )
          return wide ? (
            <div key={k.label} className="acr-row" data-status={status}>
              <div className="acr-key">
                <span className="acr-label">{k.label}</span>
                {meta}
              </div>
              <div><Pill status={status} secs={secondsAt(i, s.t)} /></div>
              <Quota rem={quotaAt(i, s.t)} wide />
              <div className="acr-used"><Text filler style={{ fontSize: 12 }}>just now</Text></div>
            </div>
          ) : (
            <div key={k.label} className="acr-row acr-row--compact" data-status={status}>
              <div className="acr-key">
                <span className="acr-label">{k.label}</span>
                {meta}
              </div>
              <div className="acr-side">
                <Pill status={status} secs={secondsAt(i, s.t)} />
                <Quota rem={quotaAt(i, s.t)} wide={false} />
              </div>
            </div>
          )
        })}
        <div className="acr-row acr-row--more" aria-hidden />
      </div>
    </div>
  )
}

export function RecoveryDemo({ theme = 'light' }: { theme?: 'light' | 'dark' }) {
  return (
    <Stage<S>
      width={960}
      height={380}
      compact={{ below: 600, width: 358, height: 340 }}
      theme={theme}
      initial={initial}
      poster={poster}
      script={script}
      className="acr"
    >
      {(s, _rect, layout) => <Scene s={s} layout={layout} />}
    </Stage>
  )
}
