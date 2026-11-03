#!/usr/bin/env python3
"""Write compositions/captions.html from assets/voiceover-words.json.

The word timings come from the ElevenLabs with-timestamps response (see README).
Times in the JSON are relative to the start of voiceover.mp3; the voiceover
starts OFFSET seconds into the film. Captions stop before the end card, which
shows its own words.
"""
import html
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OFFSET = 0.5
STOP_AT = 40.6  # end card starts here (film time)

words = json.loads((ROOT / "assets/voiceover-words.json").read_text())

# Sentences, then clauses at commas; long clauses split in half, short
# neighbouring clauses merged back up to MERGE_TO words.
MERGE_TO = 7
LONG = 9
spoken = [w for w in words if w["start"] + OFFSET < STOP_AT]
sentences, cur = [], []
for w in spoken:
    cur.append(w)
    if w["text"][-1] in ".?!":
        sentences.append(cur)
        cur = []
if cur:
    sentences.append(cur)

groups = []
for sent in sentences:
    if len(sent) <= LONG:
        groups.append(sent)
        continue
    clauses, cur = [], []
    for w in sent:
        cur.append(w)
        if w["text"][-1] == ",":
            clauses.append(cur)
            cur = []
    if cur:
        clauses.append(cur)
    merged = []
    for c in clauses:
        if merged and len(merged[-1]) + len(c) <= MERGE_TO:
            merged[-1] = merged[-1] + c
        else:
            merged.append(c)
    for c in merged:
        if len(c) > LONG:
            h = (len(c) + 1) // 2
            groups += [c[:h], c[h:]]
        else:
            groups.append(c)

items = []
for gi, g in enumerate(groups):
    start = round(g[0]["start"] + OFFSET - 0.05, 3)
    nxt_start = groups[gi + 1][0]["start"] + OFFSET - 0.05 if gi + 1 < len(groups) else STOP_AT
    end = min(g[-1]["end"] + OFFSET + 0.35, nxt_start, STOP_AT)
    dur = round(end - start, 3)
    text = html.escape(" ".join(w["text"] for w in g))
    items.append(
        f'    <div id="cap-{gi}" class="cap clip" data-start="{start}" data-duration="{dur}" '
        f'data-track-index="0"><span>{text}</span></div>'
    )

out = f"""<template>
  <style>
    #root {{
      position: absolute;
      inset: 0;
      pointer-events: none;
    }}
    .cap {{
      position: absolute;
      left: 0;
      right: 0;
      bottom: 84px;
      text-align: center;
      overflow: visible;
    }}
    .cap span {{
      font-family: var(--sans);
      font-size: 36px;
      font-weight: 500;
      color: #3d3c39;
      letter-spacing: -0.005em;
    }}
  </style>
  <div id="root" data-composition-id="captions" data-width="1920" data-height="1080">
{chr(10).join(items)}
  </div>
  <script>
    (function () {{
      const tl = gsap.timeline({{ paused: true }});
      document.querySelectorAll(".cap").forEach((el) => {{
        const s = parseFloat(el.dataset.start);
        const d = parseFloat(el.dataset.duration);
        const span = el.querySelector("span");
        tl.fromTo(span, {{ opacity: 0 }}, {{ opacity: 1, duration: 0.12, ease: "power2.out" }}, s);
        tl.to(span, {{ opacity: 0, duration: 0.1, ease: "power2.in" }}, s + d - 0.1);
      }});
      window.__timelines["captions"] = tl;
    }})();
  </script>
</template>
"""
(ROOT / "compositions/captions.html").write_text(out)
print(f"{len(groups)} caption groups")
for g in groups:
    print(round(g[0]["start"] + OFFSET, 2), " ".join(w["text"] for w in g))
