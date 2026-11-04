import { Hash } from 'lucide-react'
import { Bar, Collapse, Count, Roll, Stage, Window, type Act } from '../stageshow/kit'
import './AlertsDemo.css'

// "Find out before your users do." A system story, no cursor. SerpAPI key
// acct-3 burns through its quota in Acnologia's keys table and turns "out of
// quota"; a beat later the matching alert lands in #ops-alerts. Then the
// quota resets, the key fills back up and a second alert says it works
// again. The table is the cause, the channel the consequence, and the loop
// ends where it began: acct-3 full, the two alerts leaving the channel.
// Narrow, the service page runs off the bottom with the key's card from the
// dashboard's own phone layout, and the channel comes up over its lower half,
// edge on the card's edges, keeping only the latest older message.

// The dragon mark at 96px (a 30px avatar on a 3x screen), served from web/public
const MARK = '/mark-96.webp'
const LIMIT = 1000

const OUT = 'Acnologia: SerpAPI key "acct-3" is out of quota'
const BACK = 'Acnologia: SerpAPI key "acct-3" works again'

type Status = 'available' | 'exhausted'
type S = { rem: number; status: Status; out: boolean; back: boolean }

const START: S = { rem: LIMIT, status: 'available', out: false, back: false }
const POSTER: S = { rem: 0, status: 'exhausted', out: true, back: false }

async function script(a: Act<S>) {
  await a.wait(900)
  // The key gets used: the quota runs down in a few strides
  a.mark('drain')
  for (const rem of [640, 290, 84]) {
    await a.set({ rem })
    await a.wait(600)
  }
  await a.set({ rem: 0 })
  await a.wait(560)
  // Acnologia parks it
  a.mark('exhausted')
  await a.set({ status: 'exhausted' })
  await a.wait(700)
  // A beat later the webhook lands in the channel
  a.mark('alert')
  await a.set({ out: true })
  await a.wait(3000)
  // The quota resets: the bar fills, the key is back, and the channel hears it
  a.mark('reset')
  await a.set({ rem: LIMIT })
  await a.wait(500)
  await a.set({ status: 'available' })
  await a.wait(700)
  a.mark('back')
  await a.set({ back: true })
  await a.wait(2600)
  // The story's two alerts leave, newest first, around the older message
  a.mark('exit')
  await a.set({ back: false })
  await a.wait(80)
  await a.set({ out: false })
  await a.wait(250)
}

const fmt = (n: number) => Math.round(n).toLocaleString('en-US')

function Pill({ status }: { status: Status }) {
  return (
    <span className="aa-pill" data-status={status}>
      <span className="aa-pill__dot" aria-hidden />
      <Roll variant="fade">{status === 'exhausted' ? 'out of quota' : 'available'}</Roll>
    </span>
  )
}

function Quota({ rem, live, count }: { rem: number; live?: boolean; count?: boolean }) {
  const pct = (rem / LIMIT) * 100
  const tone = pct > 40 ? 'ok' : pct > 10 ? 'mid' : 'low'
  return (
    <div className="aa-quota" data-tone={tone}>
      <div className="aa-quota__text">
        {live || count ? (
          <>
            <Count className="aa-quota__rem" value={rem} format={fmt} duration={380} stagger={30} />
            <span>/ 1,000</span>
            {live && (
              <span className="aa-quota__low" data-on={pct < 10 || undefined}>
                low
              </span>
            )}
          </>
        ) : (
          <Bar w={58} h={5} tone={3} />
        )}
      </div>
      <div className="aa-meter">
        <div className="aa-meter__fill" style={{ width: `${pct}%` }} />
      </div>
    </div>
  )
}

// The key's hint line (a masked secret, in mono), as filler in its own line box
function Hint({ w }: { w: number }) {
  return (
    <span className="aa-key__hint">
      <Bar w={w} h={5} tone={2} />
    </span>
  )
}

// The neighbor: a real name, state and count, so the eye compares like with like
const ROWS = [{ name: 'acct-2', hint: 52, rem: 458 }]

function KeysTable({ s }: { s: S }) {
  return (
    <div className="aa-table">
      <div className="aa-thead">
        <span>Key</span>
        <span>Status</span>
        <span>Quota</span>
      </div>
      {ROWS.map((r) => (
        <div key={r.name} className="aa-tr">
          <div className="aa-key">
            <span className="aa-key__name">{r.name}</span>
            <Hint w={r.hint} />
          </div>
          <Pill status="available" />
          <Quota rem={r.rem} count />
        </div>
      ))}
      <div className="aa-tr" data-target="row">
        <div className="aa-key">
          <span className="aa-key__name">acct-3</span>
          <Hint w={48} />
        </div>
        <Pill status={s.status} />
        <Quota rem={s.rem} live />
      </div>
      {[
        [44, 56],
        [50, 40],
      ].map(([a, b], i) => (
        <div key={i} className="aa-tr">
          <div className="aa-key">
            <span className="aa-key__line">
              <Bar w={a} h={6} tone={3} />
            </span>
            <Hint w={b} />
          </div>
          <Bar w={72} h={20} r={10} tone={1} />
          <div />
        </div>
      ))}
    </div>
  )
}

// The phone layout of the same table: one card per key
function KeyCard({ s }: { s: S }) {
  return (
    <div className="aa-card">
      <div className="aa-key">
        <span className="aa-key__name">acct-3</span>
        <Hint w={60} />
      </div>
      <Pill status={s.status} />
      <Quota rem={s.rem} live />
    </div>
  )
}

function Message({ head, time, text }: { head?: boolean; time?: string; text: string }) {
  return (
    <div className="aa-msg" data-head={head || undefined}>
      {head ? <img className="aa-msg__av" src={MARK} alt="" draggable={false} /> : <span className="aa-msg__av" />}
      <div className="aa-msg__body">
        {head && (
          <div className="aa-msg__meta">
            <span className="aa-msg__name">Acnologia</span>
            <span className="aa-msg__time">{time}</span>
          </div>
        )}
        <div className="aa-msg__text">{text}</div>
      </div>
    </div>
  )
}

function Channel({ s, compact }: { s: S; compact?: boolean }) {
  return (
    <section className="aa-channel">
      <div className="aa-channel__head">
        <Hash size={15} strokeWidth={2} className="aa-channel__hash" aria-hidden />
        <span>ops-alerts</span>
      </div>
      <div className="aa-feed">
        {compact ? (
          <Message head time="8:15 AM" text="Acnologia: ElevenLabs answers again" />
        ) : (
          <>
            <Message head time="8:12 AM" text="Acnologia: ElevenLabs keeps failing (503: system busy), Acnologia paused traffic to it" />
            <Message text="Acnologia: ElevenLabs answers again" />
          </>
        )}
        <Collapse open={s.out} reveal="drop" from="below">
          <Message head time="9:41 AM" text={OUT} />
        </Collapse>
        <Collapse open={s.back} reveal="drop" from="below">
          <Message text={BACK} />
        </Collapse>
      </div>
    </section>
  )
}

export function AlertsDemo({ theme = 'light' }: { theme?: 'light' | 'dark' }) {
  return (
    <Stage<S>
      width={720}
      height={380}
      compact={{ below: 600, width: 358, height: 440 }}
      theme={theme}
      initial={START}
      poster={POSTER}
      script={script}
      className="aa"
    >
      {(s, _rect, layout) => (
        <>
          {layout === 'compact' ? (
            <>
              <div className="aa-panel">
                <div className="aa-title">SerpAPI</div>
                <KeyCard s={s} />
              </div>
              <Channel s={s} compact />
            </>
          ) : (
            <>
              <Window url="acnologia.local/serpapi" className="aa-window">
                <div className="aa-page">
                  <div className="aa-head">
                    <div className="aa-title">SerpAPI</div>
                    <div className="aa-sub">
                      <Bar w={44} h={5} tone={3} />
                      <Bar w={86} h={5} tone={2} />
                    </div>
                  </div>
                  <KeysTable s={s} />
                </div>
              </Window>
              <Channel s={s} />
            </>
          )}
        </>
      )}
    </Stage>
  )
}
