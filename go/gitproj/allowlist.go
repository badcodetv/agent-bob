package gitproj

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// The secret allowlist (design/2026-09-09-git-projection.md §D).
//
// Nothing about a project is rendered into a git repository unless a rule in
// this file names the field and records a decision about it. The database is
// private; a repo with collaborators, forks and permanent history is not, and
// several configuration fields hold live credentials today — a Slack incoming
// webhook URL *is* a bearer token, and an MCP server's `env` map is where an
// operator naturally pastes an API key.
//
// Three decisions, and only three:
//
//	Render            safe to publish as-is.
//	RenderEnvRefOnly  publishable ONLY as a whole-value ${VAR} reference.
//	Never             not rendered at all, reason recorded below.
//
// ── Refusal, not redaction ──────────────────────────────────────────────────
//
// When a RenderEnvRefOnly field holds anything other than a whole-value
// ${VAR} reference, the answer is an *UnrenderableError and the tree is not
// written at all. It is deliberately not "render the file with the value
// starred out". A redacted value teaches an operator that the field was
// published safely, and the next field like it — added by someone reading the
// rendered output as evidence of what is safe — will not be.
//
// ── Why the rule is "accept only ${VAR}" and never "reject literals" ─────────
//
// This is DI1 in the design doc's Discovered Issues Log, and it is binding.
// §D of that doc originally claimed the codebase already enforced "a value is
// either a literal or entirely one ${VAR}". It did not, and for MCP env and
// header values it still does not: agentdb.MCPServerConfig.Validate only
// refuses a value that CONTAINS "${" without being a whole reference, so a
// value with no "${" at all — `xoxb-1234-real-token` — is perfectly valid
// stored configuration, and cmd/agentd's resolveHeaders passes a partially
// interpolated `Bearer ${TOKEN}` through to the wire as a literal.
//
// So the rule here is stated positively and refuses by default: a value is
// renderable in a credential-bearing field if and only if it matches
// envRefRe end-to-end. A literal is refused. A partial interpolation is
// refused — it is the shape MOST likely to carry a real token, since
// `Bearer sk-…` and `https://host/services/<secret>` both live in it. A
// non-string is refused. A blob whose shape we cannot walk is refused.
//
// ── What this allowlist does NOT do ─────────────────────────────────────────
//
// It is not a secret scanner. Free-text fields that a human or a model
// authored — SystemPrompt, Skill.Markdown, Skill.InstallSh, Description —
// render as-is, and a token pasted into a prompt will be published. Those
// fields exist to be read; there is no machine-checkable shape that separates
// prose from a leaked key, and pretending otherwise would be the redaction
// mistake in another costume. The allowlist covers the *structured* fields
// whose whole job is to hold a credential. The operator guide (G17) must say
// this plainly.
// ─────────────────────────────────────────────────────────────────────────────

// Decision is what the allowlist says about one field. Every field of every
// rendered struct has exactly one, recorded in the tables below.
type Decision string

const (
	// Render: publish the value as it is stored.
	Render Decision = "render"
	// RenderEnvRefOnly: publish ONLY a whole-value ${VAR} reference. Any other
	// value is an *UnrenderableError. When the rule carries Leaves the check
	// applies at those positions inside a jsonb blob rather than to the whole
	// value — an attention channel's `kind` is not a secret, its `url` is.
	RenderEnvRefOnly Decision = "render_env_ref_only"
	// Never: not rendered. The Reason on the rule says why.
	Never Decision = "never"
)

// The struct names CheckRenderable and the rule tables are keyed by. They are
// the Go type names, because the reflection guard in allowlist_guard_test.go
// compares this file against the real structs and a rename must break the
// build rather than quietly drop a field's decision.
const (
	StructProjectSettings = "ProjectSettings"
	StructWorker          = "Worker"
	StructSkill           = "Skill"
	StructSubscription    = "Subscription"
	StructSchedule        = "Schedule"
	StructCustomImage     = "CustomImage"
)

// Rule is one field's decision, plus the facts a later ticket needs.
//
// Field is the Go struct field name — that is the guard test's vocabulary and
// the vocabulary CheckRenderable expects, because a reflection-driven renderer
// naturally holds Go field names. Key is the same field's JSON/frontmatter key,
// so G4 (Render) can build a frontmatter map without re-deriving it from struct
// tags.
type Rule struct {
	// Field is the Go struct field name.
	Field string
	// Key is the frontmatter key this field renders under (its json tag).
	// Empty when Decision is Never.
	Key string
	// Decision is Render, RenderEnvRefOnly or Never.
	Decision Decision
	// Leaves applies only to RenderEnvRefOnly. Empty means "the whole value
	// must be a ${VAR} reference". Non-empty means "these positions inside the
	// value must be", which is how a jsonb blob gets checked at the leaf that
	// holds the credential instead of being refused wholesale.
	Leaves []leafSelector
	// NotImportable records §D's rule that a field renders as informational but
	// must never be written back by the git importer (G11). It is a fact for
	// the importer to read, not something this file can enforce.
	NotImportable bool
	// Reason is why this decision, in one line. Mandatory for Never.
	Reason string
}

// leafSelector names a position inside a jsonb blob. Segments are literal map
// keys, except "*" which matches every key at that level. So
// {"headers", "*"} is "every value in the headers map" and
// {"*", "env", "*"} is "every value in every server's env map".
type leafSelector []string

// envRefRe matches a WHOLE-value ${VAR} reference and nothing else.
//
// KEEP IN SYNC with cmd/agentd/attention.go's envRefRe and agentdb/sessions.go's
// envRefPattern — the three copies exist because gitproj is a leaf package that
// may not import cmd/agentd, and the agentdb copy is unexported. Same reasoning
// as the duplication noted in the design doc's DI3.
//
// Note what is deliberately absent: no surrounding-whitespace tolerance. The
// existing resolvers disagree about trimming (resolveHeaders trims before
// matching, MCPServerConfig.Validate does not), so `" ${VAR}"` resolves on one
// path and is sent as literal text on another. A value whose safety depends on
// which reader picks it up is not a safe reference, so it is refused here.
var envRefRe = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*\}$`)

// IsEnvRef reports whether value is a whole-value ${VAR} reference — the only
// shape a credential-bearing field may be published in.
func IsEnvRef(value string) bool { return envRefRe.MatchString(value) }

// ErrUnrenderable is the sentinel every render refusal wraps, so callers can
// write errors.Is(err, ErrUnrenderable) without depending on the struct.
var ErrUnrenderable = errors.New("gitproj: field cannot be rendered")

// UnrenderableError names the struct and field that refused to render.
//
// It deliberately does NOT carry the offending value. This error travels into
// logs, project events and the console; putting a literal Slack webhook URL
// into the message that exists because it must not be published would be the
// leak the check was written to prevent. The path is enough to fix it.
type UnrenderableError struct {
	// Struct is one of the Struct* constants above.
	Struct string
	// Field is the Go field name, with a dotted leaf path appended when the
	// refusal was inside a blob, e.g. "AttentionChannel.headers.Authorization".
	Field string
	// Reason says what was wrong, without quoting the value.
	Reason string
}

func (e *UnrenderableError) Error() string {
	return fmt.Sprintf("gitproj: %s.%s cannot be rendered: %s", e.Struct, e.Field, e.Reason)
}

func (e *UnrenderableError) Unwrap() error { return ErrUnrenderable }

// ─────────────────────────────────────────────────────────────────────────────
// The tables. One entry per struct field. No wildcards, no defaults, no
// "everything else renders" — a field that is not here fails the guard test in
// allowlist_guard_test.go and the build with it.
// ─────────────────────────────────────────────────────────────────────────────

// attentionChannelLeaves are the credential-bearing positions inside
// ProjectSettings.AttentionChannel, whose stored shape is
// {"kind":"webhook","url":"…","headers":{"Authorization":"…"}}.
//
// `url` is the important one: a Slack or Discord incoming-webhook URL is a
// bearer token in its entirety — anyone holding it posts as the integration —
// and the store still accepts a literal one (cmd/agentd/attention.go's
// parseAttentionChannel allows a literal http(s) URL; G3 only closed partial
// interpolation). Header values are the ordinary Authorization case. `kind` is
// the discriminator and header NAMES are not secrets, so neither is listed.
var attentionChannelLeaves = []leafSelector{
	{"url"},
	{"headers", "*"},
}

// mcpConfigLeaves are the credential-bearing positions inside an `mcp_config`
// jsonb blob, whose shape is a map of server name → agentdb.MCPServerConfig.
//
// `env` and `headers` are where a literal key lands, and nothing stops one:
// MCPServerConfig.Validate refuses partial interpolation but accepts a value
// with no "${" at all. The doc comment on that struct claims these values "are
// never secret values" — that is an aspiration about how operators use it, not
// an invariant the code holds.
//
// `url` is included, which goes one step beyond the field-by-field list in the
// §D discussion, and deliberately. Hosted MCP endpoints routinely carry the
// credential in the URL itself (Zapier's `…/api/mcp/s/<secret>/mcp`, Composio,
// Smithery), which is the same "the URL is the token" shape as a Slack webhook.
// The cost is that a plain non-secret MCP URL must also be moved into an
// environment variable before a project will render; that refusal is loud,
// documented and one edit to fix, whereas a published endpoint secret is
// permanent in git history. `command` and `args` are an executable path and its
// arguments inside the container, and are rendered.
var mcpConfigLeaves = []leafSelector{
	{"*", "url"},
	{"*", "env", "*"},
	{"*", "headers", "*"},
}

var projectSettingsRules = []Rule{
	{Field: "Project", Decision: Never,
		Reason: "a project's identity is the clone it lives in, not a field inside it; rendering it would invite an import that retargets a project at another namespace. The commit trailers carry Bob-Project for humans."},
	{Field: "BaseImage", Key: "base_image", Decision: Render,
		Reason: "an image reference; names a registry path, carries no credential (registry auth is imageregistry/auth, resolved at pull time)."},
	{Field: "SystemPrompt", Key: "system_prompt", Decision: Render,
		Reason: "the project prompt — rendered as the BODY of settings.md, which is the whole point of the projection. Free text: see the 'not a secret scanner' note above."},
	{Field: "MCPConfig", Key: "mcp_config", Decision: RenderEnvRefOnly, Leaves: mcpConfigLeaves,
		Reason: "jsonb map of MCP servers; url, env values and header values can hold literal tokens, so they are checked at the leaf. The rest of the blob renders."},
	{Field: "AttentionChannel", Key: "attention_channel", Decision: RenderEnvRefOnly, Leaves: attentionChannelLeaves,
		Reason: "jsonb {kind,url,headers}; the webhook url IS a bearer token and header values are the ordinary Authorization case. Checked at the leaf so kind still renders."},
	{Field: "MaxConcurrentJobs", Key: "max_concurrent_jobs", Decision: Render, Reason: "a capacity number."},
	{Field: "DailyTokensSoft", Key: "daily_tokens_soft", Decision: Render, Reason: "a budget number."},
	{Field: "DailyTokensHard", Key: "daily_tokens_hard", Decision: Render, Reason: "a budget number."},
	{Field: "BriefingMaxBytes", Key: "briefing_max_bytes", Decision: Render, Reason: "a size limit."},
	{Field: "SnapshotTTLDays", Key: "snapshot_ttl_days", Decision: Render, Reason: "a retention number."},
	{Field: "Briefing", Key: "briefing", Decision: Render,
		Reason: "project-wide briefing selectors: label expressions naming what workers read, not values."},

	// The five git fields. §D: they render (they name a repo and the NAME of an
	// environment variable, never a secret) but they are NOT IMPORTABLE — if a
	// commit could rewrite them, anyone with push access to the mirror could
	// point a project's projection, and its push credential, at a repo they
	// control. The struct comment in agentdb/project_settings.go says the same;
	// this is the machine-readable copy G11 reads. DI3 in the design doc.
	{Field: "GitRemote", Key: "git_remote", Decision: Render, NotImportable: true,
		Reason: "the repo URL this project renders to — a public-ish location, not a credential; the token lives in GitTokenEnv's variable. NOT IMPORTABLE."},
	{Field: "GitBranch", Key: "git_branch", Decision: Render, NotImportable: true,
		Reason: "a branch name. NOT IMPORTABLE."},
	{Field: "GitSubfolder", Key: "git_subfolder", Decision: Render, NotImportable: true,
		Reason: "one path segment Bob owns in the repo. NOT IMPORTABLE — an import that could move it could write outside the subfolder, including .github/workflows."},
	{Field: "GitTokenEnv", Key: "git_token_env", Decision: Render, NotImportable: true,
		Reason: "the NAME of an environment variable, never its value — the same api_key_env pattern as the project map. Safe to publish; the secret stays in agentd's environment. NOT IMPORTABLE."},
	{Field: "GitWebhookSecretEnv", Key: "git_webhook_secret_env", Decision: Render, NotImportable: true,
		Reason: "the NAME of the environment variable holding the webhook HMAC secret, never the secret. NOT IMPORTABLE, and the sharpest case of the five (G20): a commit that could rewrite it would point signature verification at a secret the committer chose, i.e. make forged deliveries verify."},

	{Field: "UpdatedAt", Decision: Never,
		Reason: "a wall-clock timestamp that changes on every write. Rendering it would put a diff in every commit whether or not anything about the project changed, and would break the 'render is a pure function, equal state means no commit' property the import loop's termination rests on. When a thing changed is git's own answer."},
}

var workerRules = []Rule{
	{Field: "Project", Decision: Never,
		Reason: "as ProjectSettings.Project: identity is the clone, not a field. Rendering it invites a retargeting import."},
	{Field: "Name", Key: "name", Decision: Render,
		Reason: "the worker's identity, and its filename (workers/<name>.md), validated by ValidateName before it becomes a path."},
	{Field: "Description", Key: "description", Decision: Render, Reason: "human prose about the worker."},
	{Field: "SystemPrompt", Key: "system_prompt", Decision: Render,
		Reason: "the prompt — rendered as the BODY, and the field the whole projection exists to make diffable. Free text; not scanned."},
	{Field: "MCPConfig", Key: "mcp_config", Decision: RenderEnvRefOnly, Leaves: mcpConfigLeaves,
		Reason: "same jsonb shape and same leaf rule as ProjectSettings.MCPConfig."},
	{Field: "Image", Key: "image", Decision: Render, Reason: "an image reference ('', name, or name:version)."},
	{Field: "Briefing", Key: "briefing", Decision: Render, Reason: "label selectors naming what this worker reads."},
	{Field: "MaxInstances", Key: "max_instances", Decision: Render, Reason: "a concurrency number."},
	{Field: "Enabled", Key: "enabled", Decision: Render, Reason: "a flag."},
	{Field: "Frozen", Key: "frozen", Decision: Render,
		Reason: "a flag, and one a human reviewing a diff especially wants to see change — it is the causal-isolation boundary."},
	{Field: "Connections", Key: "connections", Decision: Render, NotImportable: true,
		Reason: "connection names — the credential lives in agentd's environment and is never a field. " +
			"Rendered so a reader sees who can reach what; NOT importable, because anyone with push access to " +
			"the mirror could otherwise grant '*' (Decision 3). The importer drops the key rather than writing " +
			"it (gitproj's notImportable map in parse.go, T11)."},
	{Field: "CreatedAt", Decision: Never, Reason: "wall-clock timestamp; see ProjectSettings.UpdatedAt. Creation order is git's first commit of the file."},
	{Field: "UpdatedAt", Decision: Never, Reason: "wall-clock timestamp; churns every commit. See ProjectSettings.UpdatedAt."},
}

var skillRules = []Rule{
	{Field: "ID", Decision: Never,
		Reason: "an internal uuid. A skill's identity in the repo is its name (skills/<name>.md); publishing the row id adds nothing a reader can use and changes on every revision."},
	{Field: "CreatedAt", Decision: Never, Reason: "wall-clock timestamp; see ProjectSettings.UpdatedAt."},
	{Field: "UpdatedAt", Decision: Never, Reason: "wall-clock timestamp; see ProjectSettings.UpdatedAt."},
	{Field: "Name", Key: "name", Decision: Render, Reason: "the skill's identity and its filename."},
	{Field: "Description", Key: "description", Decision: Render, Reason: "human prose."},
	{Field: "Visibility", Key: "visibility", Decision: Render, Reason: "an enum ('organizational' etc.)."},
	{Field: "Customer", Decision: Never,
		Reason: "this column IS the project namespace for §14 skills. Same reason as Worker.Project: identity is the clone."},
	{Field: "OwnerEmail", Decision: Never,
		Reason: "personal data. The database is private; a git repo has collaborators, forks and permanent history, and an email address published there cannot be recalled. Provenance that IS rendered is a worker name, not a person."},
	{Field: "RequiresBuild", Key: "requires_build", Decision: Render, Reason: "a flag derived from whether install.sh exists."},
	{Field: "ContentHash", Decision: Never,
		Reason: "a derived digest of content git already stores and hashes itself; it would churn a diff line for no reader."},
	{Field: "BlobPrefix", Decision: Never,
		Reason: "an internal path in the shared BlobStore — the same store that holds artifacts, snapshots and dataset bytes (docs/20-datasets.md warns the prefix is load-bearing). Publishing internal storage layout buys a reader nothing and hands a probe a map."},
	{Field: "Manifest", Decision: Never,
		Reason: "untyped jsonb with no schema constrained anywhere in the store, so its contents cannot be enumerated and checked. Unsure means Never (§D)."},
	{Field: "PromotedBy", Decision: Never,
		Reason: "an actor identity that is an email or user id in practice. Same reason as OwnerEmail."},
	{Field: "Revision", Key: "revision", Decision: Render, Reason: "the append ordinal; skills are append-only and the revision is how 'newest' is defined."},
	{Field: "Labels", Key: "labels", Decision: Render, Reason: "label set saying what the skill is for; the same grammar as memory labels."},
	{Field: "Markdown", Key: "markdown", Decision: Render,
		Reason: "the skill document — rendered as the BODY. Free text; not scanned."},
	{Field: "InstallSh", Key: "install_sh", Decision: Render,
		Reason: "the install script. Free text authored by a human or a worker, in the same trust class as a prompt: publishable by intent, not scanned for pasted secrets."},
	{Field: "CreatedByWorker", Key: "created_by_worker", Decision: Render,
		Reason: "server-stamped provenance naming a worker, not a person. The design already publishes the equivalent in commit trailers (Bob-Actor-Worker)."},
	{Field: "CreatedBySession", Key: "created_by_session", Decision: Render,
		Reason: "server-stamped session id — an identifier, never a token (session tokens live elsewhere). The design already publishes it as the Bob-Actor-Session trailer."},
}

// subscriptionRules covers agentdb.Subscription (G19).
//
// Nothing here can hold a credential today: Filter is an equality match on
// event-envelope fields, EventType and Worker are names, MaxFiringsPerHour is
// a number. If that stops being true — someone adds a signing secret or a
// callback URL — the new field must not default to Render; it belongs here
// with a RenderEnvRefOnly decision and Leaves if only part of a blob is
// credential-bearing.
var subscriptionRules = []Rule{
	{Field: "ID", Key: "id", Decision: Render, Reason: "the subscription's identity and its filename (subscriptions/<id>.md)."},
	{Field: "Project", Decision: Never,
		Reason: "as ProjectSettings.Project: identity is the clone, not a field. Rendering it invites a retargeting import."},
	{Field: "EventType", Key: "event_type", Decision: Render, Reason: "the event type name being subscribed to."},
	{Field: "Filter", Key: "filter", Decision: Render,
		Reason: "jsonb equality match on event-envelope fields (e.g. {\"worker\":\"email-answerer\"}) — names and values drawn from the event vocabulary, not a place a credential is stored."},
	{Field: "Worker", Key: "worker", Decision: Render, Reason: "the worker this subscription wakes."},
	{Field: "MaxFiringsPerHour", Key: "max_firings_per_hour", Decision: Render, Reason: "a rate-limit number."},
	{Field: "Enabled", Key: "enabled", Decision: Render, Reason: "a flag."},
	{Field: "CreatedAt", Decision: Never, Reason: "wall-clock timestamp; see ProjectSettings.UpdatedAt."},
	{Field: "UpdatedAt", Decision: Never, Reason: "wall-clock timestamp; see ProjectSettings.UpdatedAt."},
}

// scheduleRules covers agentdb.Schedule (G19).
//
// Configuration only. ProvisionFailures, LastProvisionError and LastEvaluated
// are RUNTIME STATE — LastEvaluated is rewritten by every scheduler tick, and
// rendering any of the three would produce a commit on every tick or failure,
// burying the prompt changes this projection exists to make reviewable.
var scheduleRules = []Rule{
	{Field: "ID", Key: "id", Decision: Render, Reason: "the schedule's identity and its filename (schedules/<id>.md)."},
	{Field: "Project", Decision: Never,
		Reason: "as ProjectSettings.Project: identity is the clone, not a field. Rendering it invites a retargeting import."},
	{Field: "Worker", Key: "worker", Decision: Render, Reason: "the worker-mode target; empty exactly when TargetSession is set."},
	{Field: "TargetSession", Key: "target_session", Decision: Render, Reason: "the session-mode target — a session NAME, not a credential."},
	{Field: "Cron", Key: "cron", Decision: Render, Reason: "the cron expression."},
	{Field: "Input", Key: "input", Decision: Render, Reason: "the instruction text delivered as the event — authored configuration, in the same trust class as a prompt."},
	{Field: "Enabled", Key: "enabled", Decision: Render, Reason: "a flag."},
	{Field: "ProvisionFailures", Decision: Never,
		Reason: "runtime state: a counter of consecutive provisioning failures, reset by the next successful firing. Rendering it would commit on every failure, and is exempt from the config log for the same reason (NoteScheduleEvaluated)."},
	{Field: "LastProvisionError", Decision: Never,
		Reason: "runtime state paired with ProvisionFailures — the reason for the most recent failure, not configuration, and would churn a diff on every failure."},
	{Field: "LastEvaluated", Decision: Never,
		Reason: "runtime state: the scheduler's per-tick evaluation watermark (RD11), rewritten every tick. Rendering it would produce a commit a minute, forever, and break 'equal state means no commit'."},
	{Field: "CreatedAt", Decision: Never, Reason: "wall-clock timestamp; see ProjectSettings.UpdatedAt."},
	{Field: "UpdatedAt", Decision: Never, Reason: "wall-clock timestamp; see ProjectSettings.UpdatedAt."},
}

// imageRules covers agentdb.CustomImage (G19). §B: the frontmatter only — the
// image bytes are a versioned blob with their own store and their own reaper,
// and git is not a third storage tier (§G).
var imageRules = []Rule{
	{Field: "ID", Decision: Never,
		Reason: "an internal uuid. A custom image's identity in the repo is its name (images/<name>.md), as Skill.ID argues."},
	{Field: "CreatedAt", Decision: Never, Reason: "wall-clock timestamp; see ProjectSettings.UpdatedAt."},
	{Field: "UpdatedAt", Decision: Never, Reason: "wall-clock timestamp; see ProjectSettings.UpdatedAt."},
	{Field: "Name", Key: "name", Decision: Render, Reason: "the image's identity and its filename."},
	{Field: "Description", Key: "description", Decision: Render, Reason: "human prose."},
	{Field: "Visibility", Key: "visibility", Decision: Render, Reason: "an enum ('organizational' etc.)."},
	{Field: "Customer", Decision: Never,
		Reason: "this column IS the project namespace, exactly as Skill.Customer. Identity is the clone."},
	{Field: "OwnerEmail", Decision: Never,
		Reason: "personal data. Same reason as Skill.OwnerEmail — an email address published to a repo with forks and permanent history cannot be recalled."},
	{Field: "ContentHash", Decision: Never,
		Reason: "an internal digest of stored bytes git does not hold; useless to a reader and churns without configuration changing."},
	{Field: "RegistryHandle", Decision: Never,
		Reason: "a JSON-encoded imageregistry.Handle — internal storage layout, unschema'd here. Unsure means Never (§D), same reasoning as Skill.Manifest."},
	{Field: "SkillSet", Decision: Never,
		Reason: "a JSON-encoded ordered list of {skillId,name,content_hash} — internal storage layout, unschema'd here. Unsure means Never (§D), same reasoning as Skill.Manifest."},
	{Field: "RequiresBuild", Key: "requires_build", Decision: Render, Reason: "a flag derived from whether install.sh exists."},
	{Field: "BaseImageID", Decision: Never,
		Reason: "an internal uuid naming lineage in the store's own id space; not a name a reader or the importer can act on."},
	{Field: "BaseInstallation", Key: "base_installation", Decision: Render, Reason: "the platform installation this layer was built on, when applicable — a name, not a credential."},
	{Field: "Focus", Key: "focus", Decision: Render, Reason: "the CLAUDE.md focus applied in this layer — authored text."},
	{Field: "Version", Key: "version", Decision: Render, Reason: "the append ordinal; images are append-only and the version is how 'newest' is defined."},
	{Field: "Labels", Key: "labels", Decision: Render, Reason: "label set — the commit message of a version, same grammar as memory labels."},
	{Field: "CreatedByWorker", Key: "created_by_worker", Decision: Render,
		Reason: "server-stamped provenance naming a worker, not a person. Same reasoning as Skill.CreatedByWorker."},
	{Field: "CreatedBySession", Key: "created_by_session", Decision: Render,
		Reason: "server-stamped session id — an identifier, never a token. Same reasoning as Skill.CreatedBySession."},
	{Field: "ReapedAt", Decision: Never,
		Reason: "reaper bookkeeping: a tombstone timestamp stamped when the snapshot_ttl_days reaper deletes this version's bytes. Runtime state, not configuration."},
	{Field: "ExpiresAt", Decision: Never,
		Reason: "reaper bookkeeping: the instant the reaper may delete this version's bytes, stamped at burn time from a project setting that can itself change. Runtime state."},
	{Field: "LastResumedAt", Decision: Never,
		Reason: "reaper bookkeeping: rewritten every time a session launches from this version, to defer reaping. Runtime state that would commit on every launch."},
}

// allowlist is the whole table, by struct name.
var allowlist = map[string][]Rule{
	StructProjectSettings: projectSettingsRules,
	StructWorker:          workerRules,
	StructSkill:           skillRules,
	StructSubscription:    subscriptionRules,
	StructSchedule:        scheduleRules,
	StructCustomImage:     imageRules,
}

// ruleIndex is allowlist flattened to struct → field → rule, built once.
var ruleIndex = func() map[string]map[string]Rule {
	out := make(map[string]map[string]Rule, len(allowlist))
	for structName, rules := range allowlist {
		byField := make(map[string]Rule, len(rules))
		for _, r := range rules {
			byField[r.Field] = r
		}
		out[structName] = byField
	}
	return out
}()

// Rules returns the allowlist entries for a struct, in the order they are
// declared above. The bool is false for a struct the allowlist does not cover.
func Rules(structName string) ([]Rule, bool) {
	rules, ok := allowlist[structName]
	return rules, ok
}

// RuleFor returns the decision recorded for one Go field.
func RuleFor(structName, field string) (Rule, bool) {
	byField, ok := ruleIndex[structName]
	if !ok {
		return Rule{}, false
	}
	r, ok := byField[field]
	return r, ok
}

// RenderableFields returns the Go field names of structName that may be
// rendered at all — everything whose decision is not Never — sorted, so a
// caller iterating them produces deterministic output. It is the list G4
// (Render) should build its frontmatter from.
func RenderableFields(structName string) []string {
	rules, ok := allowlist[structName]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		if r.Decision != Never {
			out = append(out, r.Field)
		}
	}
	sort.Strings(out)
	return out
}

// CheckRenderable is the gate the renderer calls before writing a file.
//
// fields is what the renderer intends to publish, keyed by GO FIELD NAME (see
// Rule.Field). Every key must be named by the allowlist and must not be Never,
// and every RenderEnvRefOnly value must be a whole-value ${VAR} reference at
// the positions the rule names. On the first problem — checked in sorted field
// order so the error is stable — it returns an *UnrenderableError wrapping
// ErrUnrenderable, and the caller must write NOTHING: §D is refusal, not
// partial output.
//
// A field the renderer chose to omit is simply absent from fields; only what is
// present is checked. Empty and nil values are treated as "not set" and pass —
// an unset attention channel must not make a project unrenderable, and an empty
// string carries no credential. The renderer is expected to omit the key rather
// than emit `url: ""`.
func CheckRenderable(structName string, fields map[string]any) error {
	byField, ok := ruleIndex[structName]
	if !ok {
		return &UnrenderableError{
			Struct: structName,
			Field:  "*",
			Reason: "no allowlist table exists for this struct; add one in gitproj/allowlist.go before rendering it",
		}
	}

	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		rule, known := byField[name]
		if !known {
			return &UnrenderableError{
				Struct: structName,
				Field:  name,
				Reason: "field is not named by the allowlist; add it to gitproj/allowlist.go with a decision (Render, RenderEnvRefOnly or Never) and a reason",
			}
		}
		switch rule.Decision {
		case Never:
			return &UnrenderableError{
				Struct: structName,
				Field:  name,
				Reason: "allowlist decision is Never: " + rule.Reason,
			}
		case Render:
			continue
		case RenderEnvRefOnly:
			if err := checkEnvRefRule(structName, rule, fields[name]); err != nil {
				return err
			}
		default:
			return &UnrenderableError{
				Struct: structName,
				Field:  name,
				Reason: fmt.Sprintf("unknown allowlist decision %q", rule.Decision),
			}
		}
	}
	return nil
}

// checkEnvRefRule applies RenderEnvRefOnly to one value: either the whole value
// (no Leaves) or every position the leaf selectors reach.
func checkEnvRefRule(structName string, rule Rule, value any) error {
	if len(rule.Leaves) == 0 {
		return checkEnvRefLeaf(structName, rule.Field, value)
	}
	for _, sel := range rule.Leaves {
		if err := walkLeaves(structName, rule.Field, rule.Field, value, sel); err != nil {
			return err
		}
	}
	return nil
}

// walkLeaves descends sel through value, checking every position it reaches.
//
// A missing key is not an error — an attention channel with no `headers` is
// ordinary. A value that is present but is NOT a map where the selector needs
// one IS an error: the check cannot see inside it, and a blob we cannot walk is
// a blob we cannot say is safe. Refusal by default, all the way down.
func walkLeaves(structName, field, path string, value any, sel leafSelector) error {
	if len(sel) == 0 {
		return checkEnvRefLeaf(structName, path, value)
	}
	if value == nil {
		return nil
	}
	m, ok := asStringMap(value)
	if !ok {
		return &UnrenderableError{
			Struct: structName,
			Field:  path,
			Reason: fmt.Sprintf("expected an object here so that %q could be checked for whole-value ${VAR} references, but the stored value is a %T", strings.Join(sel, "."), value),
		}
	}

	head, tail := sel[0], sel[1:]
	if head == "*" {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := walkLeaves(structName, field, path+"."+k, m[k], tail); err != nil {
				return err
			}
		}
		return nil
	}
	child, present := m[head]
	if !present {
		return nil
	}
	return walkLeaves(structName, field, path+"."+head, child, tail)
}

// checkEnvRefLeaf is the whole rule, in one place: a value at a
// credential-bearing position is renderable only if it is a string that is
// entirely one ${VAR} reference, or is unset.
func checkEnvRefLeaf(structName, path string, value any) error {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		if v == "" {
			return nil
		}
		if IsEnvRef(v) {
			return nil
		}
		return &UnrenderableError{
			Struct: structName,
			Field:  path,
			Reason: "credential-bearing field: the stored value is not a whole-value ${VAR} reference, so it may be a literal secret and is refused (a literal, a partial interpolation like \"Bearer ${TOKEN}\", or padding whitespace all land here). Move the value into an environment variable of agentd and set this field to ${VAR}",
		}
	default:
		return &UnrenderableError{
			Struct: structName,
			Field:  path,
			Reason: fmt.Sprintf("credential-bearing field: expected a string holding a whole-value ${VAR} reference, got %T", value),
		}
	}
}

// asStringMap normalises anything string-keyed and map-shaped into
// map[string]any so walkLeaves can descend it.
//
// Reflection rather than a type switch, because a type switch would miss every
// NAMED map type — agentdb.JSONMap is declared as `type JSONMap map[string]any`
// and does NOT satisfy `case map[string]any`, which is precisely the value the
// store hands us for mcp_config and attention_channel. A missed case here would
// not be a compile error or a visible failure: walkLeaves would see "not a map",
// and the strictness of this file is the only thing that turns that into a
// refusal rather than a silent publish.
func asStringMap(v any) (map[string]any, bool) {
	if m, ok := v.(map[string]any); ok {
		return m, true
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil, false
	}
	out := make(map[string]any, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		out[iter.Key().String()] = iter.Value().Interface()
	}
	return out, true
}
