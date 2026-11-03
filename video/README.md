# Acnologia product video

A 48 second product film (1920x1080, 30 fps) built with
[HyperFrames](https://hyperframes.heygen.com): HTML scenes animated with GSAP
and rendered frame by frame to MP4.

The film shows only Acnologia's own name and logo. Provider and vendor names
are left out on purpose: the upstream is "Any API", keys and tokens are
illustrative masked strings, and the app rows use neutral service counts.

## Files

| Path | What it is |
| --- | --- |
| `index.html` | Root composition: mounts the scenes, the captions and the audio |
| `compositions/*.html` | One scene per file: `problem`, `reveal`, `flow`, `rotate`, `share`, `mcp`, `endcard`, plus `captions` |
| `assets/logo.png` | The Acnologia logo (copy of `docs/brand/logo.png`) |
| `assets/apps-header.png` | The header strip of `docs/screenshots/apps.png` (nav and "Apps" title only); the app rows under it are rebuilt in HTML with neutral names |
| `assets/voiceover.mp3` | The narration, loudness normalised |
| `assets/voiceover-words.json` | Per word start and end times from the voiceover request |
| `assets/click.mp3` | The one sound effect, on the Revoke click |
| `scripts/build-captions.py` | Writes `compositions/captions.html` from the word timings |
| `acnologia.mp4` | Full render |
| `acnologia-web.mp4` | 1280x720 H.264 + AAC with faststart, for the landing page |
| `poster.jpg` | Poster still (the end card) |

## Re-render

Needs Node and ffmpeg. The CLI is pinned in `package.json`.

```sh
cd video
npx hyperframes check                 # lint, layout and timing checks
npx hyperframes preview               # Studio preview in the browser
npx hyperframes render --output acnologia.mp4 --fps 30 --quality delivery

# Landing page version
ffmpeg -y -i acnologia.mp4 -vf scale=1280:720:flags=lanczos \
  -c:v libx264 -profile:v high -pix_fmt yuv420p -preset slow -crf 20 \
  -c:a aac -b:a 128k -ac 2 -movflags +faststart acnologia-web.mp4

# Poster
ffmpeg -y -ss 46 -i acnologia.mp4 -frames:v 1 -q:v 3 poster.jpg
```

If you change the voiceover, rebuild the captions with
`python3 scripts/build-captions.py` and retime the scene hosts in `index.html`
(each scene's `data-start` sits on the word that opens it; the times inside a
scene are local to that scene). The voiceover starts 0.5 s into the film.

## How the voiceover was made

The narration was generated with ElevenLabs text to speech, called through an
Acnologia server's proxy, so the ElevenLabs key never left that server: the
request carried only an `acn_` app token.

1. `GET /proxy/elevenlabs/v2/voices?page_size=20` to list voices. The voice is
   **River** (`SAz9YHcvj6GT2YYXdXww`: relaxed, neutral, calm, American).
2. `POST /proxy/elevenlabs/v1/text-to-speech/SAz9YHcvj6GT2YYXdXww/with-timestamps?output_format=mp3_44100_128`
   with `model_id: eleven_multilingual_v2`, voice settings stability 0.55,
   similarity 0.75, style 0.1, speaker boost on, and the script below. The
   response's `audio_base64` was decoded to `assets/voiceover.mp3` and its
   character `alignment` was grouped into words for
   `assets/voiceover-words.json`.
3. The audio was normalised with
   `ffmpeg -af loudnorm=I=-16:TP=-1.5:LRA=11`.
4. The click is one `POST /proxy/elevenlabs/v1/sound-generation` call with
   the text "single soft UI toggle switch click, clean, dry" (0.8 s).

Each request sent `Authorization: Bearer <acn_ token>`; no token is stored
anywhere in this folder.

### Script (654 characters)

> Your API keys end up everywhere. Pasted into every app, every agent, every
> teammate's laptop. And once a key leaks, you can't take it back.
>
> Acnologia is one server that holds your keys. Everyone else gets their own
> token.
>
> Your app calls Acnologia. Acnologia attaches the real key on the way out,
> and the API answers. The key never leaves your server. When a key hits a
> rate limit, the next one takes over.
>
> Share a service with another server, a teammate, or anyone with a link.
> Then revoke it in one click, without touching the key.
>
> AI agents connect over MCP, and use your services without ever seeing a
> key.
>
> Acnologia. Self host it. MIT licensed.

## Scene timing (film seconds)

| Scene | Start | Opens on |
| --- | --- | --- |
| problem | 0.0 | "Your API keys end up everywhere." |
| reveal | 9.9 | "Acnologia is one server..." |
| flow | 15.3 | "Your app calls Acnologia." |
| rotate | 24.5 | "When a key hits a rate limit..." |
| share | 27.8 | "Share a service..." (Revoke click at 33.4) |
| mcp | 35.3 | "AI agents connect over MCP..." |
| endcard | 40.6 | "Acnologia. Self host it. MIT licensed." |
