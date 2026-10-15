export interface Workspace {
  id: string;
  name: string;
  icon: string;
  color: string;
  sort_order: number;
  created_at: string;
  /** Commit-identity override (WS-008): both null means "use the global git identity". */
  git_name: string | null;
  git_email: string | null;
  /** Which Azure DevOps organisation this workspace's tickets come from. Null means the user has
   * not chosen, and the resolution falls through to the linked project's own organisation. */
  ado_org: string | null;
  /** Which project inside it holds the board. Null falls through to the repository's own — which a
   * GitHub-hosted repository does not have, so this is the only place it can be said. */
  ado_project: string | null;
}

export interface Project {
  id: string;
  workspace_id: string;
  name: string;
  local_path: string;
  remote_url: string | null;
  color: string;
  icon: string;
  ado_org: string | null;
  ado_project: string | null;
  ado_repo_id: string | null;
  github_owner: string | null;
  github_repo: string | null;
  github_host: string | null;
  sort_order: number;
  created_at: string;
}

export interface NewProject {
  workspace_id: string;
  name: string;
  local_path: string;
  remote_url: string | null;
  /** Omit to let `workspaceStore.addProject` pick the least-used hue — see `lib/ui/projectColor.ts`. */
  color?: string;
  icon: string;
  ado_org: string | null;
  ado_project: string | null;
  ado_repo_id: string | null;
  github_owner: string | null;
  github_repo: string | null;
  github_host: string | null;
}

/** A saved GitHub connection — one per host (github.com or an Enterprise Server). Persisted as
 * the `github_connections` app-setting (JSON); the token itself lives in the OS keychain. */
export interface GithubConnection {
  host: string;
  username: string;
}

/** A saved Azure DevOps connection — one per organization. Persisted as the `ado_connections`
 * app-setting (JSON); the PAT itself lives in the OS keychain, keyed per org. */
export interface AdoConnection {
  org: string;
}

export interface FileStatusEntry {
  path: string;
  status: string;
}

export interface RepoStatusInfo {
  staged: FileStatusEntry[];
  unstaged: FileStatusEntry[];
  untracked: FileStatusEntry[];
  conflicted: FileStatusEntry[];
  current_branch: string | null;
  is_detached: boolean;
}

export interface CommitInfo {
  id: string;
  short_id: string;
  summary: string;
  author_name: string;
  author_email: string;
  timestamp: number;
  parent_ids: string[];
  refs: string[];
}

export interface BranchInfo {
  name: string;
  is_head: boolean;
  is_remote: boolean;
  upstream: string | null;
  ahead: number;
  behind: number;
  target: string | null;
}

export interface StashInfo {
  index: number;
  message: string;
  oid: string;
}

export interface RemoteInfo {
  name: string;
  url: string;
}

export interface GitIdentity {
  name: string | null;
  email: string | null;
}

export interface FileEntry {
  name: string;
  path: string;
  is_dir: boolean;
}

export interface MergeOutcome {
  status: "up_to_date" | "fast_forward" | "merged" | "conflicts";
  conflicts: string[];
}

export interface ConflictFile {
  path: string;
}

export interface DiffLine {
  origin: string;
  content: string;
  old_lineno: number | null;
  new_lineno: number | null;
}

export interface DiffHunkInfo {
  header: string;
  lines: DiffLine[];
}

export interface FileDiffInfo {
  old_path: string | null;
  new_path: string | null;
  status: string;
  hunks: DiffHunkInfo[];
}

/**
 * How applying a stash went (GIT-015).
 *
 * Only `"applied"` removed the entry — libgit2 leaves the stash in place for every other outcome,
 * which is what makes a conflicting pop recoverable rather than a loss.
 */
export type StashApplyOutcome = "applied" | "conflicts" | "not_found" | "uncommitted_changes" | "unknown";

/** One file a commit touched, without its diff — what the graph expands a commit into (GIT-035). */
export interface CommitFileInfo {
  old_path: string | null;
  new_path: string | null;
  status: string;
}

/** A credential-looking match found in the staged diff by the pre-commit secret scanner. */
export interface SecretHit {
  file: string;
  line: number;
  rule: string;
  rule_name: string;
  severity: "critical" | "warning";
  preview: string;
}

export interface ReviewContext {
  id: string;
  workspace_id: string;
  name: string;
  content: string;
  enabled: boolean;
  created_at: string;
}

/** A saved PR-review run as listed in the memory manager (slim projection, no heavy text). */
export interface ReviewRunSummary {
  id: string;
  project_id: string;
  project_name: string;
  pr_id: number;
  pr_title: string;
  iter: number;
  level: string;
  findings_count: number;
  created_at: string;
}

/** One finding inside a saved run's `findings` JSON (mirrors the sidecar's `MemoryFinding`). */
export interface SavedFinding {
  id: string;
  severity: string;
  tipo: string;
  categoria: string;
  subtitulo: string;
  archivo: string | null;
  lineas: string | null;
  confianza: number | null;
  estado: string;
  thread_id?: number | null;
  introducido_en_iter: number;
  resuelto_en_iter?: number | null;
  motivo_descarte?: string | null;
  delta?: string | null;
}

/** Full content of one saved review run, for the in-app viewer / export. */
export interface ReviewRunDetail {
  id: string;
  project_id: string;
  pr_id: number;
  iter: number;
  level: string;
  /** Run metadata as a JSON string (ReviewMeta). */
  meta: string;
  review_md: string;
  diff: string;
  /** Parsed findings as a JSON string. */
  findings: string;
  created_at: string;
}

/** A user-defined SDD/Harness agent (role): name, role, model, prompt, on/off. */
export interface WorkspaceAgent {
  id: string;
  workspace_id: string;
  name: string;
  role: string;
  provider: string;
  model: string;
  prompt: string;
  enabled: boolean;
  sort_order: number;
  created_at: string;
}

export interface WorkspaceSkill {
  id: string;
  workspace_id: string;
  skill_name: string;
  source_repo: string;
  enabled: boolean;
  installed_at: string;
}

export interface WorkspaceMcp {
  id: string;
  workspace_id: string;
  name: string;
  command: string;
  args: string;
  env: string;
  enabled: boolean;
  created_at: string;
}

export interface ActivityLogEntry {
  id: string;
  project_id: string;
  /** The conversation this turn belongs to (app-minted, stable), not the engine's session token. */
  session_id: string | null;
  /** The engine's resume token for this turn, when it reported one — used to carry a reopened
   * conversation forward on the CLI's side. */
  engine_session_id: string | null;
  question: string;
  answer: string;
  /** JSON array of `{stream, line}` with what the engine printed while working on this turn, so a
   * finished answer can still show how it got there. `null` for turns recorded before traces. */
  trace: string | null;
  created_at: string;
  /** How long the engine took to answer, in ms. `null` for turns recorded before this was tracked. */
  response_time_ms: number | null;
  /** True when the turn failed — `answer` then holds the engine's error text. */
  is_error: boolean;
  /** Provider id that answered this turn (`claude`, `codex`, …), recorded at the time it ran so a
   * reopened conversation isn't relabelled by today's routing. `null` for older turns. */
  provider: string | null;
  /** Model the CLI reported for this turn. `null` for older turns, or when it didn't report one. */
  model: string | null;
  /** Version of the engine CLI that answered this turn. `null` for older turns. */
  engine_version: string | null;
}

export interface JobHistoryEntry {
  id: string;
  project_id: string;
  kind: string;
  label: string;
  custom_label: string | null;
  status: string;
  result: string | null;
  error: string | null;
  meta: string;
  created_at: string;
}

export interface ChatConversationSummary {
  session_id: string;
  project_id: string;
  title: string;
  created_at: string;
  updated_at: string;
  turn_count: number;
}

export interface PrThreadComment {
  author: string;
  content: string;
  published_date: string;
}

export interface PrCommentThread {
  id: number;
  file_path: string | null;
  start_line: number | null;
  end_line: number | null;
  comments: PrThreadComment[];
}

export interface GitProgressEvent {
  op: string;
  line: string;
}

export interface GitDoneEvent {
  op: string;
  success: boolean;
  message: string;
}

export type ThemePreference = "light" | "dark" | "system";

export interface AdoProject {
  id: string;
  name: string;
}

export interface AdoRepo {
  id: string;
  name: string;
}

export type VcsProvider = "azure" | "github";

/** AI-drafted PR title + body, returned by `generate_pr_description` to prefill the create form. */
export interface PrDescriptionDraft {
  title: string;
  body: string;
}

export interface PullRequestSummary {
  id: number;
  title: string;
  description: string;
  status: "open" | "draft" | "merged" | "closed";
  source_branch: string;
  target_branch: string;
  author: string;
  created_at: string;
  url: string;
  /** Which host this PR came from — drives the "view on…" link and post-confirmation copy. */
  provider: VcsProvider;
}

export type AutoLinkResult =
  | { status: "Linked"; project: Project }
  | { status: "NeedsToken"; provider: VcsProvider; identifier: string }
  | { status: "NotDetected" };

/** The decision the signed-in user has already recorded on a pull request, as its host reports it
 * — so a vote cast on the website counts the same as one cast here. Drives whether the approve /
 * request-changes buttons are still offered. */
export type PrDecision = "approved" | "changes_requested" | "none";

/** What a PR decision left behind: the pull request as the host now reports it, and the Activity
 * row the action was filed under. */
export interface PrActionOutcome {
  pr: PullRequestSummary;
  activity: JobHistoryEntry;
}

/** What a pasted pull-request link turned out to be. `Ready` is the happy path: the PR was read
 * from its host *and* matched to a local repository (linked on the spot if it wasn't already), so
 * everything downstream — diff, findings, comments, review memory — works exactly as it does for
 * a PR picked from the sidebar. */
export type PrLinkResolution =
  | {
      status: "Ready";
      project_id: string;
      workspace_id: string;
      project_name: string;
      pr: PullRequestSummary;
    }
  | { status: "NeedsToken"; provider: VcsProvider; identifier: string }
  /** A credential is saved and the host refused it — a different sentence from "connect this
   * organisation", because the user has already done that. Azure DevOps only for now. */
  | { status: "Expired"; provider: VcsProvider; identifier: string }
  | {
      status: "NoLocalRepo";
      provider: VcsProvider;
      repo_label: string;
      /** Clone URL for the "clone it and review" offer. */
      clone_url: string;
      pr: PullRequestSummary;
    }
  | { status: "Unrecognized" };

// ---------------------------------------------------------------------------
// Work items (tickets)
// ---------------------------------------------------------------------------

/** A cached work item. `mirror_path` is where its readable copy lives on disk. */
export interface Ticket {
  id: string;
  provider: string;
  org: string;
  project: string;
  /** Text, not a number: Azure numbers work items and Jira names them ("PROJ-45"). */
  external_id: string;
  title: string;
  state: string;
  work_item_type: string;
  assigned_to: string | null;
  web_url: string;
  rev: number;
  mirror_path: string;
  synced_at: string;
}

/** One row of a ticket picker — what a list shows, fetched without the rest of the work item. */
export interface TicketSummary {
  external_id: string;
  title: string;
  state: string;
  work_item_type: string;
  assigned_to: string | null;
}

/** How the Azure account for a project's tickets was decided. `none` means it was not: the UI has
 * to ask, because guessing shows the wrong board's (empty) list and blames the board. */
export type TicketAccountSource = "workspace" | "project" | "only_connection" | "none";

export interface TicketAccount {
  org: string | null;
  project: string | null;
  source: TicketAccountSource;
}

/** Where a ticket's requirements were found, and in what shape.
 * `list`: an enumerable list, already numbered in `items`.
 * `prose`: narrative — the model enumerates it, because splitting prose by regex cuts criteria in half.
 * `none`: no field carried enough content to be a requirement, which the review says out loud. */
export type TicketCriteriaMode = "list" | "prose" | "none";

export interface TicketCriteria {
  mode: TicketCriteriaMode;
  /** The Azure reference name the content came from, e.g. `System.Description`. */
  field: string | null;
  markdown: string;
  items: string[];
}

/** Where a cached ticket is linked: one per branch it is work for. */
export interface TicketLink {
  project_id: string;
  /** The repository's own name — an id on screen answers nothing. */
  project_name: string;
  branch: string;
}

/**
 * A cached ticket and the branches it is work for.
 *
 * The links ride with it because `list_tickets` is workspace-wide: a row that does not say which
 * repository and branch it belongs to is unreadable as soon as a workspace holds two repositories.
 * Genuinely plural — `ticket_links` is keyed `(project_id, branch)`.
 */
export interface TicketWithLinks {
  ticket: Ticket;
  links: TicketLink[];
}

/** A ticket a branch name appears to be about. A suggestion, never a link. */
export interface TicketSuggestion {
  provider: string;
  external_id: string;
}

/** A work item address parsed out of pasted text. `org`/`project` are null for a bare id. */
export interface TicketLinkRef {
  id: number;
  org: string | null;
  project: string | null;
}

/**
 * One finished ticket review.
 *
 * `criteria` and `coverage` are what the sidecar parsed out of the model's answer with
 * `TicketVerdict`, stored as parsed so a later parser change cannot rewrite history. `review_md` is
 * the whole answer, findings and verdict sections together — `splitTicketReview` cuts it for the two
 * renderers.
 */
export interface TicketReviewResult {
  id: string;
  project_id: string;
  ticket_id: string;
  branch: string;
  base_ref: string;
  head_sha: string;
  level: string;
  review_md: string;
  criteria: TicketCriterionVerdictWire[];
  coverage: TicketCoverageWire | null;
  created_at: string;
}

/** The wire form of one criterion's verdict — snake_case, unlike `parseTicketVerdict`'s own type. */
export interface TicketCriterionVerdictWire {
  id: string;
  criterion: string;
  verdict: string;
  evidence: string;
  confidence: number | null;
}

/** The wire form of the coverage block. */
export interface TicketCoverageWire {
  coverage: string;
  missing: string;
  out_of_scope: string;
  summary: string;
}

/**
 * Where a person dragged one table of a schema document (DBML-005).
 *
 * snake_case in both directions: `dbml_load_layout` returns these and `dbml_save_positions` takes
 * them back unchanged. `table_key` is `schema.table` in lower case (`lib/dbml/identifiers.ts`
 * `tableKey`).
 */
export interface DbmlTablePosition {
  table_key: string;
  x: number;
  y: number;
}

/**
 * What the AI is asked to do with a schema document (DBML-016).
 *
 * Here rather than in `lib/dbml/` because it is a wire value — the sidecar's `DbmlAssistant` matches
 * these three strings and answers `unknown DBML assist mode '…'` for anything else — and because
 * `lib/ipc/commands.ts` must not import from `lib/dbml/`, whose entry point drags in the 15 MB
 * parser (DBML-006).
 */
export type DbmlAssistMode = "edit" | "review" | "explain";

/** The engines the schema designer can read a schema out of (DBML-023). */
export type DbmlDriver = "postgres" | "sqlserver" | "mysql" | "sqlite";

export const DBML_DRIVERS: readonly DbmlDriver[] = ["postgres", "sqlserver", "mysql", "sqlite"];

/**
 * A saved database connection.
 *
 * **There is no password field, and that is structural** (DBML-024): it lives in the OS credential
 * store and the sidecar never returns it. `file_path` is SQLite's alternative to host/port/database.
 */
export interface DbmlConnection {
  id: string;
  name: string;
  driver: DbmlDriver;
  host: string | null;
  port: number | null;
  database: string | null;
  username: string | null;
  file_path: string | null;
  use_tls: boolean;
  created_at: string;
  updated_at: string;
}

/**
 * What the renderer sends to create or update one.
 *
 * `password` travels in one direction only. Sending it blank leaves whatever is stored alone, which
 * is what lets somebody fix a port without retyping the secret.
 */
export interface NewDbmlConnection {
  id: string | null;
  name: string;
  driver: DbmlDriver;
  host: string | null;
  port: number | null;
  database: string | null;
  username: string | null;
  file_path: string | null;
  use_tls: boolean;
  password: string | null;
}

/** What a real database looks like, read out of it (DBML-025). Structured, not DBML text. */
export interface DbmlSchemaSnapshot {
  tables: DbmlSnapshotTable[];
  refs: DbmlSnapshotRef[];
  enums: DbmlSnapshotEnum[];
}

export interface DbmlSnapshotTable {
  schema: string;
  name: string;
  columns: DbmlSnapshotColumn[];
  indexes: DbmlSnapshotIndex[];
}

export interface DbmlSnapshotColumn {
  name: string;
  /** As the engine spells it, arguments included — `varchar(120)`. */
  type: string;
  pk: boolean;
  not_null: boolean;
  unique: boolean;
  increment: boolean;
  default_value: string | null;
}

export interface DbmlSnapshotIndex {
  name: string | null;
  columns: string[];
  unique: boolean;
  pk: boolean;
}

export interface DbmlSnapshotRef {
  from_schema: string;
  from_table: string;
  from_columns: string[];
  to_schema: string;
  to_table: string;
  to_columns: string[];
  on_delete: string | null;
  on_update: string | null;
}

export interface DbmlSnapshotEnum {
  schema: string;
  name: string;
  values: string[];
}

/**
 * How much can be known about one AI provider's consumption (USAGE-004).
 *
 * `measured` — the provider writes a transcript this app can read.
 * `unlimited` — a local engine, metered by nothing at all.
 * `unavailable` — it publishes no readable consumption, so nothing is shown rather than guessed.
 */
export type UsageState = "measured" | "unlimited" | "unavailable";

/** One model's share of a metered window. */
export interface UsageModel {
  model: string;
  tokens: number;
}

/**
 * Where a percentage came from (USAGE-011).
 *
 * `reported` — the provider's own figure, read from its CLI. `observed` — divided by a ceiling this
 * app watched the user reach. They are not the same claim, and the panel does not present them as
 * though they were. Empty when there is no percentage at all.
 */
export type UsagePercentSource = "reported" | "observed" | "";

/**
 * One metered stretch of time (USAGE-003).
 *
 * `percent` is null when neither source has anything to say. `ceiling` is set only for an
 * `observed` percentage: a reported share arrives with no denominator attached. Null and zero are
 * different answers and the panel draws them differently.
 */
export interface UsageWindow {
  tokens: number;
  percent: number | null;
  source: UsagePercentSource;
  ceiling: number | null;
  started_at: string;
  resets_at: string;
  burn_per_hour: number;
  models: UsageModel[];
}

/** One agent's consumption, in both windows a subscription is metered against. */
export interface UsageProvider {
  provider: string;
  state: UsageState;
  plan: string;
  session: UsageWindow | null;
  week: UsageWindow | null;
}

/**
 * What the CodeFlow process costs the machine (USAGE-007).
 *
 * The Go process only — the renderer runs in a WebView process of its own that nothing here can
 * see, which is why the panel labels this as the process rather than as "the app". `sampled` is
 * false on the first reading, which has no previous one to rate against.
 */
export interface UsageResources {
  cpu_percent: number;
  memory_bytes: number;
  sampled: boolean;
}

/** The room the app's own files take. `complete` is false when the sweep hit its limit. */
export interface UsageData {
  bytes: number;
  complete: boolean;
}

/** What the app has been used for in the trailing week, counted from tables already being written. */
export interface UsageActivity {
  conversations: number;
  jobs: number;
}

/** Everything the indicator draws, measured at one instant (USAGE-001). */
export interface UsageSnapshot {
  providers: UsageProvider[];
  resources: UsageResources;
  data: UsageData;
  activity: UsageActivity;
  taken_at: string;
}

// ---------- code map (18-code-map.md) ----------

/** What a node of the code map is (CODEMAP-001). */
export type CodemapKind =
  | "func"
  | "method"
  | "type"
  | "interface"
  | "interface_method"
  | "component"
  | "hook"
  | "test"
  | "test_file"
  | "module"
  | "table"
  | "endpoint";

/** How two nodes relate: `cross` is a hop from TypeScript into Go (CODEMAP-004). */
export type CodemapEdgeKind = "call" | "ref" | "render" | "dispatch" | "cross" | "read" | "write" | "mention";

export type CodemapLang = "go" | "ts" | "sql";

/** One language's share of a map. `missing_tool` names a toolchain the machine does not have. */
export interface CodemapLanguageStatus {
  lang: CodemapLang;
  state: "mapped" | "partial" | "missing_tool" | "absent" | "failed";
  files: number;
  symbols: number;
  detail: string;
}

/** What a build of the map produced and what it could not do (CODEMAP-002). */
export interface CodemapStatus {
  root_path: string;
  nodes: number;
  edges: number;
  languages: CodemapLanguageStatus[];
  notes: string[];
  built_at: string;
  duration_ms: number;
  /** The Archify install a flow can be drawn with, "" when there is none (CODEMAP-017). */
  archify: string;
}

/** The stage a build is in, as `codemap:progress` reports it. */
export type CodemapStage = "scanning" | "go" | "typescript" | "linking" | "ready";

export interface CodemapProgressEvent {
  root_path: string;
  stage: CodemapStage;
}

export interface CodemapSymbolRef {
  id: string;
  name: string;
  kind: CodemapKind;
  lang: CodemapLang;
  file: string;
  line: number;
  end_line: number;
  signature: string;
}

export interface CodemapLocation {
  file: string;
  line: number;
}

/** A neighbour; `via` is the interface method or endpoint a caller reaches the symbol through. */
export interface CodemapRelation {
  symbol: CodemapSymbolRef;
  kind: CodemapEdgeKind;
  file: string;
  line: number;
  via: CodemapSymbolRef | null;
}

export interface CodemapTableUse {
  table: CodemapSymbolRef;
  read: boolean;
  write: boolean;
  mention: boolean;
  direct: boolean;
  via: CodemapSymbolRef | null;
}

/** A symbol and its neighbourhood (CODEMAP-008). */
export interface CodemapSymbolDetail {
  symbol: CodemapSymbolRef;
  doc: string;
  parent: CodemapSymbolRef | null;
  bound: string;
  callers: CodemapRelation[];
  callees: CodemapRelation[];
  implementations: CodemapRelation[];
  implements: CodemapRelation[];
  tables: CodemapTableUse[];
  tests: CodemapSymbolRef[];
  unresolved: CodemapLocation[];
}

/** A name resolved: the detail when it names one symbol, the candidates when it names several. */
export interface CodemapSymbolLookup {
  detail: CodemapSymbolDetail | null;
  candidates: CodemapSymbolRef[];
}

export interface CodemapFlowNode {
  symbol: CodemapSymbolRef;
  via: CodemapEdgeKind | "";
  file: string;
  line: number;
  children: CodemapFlowNode[];
  repeat: boolean;
  truncated: boolean;
}

/** Everything a symbol leads to (CODEMAP-009). */
export interface CodemapFlow {
  root: CodemapFlowNode;
  steps: number;
  truncated: boolean;
}

export interface CodemapGoTestGroup {
  package_dir: string;
  tests: string[];
  working_dir: string;
  command: string;
}

export interface CodemapTsTestGroup {
  package_dir: string;
  files: string[];
  runner: string;
  command: string;
}

/** What may break when a symbol changes, and the tests to run (CODEMAP-010). */
export interface CodemapImpact {
  root: CodemapSymbolRef;
  levels: CodemapSymbolRef[][];
  entry_points: CodemapSymbolRef[];
  go_tests: CodemapGoTestGroup[];
  ts_tests: CodemapTsTestGroup[];
  truncated: boolean;
}

export interface CodemapArea {
  path: string;
  lang: CodemapLang;
  symbols: number;
}

export interface CodemapBoundService {
  name: string;
  file: string;
  methods: number;
  called_by: number;
}

export interface CodemapTableSummary {
  table: CodemapSymbolRef;
  readers: number;
  writers: number;
}

export interface CodemapHotspot {
  symbol: CodemapSymbolRef;
  callers: number;
}

/** An AI answer about the code, and the conversation to continue it in (CODEMAP-014). */
export interface CodemapAskAnswer {
  answer: string;
  session_id: string;
}

/** A page Archify rendered and the app opened, and Archify's own report on it (CODEMAP-017). */
export interface CodemapArchifyResult {
  html_path: string;
  diagram_type: "architecture" | "sequence";
  report: string;
}

/** The whole map at a glance (CODEMAP-011). */
export interface CodemapOverview {
  status: CodemapStatus;
  areas: CodemapArea[];
  services: CodemapBoundService[];
  endpoints: number;
  tables: CodemapTableSummary[];
  hotspots: CodemapHotspot[];
}

// ---------- the drawn map (19-repo-map.md, backend/repomap/wire.go) ----------

/** What a declaration is, VERBATIM from `backend/repomap/facts.go`. */
export type RepoMapKind =
  | "class"
  | "interface"
  | "enum"
  | "struct"
  | "record"
  | "mixin"
  | "type"
  | "function"
  | "method"
  | "const"
  | "var";

/** One card: a folder holding everything below it, or a file. */
export interface RepoMapNode {
  /** The folder's or file's path in the repository. */
  id: string;
  label: string;
  folder: boolean;
  files: number;
  declarations: number;
  lines: number;
  /** The language most of its files are written in. */
  language: string;
  /** Files outside the node that import something in it. */
  imported_by: number;
  /** The top-level folder it belongs to; "" for a file at the root. */
  group: string;
}

/** The imports from one node into another; `weight` counts file-to-file imports. */
export interface RepoMapEdge {
  from: string;
  to: string;
  weight: number;
}

export interface RepoMapGraph {
  /** The folder shown; "" is the repository, and what the project view answers. */
  focus: string;
  nodes: RepoMapNode[];
  edges: RepoMapEdge[];
  /** These three describe the whole repository, whatever the focus. */
  files: number;
  declarations: number;
  languages: { language: string; files: number }[];
}

/** How the map was read this time. */
export interface RepoMapBuild {
  files: number;
  /** Files read now rather than taken from the cache. */
  parsed: number;
  millis: number;
  /** False in a build without tree-sitter, where no file has declarations. */
  grammars: boolean;
  /** Code files left out past the map's bound. */
  truncated: number;
  /** Git chose the files, honouring .gitignore and .gitattributes. */
  git: boolean;
}

export interface RepoMapView {
  graph: RepoMapGraph;
  build: RepoMapBuild;
}

export interface RepoMapOutlineRow {
  name: string;
  kind: RepoMapKind;
  line: number;
  end_line: number;
  signature: string;
  depth: number;
}

export interface RepoMapFileDetail {
  path: string;
  language: string;
  lines: number;
  /** False for a language without a grammar: its outline and imports are unknown. */
  parsed: boolean;
  declarations: RepoMapOutlineRow[];
  imports: string[];
  imported_by: string[];
  external: string[];
}

export interface RepoMapDeclHit {
  path: string;
  name: string;
  kind: RepoMapKind;
  line: number;
  end_line: number;
  signature: string;
  /** The declaration it sits inside; "" at the top of its file. */
  parent: string;
  /** Other files that mention it and import its file, or share its package. */
  used_by: number;
}

export interface RepoMapUsage {
  path: string;
  line: number;
  /** The signature of the declaration the mention sits inside; "" at the top level. */
  within: string;
  /** The file imports the declaring one or shares its package; false is a mention by name only. */
  confirmed: boolean;
}

// ---------- Archify documents (20-archify-diagrams.md, backend/archify) ----------

/** What the user's Archify install can do. */
export interface ArchifyStatus {
  installed: boolean;
  version: string;
  /** The install draws class diagrams (its 3.1 line onwards). */
  has_class: boolean;
  /** The command that installs it, for the view to offer when it is missing. */
  install: string;
}

/** A class diagram drawn from the map, ready to be written into the repository. */
export interface ArchifyClassDiagram {
  /** The Archify document, as JSON text. */
  document: string;
  title: string;
  types: number;
  /** Types past the diagram's bound, the least used, left out. */
  omitted: number;
}

/** A program a feature needs and the app does not ship, as found on this machine (REQ-002). */
export interface RequirementTool {
  id: "git" | "node" | "go" | "archify";
  installed: boolean;
  /** What it reported, "" when it is not installed or did not say. */
  version: string;
  path: string;
  /** False for an install too old for what it is needed for. */
  supported: boolean;
}

/** The diagrams a model draws by reading the repository (ARCHIFY-007). */
export type ArchifyAiKind = "architecture" | "workflow" | "sequence" | "dataflow" | "lifecycle";

/** A document a model drew — or changed — and the user's Archify accepted (ARCHIFY-007, ARCHIFY-008). */
export interface ArchifyDrawn {
  /** The Archify document, as JSON text. */
  document: string;
  title: string;
  diagram_type: string;
  /** How many times the answer went back to the model to be fixed. */
  rounds: number;
}

/** A topic worth drawing, picked from the map (ARCHIFY-009). */
export interface ArchifySuggestion {
  label: string;
  /** Where it comes from: `path:line`, or the file that names it. */
  hint: string;
  /** What is sent as the topic when it is picked. */
  topic: string;
}

/** A document drawn for the in-app viewer. */
export interface ArchifyRendered {
  /** Where the asset server serves the page, for an iframe. */
  view_url: string;
  /** The page on disk, in the app's data folder — what "open in the browser" opens. */
  html_path: string;
  /** Archify's own receipt; "" when the page was already drawn. */
  report: string;
}

export interface RepoMapUsageReport {
  declarations: RepoMapDeclHit[];
  usages: RepoMapUsage[];
  /** Files that mention the name but, by their known imports, mean another declaration of it. */
  excluded: number;
}
