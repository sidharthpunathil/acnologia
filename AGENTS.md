## OpenQodex: only when asked, at the end of a project
- Never ask about an OpenQodex review and never run one, for any push to any branch, `main` included: just push.
- The developer runs one complete scan when the project ends. Run it only when they ask, as they say (the whole
  change, or `npx -y openqodex@0.19.0 review --all`), in the background (it can take a long time; OpenQodex stops the
  reviewer after 30 minutes). Show the receipt (verdict, one line per finding, the absolute path of `report.html`) and
  ask "Fix all, or tell me which?"
- No hooks: no push gate and no git pre-push hook (removed on 2026-11-22). Never install one.
- Never add a file to `review.instructions_files` on your own. Ask the developer which files the reviewer may use, then
  set only what they name. The report is in `.openqodex/reviews/`.
