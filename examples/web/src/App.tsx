import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ThemeProvider, CssBaseline, Box, Button, useMediaQuery } from "@mui/material";
import {
  AgentChatProvider,
  AgentChat,
  AgentSessionList,
  ActivityPage,
  CredentialModeBadge,
  DeskPage,
  GuideProvider,
  MemoryBrowserPage,
  NAV_LABELS,
  OnboardingPage,
  OrgChartPage,
  ProjectSettingsPage,
  WorkersPage,
  buildGuideHash,
  navRevealSentence,
  parseGuideHash,
  projectIdFromLocation,
  useAsksCount,
  useNavReveal,
  usePrefersReducedMotion,
  useSessionPermalink,
  type GuideParagraphMap,
  type NavEntry,
} from "@agentkit/chat-ui";
import { AuthConfig, AuthState, clearAuthState, fetchAuthConfig, loadAuthState, mintProjectToken, saveAuthState } from "./auth";
import GuidePage from "./GuidePage";
import { GUIDE_PAGES } from "./guide/pages.generated.js";
import LoginScreen from "./LoginScreen";
import ProjectPicker from "./ProjectPicker";
import { useInterviewState, useOnboardingSession } from "./onboarding";
import Sidebar from "./Sidebar";
import { darkTheme, lightTheme } from "./theme";

const API = import.meta.env.VITE_API ?? ""; // "" → same origin (nginx proxy)

// Where an unfinished onboarding is remembered across a reload. Storage can
// throw (a private window, storage disabled), and an onboarding screen is not
// worth failing the whole shell over, so both sides swallow.
const PENDING_ONBOARDING_KEY = "agentkit.onboarding.pending";

function loadPendingOnboarding(): { project: string; goal: string } | null {
  try {
    const raw = window.localStorage.getItem(PENDING_ONBOARDING_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as { project?: unknown; goal?: unknown };
    if (typeof parsed.project !== "string" || parsed.project === "") return null;
    return { project: parsed.project, goal: typeof parsed.goal === "string" ? parsed.goal : "" };
  } catch {
    return null;
  }
}

function savePendingOnboarding(value: { project: string; goal: string } | null): void {
  try {
    if (value === null) window.localStorage.removeItem(PENDING_ONBOARDING_KEY);
    else window.localStorage.setItem(PENDING_ONBOARDING_KEY, JSON.stringify(value));
  } catch {
    // ignore
  }
}

// What a project view can show. Deliberately a state machine and not a router:
// the library must not impose react-router on hosts, and the permalink hook
// already owns the one URL that matters (the session).
//
// The set IS the library's `NavEntry` since K9 (doc 28 §3): the nav is
// progressive, so which of these a project actually shows is decided by
// `useNavReveal` from what the project contains, not hardcoded here. Desk still
// lands (K1) — its first-run panel is the onboarding screen.
//
// `events` and `automation` are gone: the first is now Activity (one rail
// instead of five tabs) and the second is a tab on the worker it belongs to.
type View = NavEntry;

// How often the two live surfaces re-fetch. The library defaults `refreshMs` to
// 0 — no timer — because a component library that starts polling the moment it
// mounts is deciding something that belongs to the host. Turning it on is
// therefore a shell decision, and this is the shell.
//
// The staged-arrival machinery behind it (the "N new" pill, and the "Pause live
// updates" toggle WCAG 2.2.2 requires wherever content moves on its own) only
// renders while something is actually polling, so before this constant existed
// both were unreachable.
const LIVE_REFRESH_MS = 15_000;

// The bridge C2 depends on: `GUIDE_PAGES` (built by build-guide.mjs from
// docs/guide/*.md, ticket C1) is keyed by slug and carries each page's
// `surfaces` list; `AboutThisScreen` looks a paragraph up BY SURFACE. This is
// the one place that inversion happens, so `web/` itself never has to import
// a generated file to make the lookup it needs (design §3 G2). First page
// wins a surface named on more than one: `GUIDE_PAGES` is already sorted by
// part/order (build-guide.mjs), so "first" is deterministic, and two pages
// claiming the same surface is a Stream-B authoring mistake this shell is not
// the place to catch.
function buildGuideParagraphs(pages: typeof GUIDE_PAGES): GuideParagraphMap {
  const map: GuideParagraphMap = {};
  for (const page of pages) {
    for (const surface of page.surfaces) {
      if (!(surface in map)) map[surface] = { slug: page.slug, text: page.firstParagraph };
    }
  }
  return map;
}

// App state machine: loading → dev (legacy /dev/token, straight to chat)
//                            → login → project picker → chat (per-project JWT)
export default function App() {
  const [authConfig, setAuthConfig] = useState<AuthConfig | null>(null);
  const [auth, setAuth] = useState<AuthState | null>(() => loadAuthState());
  const [devToken, setDevToken] = useState<string | null>(null);

  // Two theme objects, not one with `mode` flipped: the palette differs by
  // value, not by inversion (design §3.3).
  const prefersDark = useMediaQuery("(prefers-color-scheme: dark)");
  const theme = prefersDark ? darkTheme : lightTheme;

  // Built once: GUIDE_PAGES is a build-time constant (empty until docs/guide/
  // has pages — C1's "guide not written yet" case), never refetched.
  const guideParagraphs = useMemo(() => buildGuideParagraphs(GUIDE_PAGES), []);

  useEffect(() => {
    fetchAuthConfig(API)
      .then(setAuthConfig)
      .catch(() => setAuthConfig({ modes: ["dev"], google_client_id: "" }));
  }, []);

  const devMode = authConfig?.modes.includes("dev") ?? false;
  useEffect(() => {
    if (!devMode) return;
    fetch(`${API}/dev/token`)
      .then((r) => r.json())
      .then((j) => setDevToken(j.token))
      .catch(() => setDevToken("")); // dev-open fallback
  }, [devMode]);

  const handleLogin = useCallback((state: AuthState) => setAuth(state), []);

  const selectProject = useCallback((projectID: string) => {
    setAuth((prev) => {
      if (!prev) return prev;
      const next = { ...prev, selectedProject: projectID };
      saveAuthState(next);
      return next;
    });
  }, []);

  const signOut = useCallback(() => {
    clearAuthState();
    setAuth(null);
  }, []);

  // Wildcard users can mint a token for a brand-new project id — this is how
  // a project is "created" (it has no row anywhere; the first session in it
  // makes it real).
  // The goal of a project that was created and has not finished onboarding.
  //
  // Held here rather than in the workspace because the workspace is remounted
  // (keyed by project) the moment the new project is selected, and state
  // inside it would not survive that — and PERSISTED, because an interview
  // takes minutes and a reload in the middle of one would otherwise drop the
  // human on the Desk with a live interview they can no longer see. Re-entry
  // is safe: startOnboarding rejoins the existing `onboard` session rather
  // than creating a second one.
  const [pendingOnboarding, setPendingOnboarding] = useState<{ project: string; goal: string } | null>(loadPendingOnboarding);
  useEffect(() => savePendingOnboarding(pendingOnboarding), [pendingOnboarding]);

  const createProject = useCallback(async (projectID: string, goal: string) => {
    const loginToken = auth?.loginToken;
    if (!loginToken) throw new Error("no wildcard login token");
    const minted = await mintProjectToken(API, loginToken, projectID);
    setPendingOnboarding({ project: minted.id, goal });
    setAuth((prev) => {
      if (!prev) return prev;
      const projects = prev.projects.some((p) => p.id === minted.id)
        ? prev.projects.map((p) => (p.id === minted.id ? minted : p))
        : [...prev.projects, minted];
      const next = { ...prev, projects, selectedProject: minted.id };
      saveAuthState(next);
      return next;
    });
  }, [auth?.loginToken]);

  // A pasted permalink names its project (/p/<project>/s/<session>), so honour
  // it once we hold a token for that project — otherwise the link would dump
  // the reader in the project picker, or worse, in whichever project they last
  // used, and the session would never resume.
  //
  // Once only, guarded by a ref: after this, switching project is the human's
  // decision and the URL must not drag them back.
  const permalinkProjectApplied = useRef(false);
  useEffect(() => {
    if (permalinkProjectApplied.current || !auth) return;
    const wanted = projectIdFromLocation();
    if (!wanted) return;
    permalinkProjectApplied.current = true;
    // No token for that project means the reader was never authorised for it —
    // leave them where they are rather than failing a fetch later.
    if (!auth.projects.some((p) => p.id === wanted)) return;
    if (auth.selectedProject !== wanted) selectProject(wanted);
  }, [auth, selectProject]);

  const project = auth?.selectedProject ?? null;
  const projectToken = auth?.projects.find((p) => p.id === project)?.token ?? null;

  // Which model actually answers (RD18). agentd computes it from its own
  // credentials and reports it on /auth/config; the shell never guesses.
  const credentialMode = authConfig?.credential_mode ?? null;

  const chatConfig = useMemo(() => {
    const token = devMode ? devToken : projectToken;
    return {
      apiBaseUrl: API,
      // Raw token — the chat-ui hook/provider prepend "Bearer " themselves.
      getAuthToken: () => token ?? "",
      // The id is what the server is asked for; the LABEL must not claim Opus
      // answered when the offline mock did.
      models: [
        {
          id: "claude-opus-4-5",
          label: credentialMode === "mock" ? "Opus (mock — no model called)" : "Opus",
        },
      ],
    };
  }, [devMode, devToken, projectToken, credentialMode]);

  if (authConfig === null) return null; // waiting for /auth/config

  // ── Dev mode: legacy zero-login demo ──────────────────────────────────────
  if (devMode) {
    if (devToken === null) return null; // waiting for /dev/token
    return (
      <ThemeProvider theme={theme}>
        <CssBaseline />
        <AgentChatProvider config={chatConfig}>
          <Box sx={{ display: "flex", height: "100vh" }}>
            <Box sx={{ width: 280, borderRight: 1, borderColor: "divider" }}>
              <Box sx={{ px: 1.5, py: 1 }}>
                <CredentialModeBadge mode={credentialMode} />
              </Box>
              <DevSessionList />
            </Box>
            <Box sx={{ flex: 1 }}><AgentChat /></Box>
          </Box>
        </AgentChatProvider>
      </ThemeProvider>
    );
  }

  // ── Login modes ────────────────────────────────────────────────────────────
  if (!auth) {
    return (
      <ThemeProvider theme={theme}>
        <CssBaseline />
        <LoginScreen apiBase={API} config={authConfig} onLogin={handleLogin} />
      </ThemeProvider>
    );
  }

  if (!project || !projectToken) {
    return (
      <ThemeProvider theme={theme}>
        <CssBaseline />
        <GuideProvider paragraphs={guideParagraphs}>
          <ProjectPicker auth={auth} onSelect={selectProject} onCreate={createProject} onSignOut={signOut} />
        </GuideProvider>
      </ThemeProvider>
    );
  }

  return (
    <ThemeProvider theme={theme}>
      <CssBaseline />
      {/* Keyed by project: switching remounts the provider with the new token. */}
      <AgentChatProvider key={project} config={chatConfig}>
        <GuideProvider paragraphs={guideParagraphs}>
          <ProjectWorkspace
            auth={auth}
            credentialMode={credentialMode}
            project={project}
            onboardingGoal={pendingOnboarding?.project === project ? pendingOnboarding.goal : null}
            onOnboardingDone={() => setPendingOnboarding(null)}
            onSwitchProject={selectProject}
            onCreateProject={createProject}
            onSignOut={signOut}
          />
        </GuideProvider>
      </AgentChatProvider>
    </ThemeProvider>
  );
}

/**
 * The signed-in, project-scoped workspace: desk, chart, chat, workers, events,
 * automation and project settings behind one switch, with the session permalink
 * bound to the URL.
 *
 * It is a separate component because `useSessionPermalink` reads the chat
 * context — the hook has to run *inside* <AgentChatProvider>, not beside it.
 */
function ProjectWorkspace({
  auth,
  credentialMode,
  project,
  onboardingGoal,
  onOnboardingDone,
  onSwitchProject,
  onCreateProject,
  onSignOut,
}: {
  auth: AuthState;
  /** "mock" | "api-key" | "subscription" from /auth/config; null when unknown. */
  credentialMode: string | null;
  project: string;
  onSwitchProject: (projectID: string) => void;
  onboardingGoal: string | null;
  onOnboardingDone: () => void;
  onCreateProject: (projectID: string, goal: string) => Promise<void>;
  onSignOut: () => void;
}) {
  // "onboarding" and "guide" are shell-owned views, deliberately not
  // NavEntries: the nav is progressive and its entries are earned by what a
  // project contains (K9). Onboarding is a thing you are doing right now and
  // never come back to from a nav bar; the guide is the opposite case — it is
  // NOT content-driven (it is a book, always there, design §3 G7) and is
  // rendered as a permanent extra button rather than folded into the earned
  // set, so adding it never needed a reveal rule.
  const [view, setView] = useState<View | "onboarding" | "guide">(() => {
    if (onboardingGoal !== null) return "onboarding";
    if (parseGuideHash(window.location.hash) !== null) return "guide";
    return "desk";
  });

  // The guide's own deep link (`#/guide/<slug>`, §3 G7) — kept beside `view`
  // rather than inside GuidePage so a reload or a pasted link lands on the
  // right page before GuidePage ever mounts. `null` slug is "not on the guide
  // right now"; `""` is the bare index (GuidePage redirects that to page one).
  const [guideSlug, setGuideSlug] = useState<string | null>(() => parseGuideHash(window.location.hash));
  useEffect(() => {
    const onHashChange = () => {
      const parsed = parseGuideHash(window.location.hash);
      if (parsed !== null) {
        setGuideSlug(parsed);
        setView("guide");
      }
    };
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);
  const openGuide = useCallback(() => {
    // Preserve whatever page the reader last had open; buildGuideHash(null)
    // → the bare index only the very first time this project opens the guide.
    window.location.hash = buildGuideHash(guideSlug);
    setView("guide");
  }, [guideSlug]);

  // Which nav entries this project has earned (K9). Day one is four; Memory
  // arrives with the first memory, Activity with the first event, and Chart
  // with the first subscription OR the second worker — two workers being the
  // first moment there is a pair to wire, and the canvas being where a wire is
  // drawn, so gating it on a subscription made the first wire unreachable by
  // the gesture designed for it.
  //
  // The hook re-counts on a timer (DI12). It used to count once at mount, so a
  // tab could not appear until the page was reloaded — and the `appeared`
  // announcement below, which exists to name a new entry beside the action that
  // caused it, could never have fired.
  const { visible, appeared, acknowledge } = useNavReveal({ projectId: project });

  // A view can stop being visible only by the project changing under us (a
  // reveal is sticky), but a stale `view` would render a hidden surface — so
  // fall back to the Desk, which is always there. "guide" is exempt from the
  // reveal check the same way "onboarding" is: it is not one of the earned
  // NavEntries (§3 G7 — "it is not content-driven, it is a book").
  const shownView = view === "onboarding" || view === "guide" || visible.includes(view) ? view : "desk";

  // The only number in the chrome (design §3.5): how many things are asking for
  // you — through useAsksCount, which applies the very join the Asks stack
  // applies (doc 21, X7). The badge used to count open attention requests, a
  // superset: it read 2 above a stack of 1.
  //
  // While the Desk is open it already holds both lists, so it reports its own
  // count up and this hook stands down (W4 collapsing X7's duplicate fetch).
  const onDesk = view === "desk";
  const projectToken = auth.projects.find((p) => p.id === project)?.token ?? "";
  // The interview itself: created once, reused on re-entry, and never started
  // at all unless this project is actually being onboarded.
  const { sessionId: onboardSessionId, error: onboardError } = useOnboardingSession({
    apiBase: API,
    token: projectToken,
    goal: onboardingGoal,
    enabled: view === "onboarding",
  });
  // Whether this PROJECT (not this browser tab) is still in its interview —
  // the server-derived replacement for the localStorage-only gate (design §3
  // G1 / A2). Independent of `onboardSessionId` above, which only exists once
  // the onboarding VIEW has actually started or rejoined the session; this
  // one is known as soon as the workspace mounts, so the Desk and Workers can
  // withhold the topology seed and Desk can offer a way back in before the
  // human ever opens the onboarding view this visit.
  const { inInterview, onboardSessionId: interviewSessionId, resolved: interviewResolved } =
    useInterviewState({ apiBase: API, token: projectToken });
  const openOnboarding = useCallback(() => setView("onboarding"), []);
  // The pending goal is only a carrier for the text between project creation
  // and the interview's first seed message (design §3 G1) — once the server
  // says the interview is over, forget it, or a later reload of this project
  // would read the stale goal and jump straight back into the onboarding view
  // (the exact "state of the browser, not the project" bug G1 fixes).
  useEffect(() => {
    if (interviewResolved && !inInterview) onOnboardingDone();
  }, [interviewResolved, inInterview, onOnboardingDone]);
  const [deskAsks, setDeskAsks] = useState(0);
  const { count: fetchedAsks } = useAsksCount({ enabled: !onDesk });
  const openAsks = onDesk ? deskAsks : fetchedAsks;

  // URL ⇄ active session, both directions: a pasted /p/<project>/s/<session>
  // resumes that session, and whatever session is open is already permalinked.
  const { openSession, routeSessionId } = useSessionPermalink({ projectId: project });

  // Whenever the routed session CHANGES, show it. Same reasoning as
  // `showSession` below, applied to the two paths that do not go through it: a
  // pasted permalink (URL → state) and "New session" in the sidebar (state →
  // URL). Both used to resume a session behind the Desk — the landing view
  // since K1 — so the user clicked a link, the session really did resume, and
  // the screen showed the Desk's "no workers yet" panel. Nothing said so.
  //
  // Render-phase and keyed on the id, not an effect: an effect would paint the
  // wrong view first. Switching only on a *change* leaves the human free to
  // walk to Workers or Settings with a session open.
  const shownSession = useRef<string | null>(null);
  if (routeSessionId !== null && shownSession.current !== routeSessionId) {
    shownSession.current = routeSessionId;
    setView("chat");
  }

  // A clock on the chart is a link to the schedule it draws (K3: clocks render
  // on the canvas but are never edited there). Since K9 that schedule lives on
  // its worker's Triggers tab rather than on a project-wide Automation page, so
  // the link selects the worker and asks for that tab.
  //
  // `initialTab` is applied on CHANGE rather than held, so the human is free to
  // walk to Configuration afterwards — a deep link should land you somewhere,
  // not pin you there.
  const [workerFromChart, setWorkerFromChart] = useState<string | null>(null);
  const [triggersFromChart, setTriggersFromChart] = useState(false);
  const openScheduleFromChart = useCallback((_scheduleId: string, worker: string) => {
    setWorkerFromChart(worker);
    setTriggersFromChart(true);
    setView("workers");
  }, []);

  // A job row in the workers view is a link to the session that ran it — open
  // it *and* show it, since resuming a session behind a hidden tab would look
  // like nothing happened.
  const showSession = useCallback(
    (sessionId: string) => {
      openSession(sessionId);
      setView("chat");
    },
    [openSession],
  );

  return (
    <Box sx={{ display: "flex", height: "100vh" }}>
      <Box sx={{ width: 280, borderRight: 1, borderColor: "divider", display: "flex", flexDirection: "column", minHeight: 0 }}>
        {/* Which model answers, on every view, permanently (RD18). In mock mode
            — the default — everything below this line is canned output. */}
        <Box sx={{ px: 1.5, pt: 1.5 }}>
          <CredentialModeBadge mode={credentialMode} />
        </Box>
        <ViewNav
          view={shownView}
          entries={visible}
          // Navigating away no longer ends the interview (design §3 G1): it is
          // a state of the project, derived above from the server, so a nav
          // click can never again strand the human with no way back to the
          // charter and Approve. C1 arrived with the opposite behaviour still
          // in this handler — `if (view === "onboarding") onOnboardingDone()`
          // — because its branch predates A2; taking C1's side of the conflict
          // would have silently reverted A2's whole ticket, so that line is
          // deliberately gone and only the guide's cleanup survives from C1.
          onChange={(next) => {
            // Leaving the guide clears its hash (a history REPLACE, so this
            // does not cost a back-button step either): otherwise a reload
            // on, say, Desk would find the guide's stale `#/guide/...` still
            // in the address bar and jump straight back to it.
            if (view === "guide") {
              window.history.replaceState(null, "", window.location.pathname + window.location.search);
            }
            setView(next);
          }}
          asks={openAsks}
          onOpenGuide={openGuide}
        />
        <RevealNotice appeared={appeared} onDismiss={acknowledge} />
        {/* The sidebar stays mounted in every view: it carries the project
            switcher and the session list, which are how you leave a view. */}
        <Box sx={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
          <Sidebar
            auth={auth}
            project={project}
            onSwitchProject={onSwitchProject}
            onCreateProject={onCreateProject}
            onSignOut={onSignOut}
            onboardSessionId={interviewSessionId}
            inInterview={inInterview}
            onOpenOnboarding={openOnboarding}
          />
        </Box>
      </Box>

      <Box sx={{ flex: 1, minWidth: 0, overflowY: "auto" }}>
        {shownView === "desk" && (
          <DeskPage
            projectId={project}
            refreshMs={LIVE_REFRESH_MS}
            onOpenSession={showSession}
            onAsksCount={setDeskAsks}
            onStartFromTopology={() => setView("workers")}
            onOpenChat={() => setView("chat")}
            inInterview={inInterview}
            onOpenOnboarding={openOnboarding}
          />
        )}
        {/* Schedules are not edited on the canvas (K3): a clock is a deep link
            to the row on Automation. */}
        {shownView === "chart" && (
          <OrgChartPage projectId={project} onOpenAutomation={openScheduleFromChart} />
        )}
        {shownView === "chat" && <AgentChat projectId={project} />}
        {shownView === "workers" && (
          <WorkersPage
            projectId={project}
            onOpenSession={showSession}
            selected={workerFromChart}
            onSelect={setWorkerFromChart}
            initialTab={triggersFromChart ? "triggers" : undefined}
            hideTopologySeed={inInterview}
          />
        )}
        {/* No fetchConfigEvents: GET /agent/config-events is mounted, so the
            changelog tab reads the route directly. */}
        {shownView === "memory" && <MemoryBrowserPage projectId={project} onOpenSession={showSession} />}
        {shownView === "activity" && (
          <ActivityPage
            projectId={project}
            refreshMs={LIVE_REFRESH_MS}
            onOpenSession={showSession}
          />
        )}
        {shownView === "settings" && <ProjectSettingsPage projectId={project} />}
        {shownView === "onboarding" && (
          <OnboardingPage
            sessionId={onboardSessionId}
            sessionError={onboardError}
            refreshMs={4000}
            projectId={project}
          />
        )}
        {shownView === "guide" && <GuidePage slug={guideSlug} />}
      </Box>
    </Box>
  );
}

/**
 * The reveal, announced in words rather than by a pulsing badge (design 28
 * §3.2). Colour in this console is never spent on chrome, and the asks badge is
 * the only number the design allows there — so a new nav entry says what it is
 * for, once, and then gets out of the way.
 */
function RevealNotice({ appeared, onDismiss }: { appeared: NavEntry[]; onDismiss: () => void }) {
  const reduced = usePrefersReducedMotion();
  if (appeared.length === 0) return null;
  return (
    <Box
      role="status"
      data-testid="nav-reveal-notice"
      sx={{
        mx: 1,
        mb: 1,
        p: 1,
        borderLeft: 2,
        borderColor: "secondary.main",
        fontSize: 12,
        lineHeight: 1.5,
        // The whole motion budget for this feature.
        transition: reduced ? "none" : "opacity 180ms ease-out",
      }}
    >
      {appeared.map((entry) => (
        <Box key={entry} sx={{ mb: 0.5 }}>
          {navRevealSentence(entry)}
        </Box>
      ))}
      <Button size="small" onClick={onDismiss} sx={{ textTransform: "none", fontSize: 11, minWidth: 0, p: 0 }}>
        Got it
      </Button>
    </Box>
  );
}

/**
 * The view switch. Draws only what the project has revealed (K9), in the
 * library's canonical order — items appear IN PLACE and the list never
 * reorders, because a control that moves is worse than one that appears.
 */
function ViewNav({
  view,
  entries,
  onChange,
  asks,
  onOpenGuide,
}: {
  // Widened for the shell's two transient views ("onboarding", "guide"),
  // neither of which is a NavEntry and so highlights nothing in the
  // `entries.map` below — correct for onboarding (a thing you are doing, not
  // a place you go back to) and for the guide for the opposite reason: it is
  // drawn unconditionally, after this loop, because it is not earned by what
  // the project contains (design §3 G7 — "it is not content-driven, it is a
  // book").
  view: View | "onboarding" | "guide";
  entries: NavEntry[];
  onChange: (v: View) => void;
  asks: number;
  onOpenGuide: () => void;
}) {
  return (
    <Box
      sx={{ p: 1, borderBottom: 1, borderColor: "divider", display: "flex", flexWrap: "wrap", gap: 0.5 }}
    >
      {entries.map((key) => {
        // Desk carries the one badge the design allows in the chrome (§3.5).
        const badge = key === "desk" ? asks : 0;
        return (
          <Button
            key={key}
            size="small"
            variant={view === key ? "contained" : "text"}
            onClick={() => onChange(key)}
            data-testid={`nav-${key}`}
            sx={{ textTransform: "none", flexGrow: 1, minWidth: 0 }}
          >
            {badge > 0 ? `${NAV_LABELS[key]} ${badge}` : NAV_LABELS[key]}
          </Button>
        );
      })}
      <Button
        size="small"
        variant={view === "guide" ? "contained" : "text"}
        onClick={onOpenGuide}
        data-testid="nav-guide"
        sx={{ textTransform: "none", flexGrow: 1, minWidth: 0 }}
      >
        Guide
      </Button>
    </Box>
  );
}

// Dev mode keeps the original minimal list.
function DevSessionList() {
  return <AgentSessionList />;
}
