import type { ReactNode } from 'react'
import { Bar, Caret, Count, Follow, Prompt, Roll, Stage, Window, type Act } from '../stageshow/kit'
import './HeroDemo.css'

// Acnologia's docs hero: one request through the key pool. An app's curl
// goes to the proxy; the first SerpAPI key answers 429 and parks itself as
// cooling; the pool moves on to the next key, which answers 200; the
// response comes back with X-Cogito-Attempts: 2 and acct-2's quota ticks
// down by one. No cursor: this is what the system does.
//
// Beats: the command is typed; the request lands on acct-1 (busy); 429, and
// acct-1 turns cooling with its countdown; the request moves to acct-2; 200,
// the quota rolls 812 to 811; the response scrolls into the terminal and
// holds while the countdown keeps time.
//
// The loop: the terminal is a conveyor. Its history above the prompt is the
// previous run of the same request, so when a fresh prompt scrolls in at the
// end the terminal looks exactly as it did at the start and snaps back
// unseen. The table's live cells dip out and back in as the next request is
// about to go.
//
// Real strings: the provider, the command, acct-1 and acct-2 with their
// hints, the two response codes, the statuses, acct-2's quota and the
// response. Everything else is placeholder.

export type HeroTheme = 'light' | 'dark'

type Status = 'available' | 'busy' | 'cooling'

type S = {
  /** The command as typed so far (the first line in compact) */
  typedA: string
  /** Compact only: the command's continuation line */
  typedB: string
  line2: boolean
  /** The command has been sent */
  sent: boolean
  /** Which key the request is on: 0 none, 1 acct-1, 2 acct-2 */
  at: 0 | 1 | 2
  k1: Status
  k2: Status
  r1: boolean
  r2: boolean
  quota: number
  used1: string
  used2: string
  /** acct-1's cooldown, in seconds */
  cd: number
  /** The response has printed */
  out: boolean
  /** A fresh prompt has scrolled in below the response */
  next: boolean
  /** The table's live cells are dipped for the reset */
  dim: boolean
}

const initial: S = {
  typedA: '',
  typedB: '',
  line2: false,
  sent: false,
  at: 0,
  k1: 'available',
  k2: 'available',
  r1: false,
  r2: false,
  quota: 812,
  used1: '1m ago',
  used2: '4m ago',
  cd: 3599,
  out: false,
  next: false,
  dim: false,
}

const URL = ':8080/proxy/serpapi/search.json?q=coffee'
const CMD = { wide: `curl -i ${URL}`, compact: ['curl -i \\', `  ${URL}`] as const }
const LIMIT = 1000

const POSTER: S = {
  ...initial,
  typedA: CMD.wide,
  typedB: CMD.compact[1],
  line2: true,
  sent: true,
  k1: 'cooling',
  k2: 'available',
  r1: true,
  r2: true,
  quota: 811,
  used1: 'just now',
  used2: 'just now',
  cd: 3597,
  out: true,
}

const mmss = (n: number) => `${Math.floor(n / 60)}:${String(n % 60).padStart(2, '0')}`
const quota = (n: number) => `${n.toLocaleString('en-US')} / ${LIMIT.toLocaleString('en-US')} searches`

async function script(a: Act<S>) {
  const compact = a.layout === 'compact'
  await a.wait(700)

  // 1. The app sends its request
  a.mark('type')
  if (compact) {
    await a.type('typedA', CMD.compact[0], { interval: 30 })
    a.put({ line2: true })
    await a.wait(160)
    await a.type('typedB', CMD.compact[1], { interval: 26 })
  } else {
    await a.type('typedA', CMD.wide, { interval: 28 })
  }
  await a.wait(300)

  // 2. The pool attaches acct-1
  a.mark('sent')
  a.put({ sent: true, at: 1, k1: 'busy' })
  await a.wait(950)

  // 3. SerpAPI says 429: acct-1 steps aside and starts cooling
  a.mark('429')
  a.put({ r1: true, k1: 'cooling', used1: 'just now' })
  await a.wait(900)

  // 4. The pool retries on acct-2
  a.mark('retry')
  a.put({ at: 2, k2: 'busy' })
  await a.wait(950)

  // 5. 200: acct-2 answers and spends one search
  a.mark('200')
  a.put({ r2: true, k2: 'available', quota: 811, used2: 'just now' })
  await a.wait(500)

  // The response reaches the app, and holds
  a.mark('response')
  a.put({ at: 0, out: true })
  for (let i = 0; i < 3; i++) {
    await a.wait(1000)
    a.put((s) => ({ cd: s.cd - 1 }))
  }
  await a.wait(700)

  // The next request is coming: a fresh prompt scrolls in, the codes leave,
  // the live cells dip, and everything resets while the terminal looks the
  // same as it did at the start
  a.mark('reset')
  a.put({ next: true, r1: false, r2: false, dim: true })
  await a.wait(300)
  await a.snap({ ...initial, dim: true })
  await a.wait(40)
  a.put({ dim: false })
  await a.wait(400)
}

export function HeroDemo({ theme = 'light' }: { theme?: HeroTheme }) {
  return (
    <Stage<S>
      width={720}
      height={440}
      compact={{ below: 600, width: 358, height: 400 }}
      theme={theme}
      initial={initial}
      poster={POSTER}
      script={script}
      className="ah"
    >
      {(s, rect, layout) => {
        const compact = layout === 'compact'
        const card = rect('table')
        const row = s.at ? rect(`row-${s.at}`) : null
        const on = row && card.width ? { x: row.x - card.x + 4, y: row.y - card.y + 3, width: row.width - 8, height: row.height - 6 } : null
        return (
          <>
            <Window url="localhost:8080/services/serpapi" className="ah-win">
              <div className="ah-page">
                <div className="ah-head">
                  <span className="ah-name">SerpAPI</span>
                  <Bar w={compact ? 48 : 84} h={6} tone={3} />
                </div>
                <div className="ah-table" data-target="table">
                  <Follow to={on} className="ah-on" stiffness={0.12} />
                  {!compact && (
                    <div className="ah-row ah-row--head">
                      <span>Key</span>
                      <span>Status</span>
                      <span>Quota</span>
                      <span>Last used</span>
                    </div>
                  )}
                  <KeyRow
                    n={1}
                    label="acct-1"
                    hint="...a1f3"
                    code={{ text: '429', tone: 'err', on: s.r1 }}
                    status={s.k1}
                    cd={s.cd}
                    quota={<span className="ah-q-text">{quota(640)}</span>}
                    frac={640 / LIMIT}
                    used={s.used1}
                    dim={s.dim}
                  />
                  <KeyRow
                    n={2}
                    label="acct-2"
                    hint="...7c0e"
                    code={{ text: '200', tone: 'ok', on: s.r2 }}
                    status={s.k2}
                    cd={0}
                    quota={
                      <span className="ah-q-text">
                        <Count value={s.quota} />
                        <span className="ah-of">/ 1,000 searches</span>
                      </span>
                    }
                    frac={s.quota / LIMIT}
                    used={s.used2}
                    dim={s.dim}
                  />
                  {[
                    [52, 44, 0.9],
                    [46, 36, 0.35],
                    [58, 40, 0.7],
                    [50, 48, 0.55],
                  ].map(([w, q, f], i) => (
                    <div key={i} className="ah-row ah-row--filler">
                      <span className="ah-key">
                        <Bar w={w} h={7} tone={3} />
                        <Bar w={34} h={6} tone={2} />
                      </span>
                      <Bar w={compact ? 64 : 70} h={18} r={9} tone={2} />
                      {!compact && (
                        <span className="ah-quota">
                          <Bar w={q + 60} h={6} tone={2} />
                          <span className="ah-meter">
                            <i style={{ width: `${f * 100}%` }} data-tone="filler" />
                          </span>
                        </span>
                      )}
                      {!compact && <Bar w={36} h={6} tone={2} />}
                    </div>
                  ))}
                </div>
              </div>
            </Window>

            <Window chrome="terminal" lights="grey" className="ah-term" body={{ className: 'ah-term-body' }}>
              <Terminal s={s} compact={compact} />
            </Window>
          </>
        )
      }}
    </Stage>
  )
}

function KeyRow({
  n,
  label,
  hint,
  code,
  status,
  cd,
  quota,
  frac,
  used,
  dim,
}: {
  n: 1 | 2
  label: string
  hint: string
  code: { text: string; tone: 'ok' | 'err'; on: boolean }
  status: Status
  cd: number
  quota: ReactNode
  frac: number
  used: string
  dim: boolean
}) {
  return (
    <div className="ah-row" data-target={`row-${n}`}>
      <span className="ah-key">
        <span className="ah-label">
          {label}
          <span className="ah-code" data-tone={code.tone} data-on={code.on || undefined}>
            {code.text}
          </span>
        </span>
        <span className="ah-hint">{hint}</span>
      </span>
      <span className="ah-live" data-dim={dim || undefined}>
        <span className="ah-pill" data-state={status}>
          <i className="ah-dot" />
          {/* Dipped, the words are plain text, so the reset lands
              without rolling back while nobody can see it */}
          {dim ? <span>{status}</span> : <Roll>{status}</Roll>}
          <span className="ah-cd" data-on={status === 'cooling' || undefined}>
            <Count value={cd} format={mmss} duration={500} />
          </span>
        </span>
      </span>
      <span className="ah-quota ah-live" data-dim={dim || undefined}>
        {quota}
        <span className="ah-meter">
          <i style={{ width: `${frac * 100}%` }} />
        </span>
      </span>
      <span className="ah-used ah-live" data-dim={dim || undefined}>
        {dim ? <span>{used}</span> : <Roll>{used}</Roll>}
      </span>
    </div>
  )
}

// The terminal is a track of lines, bottom aligned in a fixed window and
// scrolled by whole lines: the previous run of the request, the prompt being
// typed, its response, and the next prompt
function Terminal({ s, compact }: { s: S; compact: boolean }) {
  const cmdLines = compact ? 2 : 1
  const history = cmdLines + 3
  const visible = compact ? 5 : 4
  // The last line showing: the prompt (its first line until the second is
  // typed), then the response's last line, then the next prompt
  const last = s.next ? history + cmdLines + 3 : s.out ? history + cmdLines + 2 : history + (compact && s.line2 ? 1 : 0)
  const shift = Math.max(0, last + 1 - visible)
  const typing = !s.sent && !s.out
  return (
    // The reset swaps identical pictures: the snap from the fresh prompt back
    // to the opening is deliberate and invisible
    <div className="ah-track" data-snap style={{ transform: `translateY(calc(var(--lh) * ${-shift}))` }}>
      <Command compact={compact} a={compact ? CMD.compact[0] : CMD.wide} b={CMD.compact[1]} />
      <Response compact={compact} />
      <Command compact={compact} a={compact && s.line2 ? CMD.compact[0] : s.typedA} b={s.typedB} caret={!s.out && !s.next ? (compact && s.line2 ? 'b' : 'a') : null} still={typing && s.typedA.length > 0} />
      <Response compact={compact} on={s.out} />
      <Prompt className="ah-line" data-on={s.next || undefined}>
        <Caret />
      </Prompt>
    </div>
  )
}

function Command({ compact, a, b, caret = null, still }: { compact: boolean; a: string; b: string; caret?: 'a' | 'b' | null; still?: boolean }) {
  return (
    <>
      <Prompt className="ah-line">
        {a}
        {caret === 'a' && <Caret still={still} />}
      </Prompt>
      {compact && (
        <Prompt className="ah-line ah-cont">
          {b}
          {caret === 'b' && <Caret still={still} />}
        </Prompt>
      )}
    </>
  )
}

function Response({ compact, on = true }: { compact: boolean; on?: boolean }) {
  return (
    <div className="ah-out" data-on={on || undefined}>
      <div className="ah-line">
        HTTP/1.1 <span className="ok">200 OK</span>
      </div>
      <div className="ah-line">
        <span className="ah-strong">X-Cogito-Attempts:</span> <span className="ah-attempts">2</span>
      </div>
      <div className="ah-line ah-json">{compact ? '{"organic_results": [{"title": "Blue Bottle' : '{"organic_results": [{"title": "Blue Bottle Coffee", "link"'}</div>
    </div>
  )
}
