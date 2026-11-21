## OpenQodex before main
- Pushes to `stage` or any branch other than `main` get no OpenQodex review and no question about one: just push.
- Before every push to `main`, ask the developer casually and name OpenQodex ("Want an OpenQodex pass on the new
  commits before main?"). Never push to `main` without that question. On a no, push.
- On a yes, run `npx -y openqodex@0.19.0 review --base origin/main` from the repository root, in the background (it can
  take minutes, and OpenQodex stops the reviewer after 30 minutes). Review only commits not on `main` yet.
- Show the receipt: the verdict, one line per finding and the absolute path of `report.html`. Ask: "Fix all, or tell
  me which?" Fix only the findings they name (`npx -y openqodex@0.19.0 findings 1,3`). Do not push on a blocked verdict
  unless the developer says so after seeing the findings.
- No hooks: no push gate and no git pre-push hook (removed on 2026-11-22). Never install one.
- Never add a file to `review.instructions_files` on your own. Ask the developer which files the reviewer may use, then
  set only what they name. The report is in `.openqodex/reviews/`.
