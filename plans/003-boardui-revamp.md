# 003 BoardUI revamp: component plan for four page agents

Status: ready to build. Read only research, no code changed.
Sources read: BoardUI free repo at `~/.opensrc/repos/github.com/BoardUI/boardui/main` (AGENTS.md, components, styles), the live catalog at boardui.com/components (tiers), `web/src` (pages, components, styles), and the baseline screenshots in `scratchpad/review/baseline/` (catalog, service keys, assistant dark, usage, provider editor, keys phone, doc flows).

What is already in `web/` (from the in flight React 19 and Tailwind 4 migration):
`web/AGENTS.md` (BoardUI rules), `src/styles/{globals,theme,typography}.css` (BoardUI), `src/styles/acnologia.css` (violet accent, warm neutrals, legacy alias utilities), `src/utils/cx.ts`, the `@/` alias in vite and tsconfig, `@remixicon/react`, `react-aria-components`, `motion`, `tailwind-merge`, and these BoardUI components under `src/components/`:

- base: avatar, badges/{badge,chip,status-dot}, buttons/{button,button-group,close-button,icon-button,link-button}, checkbox/{checkbox,checkbox-glyph}, combobox, divider, drawer, dropdown/{dropdown,menu-styles}, input/{input,label,hint-text}, kbd, modal, notification, popover, progress, radio, search-field, segmented-control, select, skeleton, spinner, switch, table, tabs/{tabs,pill-tab}, textarea, toast, tooltip
- application: theme/theme-toggle (do not use, see 1.9)
- foundations: direction, icons/chevrons, scroll-fade, template-copy

Note: the cloned GitHub repo is older than the catalog. Modal, Drawer, Popover, Toast, Combobox, Search Field, Skeleton, Spinner, Progress, Accordion, Meter, Number Field, Tag Group, Color Picker, Context Menu, Command Palette and Time Field are all **free** on boardui.com even though the repo lacks them. Install with `npx boardui@latest add <name>`.

---

## 1. Global rules digest

1. **Components first.** Look in `@/components/base/*` and `@/components/application/*` before writing markup. Missing and free: area A installs it (`npx boardui@latest add <name>`). Missing and PRO: compose from free parts (section 2 says how). Never write a lookalike of an installed component.
2. **Tokens only.** Text `text-text-{primary,secondary,tertiary,placeholder,error-primary}`. Surfaces `bg-background-full` (page), `bg-background-primary-default` (cards), `bg-background-secondary-default` and `-hover` (soft rows), `bg-background-tertiary-default` (wells). Borders `border-border-button-default` and `-hover`, hairlines `border-separator-border`, tables `border-border-table`, errors `border-border-error-default`. Icons `text-foreground-icon-{primary..quaternary}`. Status: `bg-status-{rose,lime,yellow,orange,blue,cyan,purple}-background` with the matching `text-status-*-text`, or Chip and StatusDot. Accent: `accent-50..950` (Acnologia violet, 600 light brand, 300 dark brand). Charts: `chart-1..5` and `-active`. No raw palette classes, no hex or oklch in TSX, no `dark:` color overrides. The legacy aliases (`bg-canvas`, `bg-surface`, `text-ink`, `text-muted`, `text-faint`, `border-line`, `bg-brand`, `bg-spark`, `bg-red-50`...) still render, but a migrated page uses zero of them.
3. **Type.** Composite utilities only: page title `text-title-1-medium` (24/34), section title `text-headline-semibold` (16/22), body `text-body-regular` and `text-body-medium` (14/20), dense rows `text-body-2-*` (13/18), meta and table heads `text-caption-1-*` (12/16), eyebrows `text-caption-2-semibold uppercase`. Acnologia keeps its mono voice: add `font-mono` on top of a composite utility for page titles, numbers, ids, slugs, key tails and timestamps (as in the baseline). Never stack `text-sm font-medium leading-5`.
4. **Spacing and shape.** Tailwind scale, `gap` over margins. Page column stays `max-w-[1076px] px-4 sm:px-6`. Cards and panels `rounded-3xl border border-border-button-default bg-background-primary-default` (this replaces the 14px `rounded-card`). Inputs, menu rows `rounded-md` to `rounded-xl`. Pills and chips `rounded-full`.
5. **Icons.** `@remixicon/react` only, passed as component refs (`leadingIcon={RiAddLine}`, `icon={RiKey2Line}`). `lucide-react` is removed page by page; A deletes the dependency last. Nav map: Catalog `RiLayoutGridLine`, Keys `RiKey2Line`, Assistant `RiChat3Line`, Providers `RiPlugLine`, Apps `RiStackLine`, Activity `RiPulseLine`, Usage `RiBarChartBoxLine`, Groups `RiGitBranchLine`, Webhooks `RiWebhookLine`, Egress `RiGlobalLine`, Settings `RiSettings3Line`, Users `RiTeamLine`, Audit `RiShieldCheckLine`, Docs `RiBookOpenLine`, Account `RiUser3Line`, Sign out `RiLogoutBoxRLine`, More `RiMoreLine`.
6. **Motion.** BoardUI components carry their own motion (modal scale and blur, toast spring, tick). Page level motion keeps `plans/001-motion-pass.md`: `--ease-out` and `--ease-in-out`, 150 to 300ms, transform and opacity only. Every custom animation has a `prefers-reduced-motion: reduce` path (fade or none). `motion/react` uses `useReducedMotion()`.
7. **Focus.** `outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring` on anything custom and focusable. RAC components already do this. Keep the skip link and the focus move to `<main>` on navigation.
8. **Inner scroll blur.** Any inner vertical scroll region (account menu, More sheet body, Chats dialog list, doc sidebar and TOC, drawer bodies, long selects) uses `useScrollFade` and `<ScrollFade>` from `@/components/foundations/scroll-fade/scroll-fade`. Headers stay outside the scroller. Overlay surface class matches the enclosing surface (`from-background-primary-default` on cards, `from-background-full` on the page). Not on the page scroll itself.
9. **Forms.** RAC based BoardUI fields (`Input`, `Textarea`, `Select`, `ComboBox`, `Switch`, `Checkbox`, `Radio`, `SegmentedControl`) with their own `label`, `hint`, `errorMessage`. No raw `<input>` or `<select>` in pages. Pending buttons: `isDisabled` plus `leadingIcon={ButtonSpinner}` (A exports it, see area A).
10. **cx.** `cx()` from `@/utils/cx` everywhere. The `cx` in `components/ui.tsx` is retired.
11. **Routing.** A wraps the app in RAC `RouterProvider` (`navigate` from react-router, `useHref`) in `main.tsx`, so `href` on RAC `Link`, `Tab`, `MenuItem`, `Button`-as-link routes client side.

**Acnologia decisions that stay:**
- **Top bar, not a sidebar.** Primary nav is Catalog, Keys, Assistant. Everything else lives in the account menu, grouped Workspace (Providers, Apps, Activity, Usage), Manage (Service groups, Webhooks, Egress proxies), Admin (Settings, Users, Audit, role gated), Help (Docs). The dot on the account pill when a menu page is open stays. BoardUI's Sidebar and App Shell are not used.
- **Phone:** bottom tab bar (Catalog, Keys, Assistant, More); More opens a bottom sheet with the same groups, theme, account and sign out. Tab bar hides while typing (`data-typing`).
- **Violet accent** through `accent-*` in `acnologia.css` (A owns; nobody else edits theme files). Warm neutrals stay.
- **Dragon mark** (`Mark`, `Brand`, `Logo`) is the logo everywhere: top bar, auth, empty assistant, docs header. BoardUI `foundations/brand/logo` is not installed.
- **Theme picker** keeps System, Light, Dark (stores/theme.ts sets `.dark` and `data-theme`). BoardUI `theme-toggle` is light and dark only and writes its own storage key, so it is not mounted.
- **Stageshow demos** (`src/stageshow/**`, `src/demos/**`) on `/docs` stay as they are. Only their host cards on the docs landing change.
- Footer line (`Acnologia · I think, therefore I am.`) stays.

**PRO is out.** No license. Never install or paste: AI Chat, Home Dashboard and every other template except Chat Starter; Composer, Composer Panel, Composer Attachments, Questionnaire, Task List, Agent Progress, Agent Limits, Agent Creator, Web Search, Calendar, and every chart card except Orders Chart and Revenue Chart. Free stand ins are named per row below.

---

## 2. Work areas

Legend: **Free** = install or already installed. **PRO** = composed from the free parts listed. Paths are relative to `web/src`. `acn/` means `components/acn/`, a new folder area A creates for Acnologia composites built only from BoardUI parts.

### Area A: shell, primitives, admin

**A0, land first (unblocks B, C, D).** Order matters.

1. Install free items: `accordion breadcrumb checkbox-card radio-card switch-card color-picker meter number-field tag-group file-upload tree-view pagination context-menu stat-cards orders-chart-card revenue-chart-card use-count-up auth-card input-otp social-button agent-thinking agent-log composer-loader settings-modal` (keep only `settings-rows.tsx` from it if the rest pulls unwanted art) and the agent chat pieces `agent-chat-message.tsx`, `agent-composer.tsx`, `agent-chat-history.tsx` copied from the free Chat Starter (not `agent-chat.tsx`, which needs `ai`, `@ai-sdk/react` and Next). Add `recharts`. Remove `next/*` imports from anything installed (history uses `next/navigation`, swap to react-router).
2. `main.tsx`: RAC `RouterProvider`. Mount BoardUI `<Toaster />` from `base/toast/toast` in `App.tsx`.
3. Export `ButtonSpinner` from `base/spinner/spinner.tsx` (lift it out of `base/modal/modal.tsx`). Add `hint` and `errorMessage` to `base/select/select.tsx` to match Input.
4. Build `acn/*` composites (list below) and turn `components/ui.tsx`, `Modal.tsx`, `Tabs.tsx`, `Toaster.tsx`, `ConfirmButton.tsx`, `CopyButton.tsx` into thin adapters over BoardUI that keep their current props. Every unmigrated page instantly looks right, and B, C, D migrate call sites to direct imports at their own pace. The adapters are deleted in A's last step once grep shows no importers.

**Shell**

| Page / section | Current | BoardUI replacement | Tier | Notes |
|---|---|---|---|---|
| Top bar | `Layout.tsx` header, NavLink pills | `acn/top-bar.tsx`: RAC `Link` pills styled with tokens; `acn/brand.tsx` (Mark, Brand, Logo) | Free (compose) | Sticky, `bg-background-full/80 backdrop-blur-sm`, hairline `border-separator-border`. Active pill `bg-background-tertiary-default`. |
| Account menu | hand rolled popover in `Layout.tsx` | `base/dropdown/dropdown` (`Dropdown`, `DropdownTrigger`, `DropdownPopover`, `DropdownGroup`, `DropdownItem`, `DropdownDivider`), pattern from `application/dashboard/dashboard-user-menu` `AccountMenuContent` | Free | Header row (name, email, role), groups Workspace, Manage, Admin, Help, then Theme (`acn/theme-picker`), Your account, Sign out. Items navigate via `href`. Popover body gets ScrollFade. Trigger stays the dark pill with the active dot (`bg-accent-600`). |
| Theme picker | `ThemePicker.tsx` | `base/segmented-control/segmented-control` (`SegmentedControl`, `SegmentedControlItem`) with `RiComputerLine`, `RiSunLine`, `RiMoonLine` | Free | Moves to `acn/theme-picker.tsx`. |
| Phone tab bar | fixed nav in `Layout.tsx` | `acn/tab-bar.tsx`: RAC `Link` cells, icon plus `text-caption-2-medium` | Free (compose) | No BoardUI equivalent. 52px targets, safe area padding, hidden on `data-typing`. |
| More sheet | `Modal` bottom sheet | `base/drawer/drawer` `Drawer placement="bottom"` with `DrawerHeader`, `DrawerBody` | Free | Same groups as the menu, 48px rows, Sign out as `Button variant="danger"`(ghost style), ScrollFade on body. |
| Phone account chip | Link pill | `base/buttons/button` `ButtonLink variant="secondary" size="small"` | Free | |
| Footer | `Layout.tsx` | tokens only | Free | |

**Primitives** (all owned by A, consumed by everyone)

| Current | BoardUI replacement | Tier | Notes |
|---|---|---|---|
| `Modal.tsx` (dialog, side drawer, phone bottom sheet with drag) | `base/modal/modal` (`Modal`, `ModalDialog`, `ModalHeader`, `ModalBody`, `ModalFooter`, `AlertDialog`) on desktop; `base/drawer/drawer` `placement="end"` for `variant="drawer"`; `placement="bottom"` on phones (SHEET_QUERY) | Free | Adapter keeps `open`, `onClose`, `title`, `description`, `variant`, `footer`. Drag to close is dropped unless Drawer supports it; Escape, backdrop and close button cover it. |
| `Toaster.tsx` + `stores/toast.ts` | `base/toast/toast` (`toast`, `toast.success`, `toast.error`, `Toaster`) | Free | Keep `stores/toast.ts` API as a shim that calls `toast()` so no page changes. |
| `ConfirmButton.tsx` | `acn/confirm-button.tsx`: `base/popover/popover` (`PopoverTrigger`, `Popover`, `PopoverFooter`) holding a `Button variant="danger"`; whole page deletes use `AlertDialog` | Free | Same props. Focus lands on Cancel. |
| `CopyButton.tsx` (`CopyButton`, `SecretBox`, `copyText`) | `acn/copy-button.tsx`: `IconButton` or `Button size="small"` with `RiFileCopyLine` to `RiCheckLine` swap, `Tooltip` "Copied"; `SecretBox` = read only `Input` in mono plus copy IconButton | Free | `aria-live` "Copied". |
| `Tabs.tsx` | `base/tabs/tabs` (`Tabs`, `TabList`, `Tab` with `count`, `TabPanel`) | Free | Keep the sideways scroll and edge fade on narrow screens (wrap TabList). URL tabs use `href` on `Tab`. |
| `CodeBlock.tsx` | `acn/code-block.tsx`: `bg-code-bg text-code-ink` tokens, `rounded-2xl`, label row, copy IconButton | Free (compose) | No BoardUI code block. Used by B, C, D. |
| `ui.Button` | `base/buttons/button` `Button`, `ButtonLink` | Free | `variant` primary, secondary, ghost, danger. `icon={<X/>}` becomes `leadingIcon={RiX}`. `loading` becomes `isDisabled` plus `leadingIcon={ButtonSpinner}`. |
| `ui.IconButton` | `base/buttons/icon-button` `IconButton` + `base/tooltip/tooltip` | Free | Always an `aria-label`. |
| `ui.Input` | `base/input/input` `Input` (`TextField`, `InputBase` for bare) | Free | `label`, `hint`, `errorMessage`, `leadingIcon`. |
| `ui.Textarea` | `base/textarea/textarea` `Textarea` | Free | auto grow, char count. |
| `ui.Select` (native) | `base/select/select` `Select`, `SelectItem`; lists over ~8 items or searchable: `base/combobox/combobox` `ComboBox` | Free | `onChange(e)` becomes `onSelectionChange(key)`. |
| `ui.Field` | each control's own `label`, `hint`, `errorMessage`; for custom controls `base/input/label` + `base/input/hint-text` | Free | `Field` survives only inside the adapter. |
| `ui.Toggle` | `base/switch/switch` `Switch`; settings rows `base/switch/switch-card` `SwitchCard` | Free | |
| `ui.Badge` | status: `base/badges/chip` `Chip variant="subtle"` (+ `base/badges/status-dot`); counts: `base/badges/badge` | Free | Map: ok `lime`, warn `yellow`, error `rose`, info `blue`, brand `purple`, neutral `gray`. |
| `ui.Callout` | `acn/callout.tsx`: `rounded-2xl bg-status-*-background text-status-*-text` + Remix status icon + optional action `Button size="small"` | Free (compose) | No free inline alert. `role="status"` or `role="alert"` for error. |
| `ui.Empty` | `acn/empty-state.tsx`: icon tile, `text-headline-semibold`, `text-body-regular text-text-secondary`, action slot | Free (compose) | Dashed border variant for in card empties (Usage). |
| `ui.Skeleton`, `PageSkeleton` | `base/skeleton/skeleton` `Skeleton`, `SkeletonText`; `acn/page-skeleton.tsx` | Free | |
| `ui.Spinner`, `PageLoading` | `base/spinner/spinner` `Spinner` | Free | |
| `ui.PageHeader` | `acn/page-header.tsx`: optional back `LinkButton` (`RiArrowLeftLine`) or `base/breadcrumb`, title `text-title-1-medium font-mono`, meta `text-caption-1-regular font-mono`, actions slot | Free (compose) | |
| `ui.Section`, `ui.Card` | `acn/section.tsx` (rounded-3xl card, title, description, action); settings style rows use `application/settings/settings-rows` (`SettingsCard`, `SettingsRow`, `SettingsSectionLabel`) | Free | |
| `ui.Stat` | `application/dashboard/stat-cards` `StatCards variant="plain"`; A makes `delta` optional and adds an `acn/stat-tile.tsx` single tile (also pressable, for Keys filters) | Free | |
| `ui.Segmented` | `base/segmented-control/segmented-control` | Free | |
| `ui.Meta` | `text-caption-1-regular text-text-secondary` | Free | delete |
| `MultiSelect.tsx` | `base/combobox` + `tag-group` (`TagGroup`, `Tag`) for chosen items | Free | Shared by B and C. |
| `StatusPill.tsx` | `Chip variant="subtle"` + `StatusDot` | Free | Shared by B and D. |
| `EntityLogo.tsx` | keep, token borders, `rounded-xl` | Free | Shared by B, C, D. |
| `KeyPaste.tsx` | `Textarea` + `base/file-upload/file-upload` drop zone + preview `Table size="sm"` | Free | Shared by B and C. Keep `useKeyInput` logic. |
| `LogoEditor.tsx` | `file-upload`, `Button`, `Input`, `Avatar`-like preview | Free | Shared by B and C. |
| `AiFill.tsx` | `Button variant="secondary" size="small" leadingIcon={RiSparkling2Line}`, panel as `acn/callout` | Free | Shared by B and C. |
| `ServiceForm.tsx` | `Input`, `Select` (strategy), `ComboBox` (provider), `number-field` `NumberField` (concurrency) | Free | Shared by B and C. |

**Auth and admin pages**

| Page / section | Current | BoardUI replacement | Tier | Notes |
|---|---|---|---|---|
| AuthShell | centered mark + card | `application/auth/auth-card` wrapper page, `logo={<Mark/>}` | Free | Keep tagline and footer. Strip remember me, social providers, switch link in the installed copy (Acnologia owns source). |
| Login | Field, Input, Button | `AuthCard mode="signin" providers={[]}`, `onSubmit(FormData)` | Free | Errors via `acn/callout` above the form. |
| Setup | 4 fields | `AuthCard mode="signup" confirmPassword` | Free | Keep min password hint. |
| App error and loading states (App.tsx) | Mark + Button | `acn/empty-state` + `Spinner` | Free | |
| NotFound | Empty | `acn/empty-state` + `ButtonLink` home | Free | |
| Settings | Section, Textarea rules, AI status, guardrails list | `SettingsCard`, `SettingsRow`, `Textarea`, `Chip` status, `Button` save/reset | Free | ClaudeLogo stays. |
| Account | profile Field/Input + ApiTokens | `SettingsCard` rows with `Input`; `ApiTokens` → `base/table` (`Table`, `TableHeader`, `TableColumn`, `TableBody`, `TableRow`, `TableCell`) + create `Modal` + `acn/copy-button` SecretBox + `acn/confirm-button` revoke | Free | `ApiTokens.tsx` is A's. |
| Users | list + invite Modal + role Select | `base/table`, `Avatar`, role `Select` with hint, `Chip` role, `Modal`, `acn/confirm-button` | Free | Phone: rows collapse to stacked cells. |
| Audit | filter Input + list + load more | `base/search-field`, `base/table` size sm, `Chip` per action, `Button` Load more (cursor based, so no Pagination) | Free | |

### Area B: daily

| Page / section | Current | BoardUI replacement | Tier | Notes |
|---|---|---|---|---|
| Catalog header | PageHeader + Add service | `acn/page-header` + `Button variant="primary" leadingIcon={RiAddLine}` | Free | |
| Catalog search | Input with icon | `base/search-field/search-field` `SearchField` | Free | |
| Catalog category tabs | `Tabs` | `base/tabs` `Tabs`, `TabList`, `Tab count` | Free | |
| Catalog category groups + service cards | Card grid | RAC `Link` card: `rounded-3xl` card, `EntityLogo`, `StatusDot`, `text-body-2-*` | Free (compose) | Whole card is the link; focus ring on card. Muted card for no keys. |
| Catalog empty / error | Empty, Callout | `acn/empty-state`, `acn/callout` | Free | |
| AddServiceModal | Modal + ServiceForm + provider picker + AiFill | `base/modal` `Modal size="lg"`, provider `ComboBox` with `EntityLogo` items, `ServiceForm`, `AiFill` | Free | B owns; C imports it unchanged from ProviderEditor (keep props). |
| Keys header | PageHeader + Check all | `acn/page-header`, `Button`, `IconButton` refresh | Free | |
| Keys filter tiles | clickable Stat tiles | `acn/stat-tile` pressable, as a RAC `ToggleButtonGroup` (single select) | Free (compose) | `aria-pressed`, 2 columns on phone. |
| Keys filters | Input + 3 Selects | `SearchField`, service `ComboBox`, validity `Select`, sort `Select` | Free | |
| Keys list | cards | desktop `base/table` (`Table size="sm"`), phone card rows; `Chip` validity, `QuotaMeter` → `meter` `Meter`, `TimeAgo` | Free | Keep row to KeyDrawer behavior. |
| KeyBits (`ValidityBadge`, `QuotaMeter`, `ActivityChart`, ...) | custom | `Chip`, `Meter`, `Tooltip`; `ActivityChart` → small recharts BarChart styled like `orders-chart-card` (chart-1) | Free | KeyBits is B only, B owns. |
| ServicePage header | back link, logo, meta, Check all, Add keys | `acn/page-header` with back, `EntityLogo` size lg, `Button`s | Free | |
| ServicePage stats | 7 Stat tiles | `StatCards variant="plain"` (A's optional delta) or `acn/stat-tile` row; scrolls sideways on phone | Free | |
| ServicePage tabs | Keys, Activity, Integrate, Settings | `base/tabs` with `href` per tab | Free | |
| KeysTable | table + row icon actions | `base/table`; row actions `IconButton` + `Tooltip` (check, reset, edit, reveal) and `acn/confirm-button` delete; on phone actions collapse into `base/dropdown` `RiMore2Line` menu | Free | `StatusPill`, `ValidityLine`, `QuotaMeter`. |
| KeyDrawer | Modal variant drawer | `base/drawer/drawer` `Drawer placement="end" size="md"`, `DrawerHeader`, `DrawerBody` (ScrollFade), `DrawerFooter` | Free | Bottom sheet on phone through A's adapter, or `placement="bottom"` when using Drawer directly. |
| ServiceSettings | Card + ServiceForm + LogoEditor + danger zone | `SettingsCard`, `SettingsRow`, `ServiceForm`, `LogoEditor`, `AlertDialog` delete | Free | |
| IntegratePanel | Segmented + CodeBlock | `SegmentedControl` (curl, JS, Python...) + `acn/code-block` | Free | |
| AddKeysModal + KeyPaste | Modal, KeyPasteArea, preview, options | `Modal size="lg"`, `KeyPaste` (A), `MultiSelect` groups, `Switch` options, `Select` | Free | |
| KeyEditModal | Modal form | `Modal`, `Input`, `Textarea`, `Select`, `Switch`, `MultiSelect`, `NumberField` | Free | |
| RevealModal | Modal + SecretBox | `Modal size="sm"`, `acn/copy-button` SecretBox, `Spinner` | Free | |
| Activity | PageHeader, Select filters, EventsList, load more | `acn/page-header`, `Select` kind filter, `EventsList`, `Button` Load more | Free | |
| EventsList + KindBadge | rows | rows in a `rounded-3xl` card: `Chip` kind, mono time, entity link; dense `text-body-2-regular` | Free (compose) | Pattern from `application/notification-center` rows, without tabs. B owns. |

### Area C: workspace

| Page / section | Current | BoardUI replacement | Tier | Notes |
|---|---|---|---|---|
| ProvidersPage | PageHeader, Tabs, provider cards, AI builder CTA | `acn/page-header`, `base/tabs`, linked cards (`ProviderLogo`, `Chip` builtin/custom, `Badge` service count), CTA `Button` `RiSparkling2Line` | Free | |
| ProviderEditor header | back, logo, meta chips, Add service, Improve with AI | `acn/page-header`, `Chip` (builtin, v1), `LinkButton` docs (`RiExternalLinkLine`), `Button`s | Free | |
| ProviderEditor tabs (Overview, Connection, Rules, Health check, Examples, Tests, JSON, Versions, Live check) | `Tabs` | `base/tabs` with `count`; scrolls sideways | Free | |
| ProviderEditor side card (Valid, tests) | Card list | `acn/section` + `StatusDot`/`RiCheckboxCircleLine` rows | Free | Stacks under content on phone. |
| ProviderEditor Identity | Fields | `Input`, `Select` status with hint, `Textarea`, `Input` URLs (`dir="ltr"`, mono) | Free | |
| Color field | color input + hex Input | `color-picker` `ColorPicker` (free) | Free | |
| Logo | LogoEditor | `LogoEditor` (A) | Free | |
| Delete provider | Section | `acn/section` + `AlertDialog` | Free | |
| SpecTabs (C owns) | Section, Field, Toggle, Select, IconButton | `acn/section`, `Input`, `Select`, `Switch`/`SwitchCard`, `NumberField`, `IconButton` add/remove rows; Tests and Versions as `base/table` | Free | Long rule lists: `accordion` `Accordion` per rule. |
| SpecInputs (C owns): `ListInput`, `StatusListInput` | Input | `tag-group` + `Input` add field | Free | |
| `OptionalNumberInput` | Input number | `number-field` `NumberField` | Free | |
| `KeyValueEditor` | rows of Inputs | rows of `Input` pairs + `IconButton` remove + `Button` add | Free (compose) | |
| `MatchEditor` | selects + inputs | `Select` + `Input` | Free | |
| `JsonTextarea` | textarea | `Textarea` mono, `errorMessage` on parse failure | Free | |
| AiBuilderPage | Sections, Inputs, KeyPaste, Toggle, submit | `acn/section`, `Input`, `Textarea`, `KeyPaste` (A), `SwitchCard`, `Button` | Free | |
| AiRunPage | Stats, steps log, draft, tests, live check, publish | `StatCards` plain (tokens, searches, turns, cost); live step log → `application/agent-log/agent-log` (`LogRow`, `WorkingRow`, `ShimmerText`, `GuideBridge`) + `application/agent-thinking` | Free | **Task List and Agent Progress are PRO**: compose them from agent-log + agent-thinking exactly like this. Tests `base/table`, draft `acn/code-block`, publish `Button`. |
| FadeText (C owns) | custom | keep, token colors, reduced motion check | Free | |
| Apps | list, form Modal, MCP panel, secret reveal | `base/table` or card rows, `Modal` form (`Input`, `Textarea`, mode `Select` with hint, `MultiSelect`, `Switch`), `acn/copy-button` SecretBox, `acn/confirm-button` | Free | |
| AppMcpPanel | Modal + Tabs + CodeBlock + Toggles | `Modal size="lg"`, `base/tabs` (client tabs), `acn/code-block`, `SwitchCard` | Free | |
| Groups | list + form | card rows, `Modal` form, `MultiSelect`/`ComboBox` members, `acn/copy-button` | Free | |
| Webhooks | list + form + secret + test | card rows with `Chip` format, `Switch` enabled, `Button size="small" variant="ghost" leadingIcon={RiSendPlaneLine}` test, `Modal` form, SecretBox | Free | |
| Egress | list + form + test | same as Webhooks, `RiRadarLine` test | Free | |
| Usage header + range | PageHeader + Segmented 7/30/90 | `acn/page-header` + `SegmentedControl` | Free | |
| Usage tiles | 4 Stat | `StatCards variant="plain" columns={4}` (delta optional) | Free | |
| Usage daily spend chart (stacked Claude + provider per UTC day) | hand drawn SVG | **`application/dashboard/orders-chart-card`** copied to `pages/usage/SpendChartCard.tsx` and changed: two `<Bar>` with `stackId="spend"`, `fill` `var(--color-chart-1)` (Claude) and `var(--color-chart-2)` (provider), top bar rounded only, x axis = days for the range, headline = range total with `useCountUp`, hover readout = that day's split (replaces the tooltip rows), delta `Chip` vs previous range optional | Free | Revenue Chart (area) is the other free card; use it only if a requests trend is added. Area, Line, Earnings, Combo, Bar List charts are PRO. Keep a visually hidden `DailyTable` for screen readers. Empty state inside the card: `acn/empty-state` dashed. |
| Usage Totals | table | `base/table` size sm, numbers right aligned mono | Free | |
| Usage Claude API and Provider APIs sections | tables + share bars | `acn/section` + `base/table`; share column `progress` `ProgressTrack` or `meter` `Meter` with chart-1 / chart-2 | Free (compose) | Replaces PRO Bar List. |

### Area D: assistant and docs

Template mapping: **AI Chat template and Composer are PRO.** The free **Chat Starter** (`application/agent-chat/*`) is the reference: centered column, message rows, composer pill, history rail. A installs `agent-chat-message`, `agent-composer`, `agent-chat-history`, `agent-thinking`, `agent-log`, `composer-loader` (no `agent-chat.tsx`, no `ai` SDK). D owns these copies and adapts them. Logic stays in `stores/assistant.ts` and `Assistant.tsx`; only rendering changes.

| Page / section | Current | BoardUI replacement | Tier | Notes |
|---|---|---|---|---|
| Page frame | centered column, Chats button, New chat | Chat Starter layout: `max-w-3xl` column; top row `Button variant="ghost" leadingIcon={RiChatHistoryLine}` Chats + `IconButton` new chat (`RiEditBoxLine`) with `Tooltip` and `Kbd` | Free | Keep the top bar from A; no sidebar. |
| Chats dialog (ThreadList) | Modal list + Shortcuts | `Modal` (desktop) / bottom `Drawer` (phone) containing the adapted `agent-chat-history` rows: rename, delete via `base/dropdown` or `context-menu`; ScrollFade on the list | Free | Background turns: keep the running indicator per thread (`AgentThinking` dot, `StatusDot`). |
| EmptyState + Suggestions | mark, title, 4 suggestion cards | dragon `Mark`, `text-title-2-medium`, suggestions as RAC `Button` cards `rounded-3xl` with Remix icon, title, caption (Chat Starter starter prompts) | Free (compose) | Same `onPick` submit. In thread suggestions: `Chip`-like `Button size="small" variant="secondary"`. |
| UserBubble | bubble + Edit | `agent-chat-message` `AgentMessage role="user"`; keys count `Badge`; Edit `Button size="xs" variant="ghost" leadingIcon={RiPencilLine}` | Free | Edit only on last user turn when idle. |
| AssistantText + RichText | plain text, inline code, lists, entity chips, Copy, Retry | `AgentMessage role="assistant"` / `StreamedText` for the reveal; keep `RichText` and `linkify`; actions `Button size="xs" variant="ghost"` Copy (`RiFileCopyLine`), Retry (`RiRefreshLine`) | Free | Actions visible on hover/focus, always on touch. |
| EntityChip | link pill | `Chip variant="subtle" color="purple"` inside RAC `Link` | Free | Focus ring on the link. |
| Steps ("Worked for Ns") | StepRow list | `agent-log` `LogRow`, `RowConnector`, `GuideBridge` in a disclosure (`accordion` single item) | Free | Replaces PRO Task List. |
| LiveStatus | text + elapsed | `agent-thinking` `AgentThinking variant="wave" tone="accent"` with label and timer | Free | |
| BuildCard (provider build) | card + steps + Open run | `rounded-3xl` card, `ProviderLogo`, `agent-log` rows, `ProgressBar`, `ButtonLink` Open run | Free (compose) | Replaces PRO Agent Progress. Keeps "finishes itself" after reload. |
| AskCard (ask_user form) | radio/checkbox/text + submit | `RadioGroup`/`Radio` or `Checkbox` rows (`radio-card`, `checkbox-card` for rich options), free text `Textarea` "Other", `Button` submit; multi question: step `Badge`s + Previous/Next `Button`s | Free (compose) | Replaces PRO Questionnaire. Disabled while busy. |
| ConfirmCard (approval) | card, Confirm danger | `rounded-3xl` card, code preview `acn/code-block`, `Button variant="danger"` Confirm + `Button variant="secondary"` Cancel | Free | |
| KeysCard / ServiceCard (tool cards) | cards with status pills | card + `EntityLogo` + `StatusPill` (A) rows + `ButtonLink` | Free | Live status pills while first checks run. |
| ErrorItem | Callout | `acn/callout` error + Retry | Free | |
| Composer | textarea pill, send, model, hint line | `agent-composer` `AgentComposer` wrapped in `composer-loader` `ComposerLoader active={working}` (accent colors) | Free | Replaces PRO Composer and Composer Panel. Keep Enter send, Shift+Enter newline, `/` focus, stop while working, model label. Respect reduced motion (ComposerLoader off). |
| Key paste attachment | AttachmentPreview tile, `toAttachment`, `mergeAttachments` | tile above the prompt: `rounded-2xl` card, `RiKey2Line`, count, source, `CloseButton` | Free (compose) | Replaces PRO Composer Attachments. Logic untouched. |
| File drop overlay | custom dashed panel | full chat overlay with `file-upload` styling tokens (dashed `border-border-button-hover`, `bg-background-full/80`) | Free (compose) | |
| Shortcuts (Cmd K new chat, Cmd Shift O chats, `/` focus) | list | `base/kbd/kbd` `Kbd` rows | Free | |
| Docs landing (`Docs.tsx`) | DocsHeader, hero, features, Stageshow demos, code | `DocsHeader` (dragon `Brand`, `ButtonLink`s), eyebrow `text-caption-2-semibold uppercase text-accent-600`, `text-display-4-medium` hero, feature cards `rounded-3xl`, demos hosted in `rounded-3xl` frames, `acn/code-block` | Free | Stageshow and demos untouched. |
| DocPage sidebar (DocNav) | grouped link list | nav list using `base/dropdown/menu-styles` row classes, `aria-current`; phone: `Drawer placement="start"`; ScrollFade | Free | `tree-view` only if nesting appears. |
| DocPage TOC | sticky list, active heading | sticky nav, `text-body-2-regular`, active `text-text-primary` + accent rail; ScrollFade | Free (compose) | Hidden under 1280 except as a `Popover` "On this page". |
| DocActions (Copy as Markdown, raw .md, llms.txt) | buttons + Modal | `base/buttons/button-group` (`ButtonGroup`, `ButtonGroupItem`) or `Dropdown` | Free | Toast on copy. |
| Markdown typography (`Markdown.tsx`) | prose classes | element map: h2 `text-title-2-semibold`, h3 `text-headline-semibold`, p `text-body-regular`, lists, tables → `base/table` size sm, inline code `font-mono bg-background-tertiary-default rounded-md`, blockquote as `acn/callout` info, links accent | Free | |
| Code blocks / Mermaid | CodeBlock, Mermaid | `acn/code-block`; Mermaid keeps token based theme vars | Free | |
| PrevNext | links | two `rounded-3xl` linked cards with `RiArrowLeftLine`/`RiArrowRightLine` | Free | |
| DocSkeleton | custom | `Skeleton`, `SkeletonText` | Free | |

---

## 3. File ownership

One owner per file. Read anything, edit only what you own. Need a change in someone else's file: message the owner, do not patch it.

**Area A** owns:
- `index.html`, `package.json`, `pnpm-lock.yaml`, `vite.config.ts`, `tsconfig.json`, `main.tsx`, `App.tsx`, `index.css`, `styles/**`, `utils/**`, `stores/theme.ts`, `stores/toast.ts`, `stores/auth.ts`, `stores/users.ts`, `hooks/**`, `lib/**` (read only for others; logic changes go through A).
- Every BoardUI install: `components/base/**`, `components/foundations/**`, `components/application/**` (except the agent chat files below). Only A runs `npx boardui add` or `pnpm add`.
- `components/acn/**` (new) and the legacy shared files until deleted: `ui.tsx`, `Layout.tsx`, `Modal.tsx`, `Toaster.tsx`, `ConfirmButton.tsx`, `CopyButton.tsx`, `Tabs.tsx`, `ThemePicker.tsx`, `CodeBlock.tsx`, `ClaudeLogo.tsx`, `ApiTokens.tsx`, `EntityLogo.tsx`, `StatusPill.tsx`, `MultiSelect.tsx`, `KeyPaste.tsx`, `LogoEditor.tsx`, `AiFill.tsx`, `ServiceForm.tsx`.
- Pages: `Login.tsx`, `Setup.tsx`, `AuthShell.tsx`, `NotFound.tsx`, `Settings.tsx`, `Account.tsx`, `Users.tsx`, `Audit.tsx`.

**Area B** owns: `pages/Catalog.tsx`, `pages/AddServiceModal.tsx`, `pages/Keys.tsx`, `pages/Activity.tsx`, `pages/service/**`, `components/KeyBits.tsx`, `components/EventsList.tsx`, `stores/{keys,services,events,insights}.ts`.

**Area C** owns: `pages/providers/**`, `pages/Apps.tsx`, `pages/AppMcpPanel.tsx`, `pages/Groups.tsx`, `pages/Webhooks.tsx`, `pages/Egress.tsx`, `pages/Usage.tsx`, new `pages/usage/**`, `components/SpecInputs.tsx`, `components/FadeText.tsx`, `stores/{providers,apps,mcp,ai}.ts`.

**Area D** owns: `pages/Assistant.tsx`, `pages/assistant/**`, `pages/Docs.tsx`, `pages/docs/**`, `stores/assistant.ts`, and after A installs them: `components/application/agent-chat/**`, `components/application/agent-log/**`, `components/application/agent-thinking/**`, `components/application/composer-loader/**`. `demos/**` and `stageshow/**` are frozen (D may only swap colors in `demos/*.css` to tokens if a theme check fails).

Cross area imports with frozen contracts (props do not change without the owner agreeing):
- C imports `AddServiceModal` (B) in ProviderEditor and ServiceSettings path. B keeps its props.
- B and D import `StatusPill`, `EntityLogo`, `CodeBlock`, `CopyButton` (A).
- B and C import `KeyPaste`, `LogoEditor`, `AiFill`, `ServiceForm`, `MultiSelect` (A). A keeps their exported names and props; it only reskins internals.

**What B, C, D may use before A finishes:** anything already installed, imported directly from BoardUI paths: `base/buttons/{button,icon-button,link-button,close-button,button-group}`, `base/input/{input,label,hint-text}`, `base/textarea`, `base/select`, `base/combobox`, `base/search-field`, `base/switch/switch`, `base/checkbox/checkbox`, `base/radio/radio`, `base/segmented-control`, `base/tabs/{tabs,pill-tab}`, `base/tooltip`, `base/kbd`, `base/badges/{badge,chip,status-dot}`, `base/avatar`, `base/divider`, `base/skeleton`, `base/spinner` (use `SpinnerGlyph size="sm"` until `ButtonSpinner` lands), `base/progress`, `base/modal`, `base/drawer`, `base/popover`, `base/dropdown`, `base/table`, `base/toast`, `base/notification`, `foundations/scroll-fade`, `foundations/icons/chevrons`, `utils/cx`. Legacy `ui.tsx` and `Modal.tsx` imports keep working throughout.
Wait for A0 before using: `acn/*`, `ButtonSpinner`, Select `hint`, `stat-cards`, `orders-chart-card`, `use-count-up`, `settings-rows`, `auth-card`, `meter`, `number-field`, `tag-group`, `color-picker`, `accordion`, `file-upload`, `radio-card`, `checkbox-card`, `switch-card`, `context-menu`, `breadcrumb`, `agent-*`, `composer-loader`, RAC `RouterProvider` (until then use `onAction={() => navigate(...)}`), `recharts`.

Sequence: A0 (installs, RouterProvider, adapters, acn) → B, C, D in parallel with A's shell and admin pages → A deletes adapters, `lucide-react`, legacy alias utilities in `acnologia.css` once grep finds no users.

---

## 4. Done checklist (every page)

- [ ] Light and dark both match intent; compare against `baseline/<page>-{desktop,phone}-{light,dark}.png`.
- [ ] 1280 wide and 390 wide: no horizontal page scroll (`document.documentElement.scrollWidth <= innerWidth`), nothing clipped, tables either fit or scroll inside their card.
- [ ] Phone: tab bar does not cover content or focused fields; sheets open from the bottom; 44px minimum targets.
- [ ] Console clean: no errors or React warnings on load and on every interaction on the page.
- [ ] Keyboard: every control reachable in order, visible `focus-visible` ring, Escape closes overlays and focus returns to the trigger, menus and tabs use arrow keys.
- [ ] Screen reader: one `h1`, labelled fields, icon buttons have `aria-label`, status changes announced (toasts, copy, live checks), decorative icons `aria-hidden`.
- [ ] Reduced motion: with `prefers-reduced-motion: reduce` nothing slides, scales, blurs or orbits; fades only.
- [ ] Inner scroll regions have the ScrollFade top edge, invisible at scrollTop 0.
- [ ] No PRO code: `grep -rniE "composer-panel|composer-attachments|questionnaire|task-list|agent-progress|agent-limits|web-search|(area|line|bar-list|combo|earnings|funnel|heatmap|radar|radial|sankey|scatter)-chart-card|boardui login" web/src` is empty.
- [ ] No raw colors: `grep -nE "\b(bg|text|border|ring|fill|stroke|from|via|to|outline|divide|shadow)-(red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|slate|gray|zinc|neutral|stone|white|black)(-[0-9]+)?\b|#[0-9a-fA-F]{3,8}\b|oklch\(" <your files>` is empty, and no legacy aliases (`canvas|surface|ink|muted|faint|line|brand|spark|soft|well`) as color utilities.
- [ ] No `lucide-react` import, no `cx` from `components/ui`, no raw `<input>`, `<select>`, `<textarea>`; icons passed as refs.
- [ ] Type via composite utilities only (no `text-sm`, `text-[13px]`, `font-medium leading-*` stacks).
- [ ] Every behavior from before still works (list per page in section 2 notes; for the Assistant: background turns, build card, ask_user, key paste attachment, entity chips, suggestions, retry and edit, shortcuts).
- [ ] `pnpm typecheck` and `pnpm build` pass.
- [ ] No em dash or other dash used as punctuation in copy or comments (check for U+2014, U+2013 between spaces, U+2012, U+2015, U+2212, and doubled hyphens).
