// Progressive navigation — which nav entries a project has earned
// (`docs/product/28-console-ia-design.md` §3, decision K9).
//
// "The initial setup of a project is always going to be different to the
// ongoing maintenance of it." A console built for an operator tending a fleet
// met a human who had none: eight buttons, seven of which opened empty rooms.
//
// So an entry appears when it has something in it. Day one is four buttons;
// a working project six; a project with a wired fleet seven. Nothing here is
// onboarding — there is no tutorial state, no completion flag and no timer.
// The nav is simply a function of what the project contains, plus what it has
// already shown (see `sticky` below).
//
// Pure: no React, no window, no storage. The shell owns persistence.

/**
 * Every nav entry, in the order they are always drawn.
 *
 * The order is FIXED and this array is its only definition. Items appear in
 * place; the list never reorders, because a control that moves is worse than
 * one that appears — it breaks exactly the muscle memory a progressive nav is
 * trying to build.
 */
export const NAV_ENTRIES = [
  'desk',
  'chat',
  'workers',
  'memory',
  'activity',
  'chart',
  'settings',
] as const

export type NavEntry = (typeof NAV_ENTRIES)[number]

/**
 * The entries a project always has, from the very first visit.
 *
 * Desk stays the landing view even when empty (K1) — its first-run panel *is*
 * the onboarding screen, so hiding it would remove the one surface that tells a
 * new human what to do next.
 */
export const NAV_ALWAYS = ['desk', 'chat', 'workers', 'settings'] as const

/** An entry that is always present. */
export type NavAlwaysEntry = (typeof NAV_ALWAYS)[number]

/**
 * An entry a project has to earn. Derived rather than listed, so adding a nav
 * entry to `NAV_ENTRIES` without giving it a reveal rule is a type error rather
 * than a button that never appears.
 */
export type NavConditionalEntry = Exclude<NavEntry, NavAlwaysEntry>

/** `NAV_ALWAYS.includes` over the wide type, without widening the constant. */
function isAlways(entry: NavEntry): boolean {
  return (NAV_ALWAYS as readonly NavEntry[]).includes(entry)
}

/** What the project contains. Everything a reveal rule is allowed to read. */
export interface NavCounts {
  workers: number
  memories: number
  events: number
  /**
   * Subscriptions — the chart's trigger, and deliberately NOT the worker count.
   *
   * Five workers with nothing wired between them have no shape: drawing them
   * gives five plates in a row, which a list does better. The chart earns its
   * place when the fleet stops being a list and becomes a graph — when one
   * worker wakes another. Schedules do not count: a clock is a per-worker fact
   * and the worker's Triggers tab renders it better than a dial on a canvas.
   */
  subscriptions: number
}

/** The rule each conditional entry answers to. */
const REVEAL_RULES: Record<NavConditionalEntry, (counts: NavCounts) => boolean> = {
  memory: (c) => c.memories > 0,
  activity: (c) => c.events > 0,
  chart: (c) => c.subscriptions > 0,
}

export interface NavRevealResult {
  /** What to draw, in canonical order. */
  visible: NavEntry[]
  /**
   * The conditional entries unlocked so far, for the caller to persist. Once an
   * entry is in here it stays: deleting the last worker un-reveals nothing,
   * because the history still exists and the surface that reads it must remain.
   * Un-revealing is how a console teaches a human that it will not stay where
   * they left it.
   */
  sticky: NavEntry[]
  /**
   * Entries that became visible on THIS evaluation — normally none, or one.
   *
   * The shell uses it to name what appeared in the confirmation of the action
   * that caused it (design 28 §3.2): the reveal is announced in words, not by a
   * pulsing badge, because colour in this console is never spent on chrome.
   */
  appeared: NavEntry[]
}

/**
 * Which nav entries a project shows.
 *
 * `sticky` is whatever the caller persisted last time; pass an empty list on a
 * project that has never been opened. The result's `sticky` is what to store.
 */
export function revealedNav(counts: NavCounts, sticky: Iterable<NavEntry> = []): NavRevealResult {
  const held = new Set<NavEntry>(sticky)
  const appeared: NavEntry[] = []

  for (const entry of NAV_ENTRIES) {
    if (isAlways(entry) || held.has(entry)) continue
    const rule = REVEAL_RULES[entry as NavConditionalEntry]
    if (rule?.(counts)) {
      held.add(entry)
      appeared.push(entry)
    }
  }

  // Canonical order in both lists — the caller must never have to sort, because
  // a caller that sorts is a caller that can sort differently.
  const visible = NAV_ENTRIES.filter((e) => isAlways(e) || held.has(e))
  return {
    visible: [...visible],
    sticky: NAV_ENTRIES.filter((e) => held.has(e)),
    appeared: NAV_ENTRIES.filter((e) => appeared.includes(e)),
  }
}

/** What each entry is called on screen. */
export const NAV_LABELS: Record<NavEntry, string> = {
  desk: 'Desk',
  chat: 'Chat',
  workers: 'Workers',
  memory: 'Memory',
  activity: 'Activity',
  chart: 'Chart',
  settings: 'Settings',
}

/**
 * The sentence that announces a newly revealed entry, in the operator's own
 * vocabulary (design §11) — named by what it does, never by how it is built.
 *
 * This is the whole motion budget for the reveal: an interface that explains a
 * change in words does not need to flash.
 */
export function navRevealSentence(entry: NavEntry): string {
  switch (entry) {
    case 'memory':
      return 'Memory is now in the sidebar — it holds what your workers have written down.'
    case 'activity':
      return 'Activity is now in the sidebar — it lists everything this project does.'
    case 'chart':
      return 'Chart is now in the sidebar — it draws which worker wakes which.'
    default:
      return `${NAV_LABELS[entry]} is now in the sidebar.`
  }
}
