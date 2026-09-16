package connections

import (
	"fmt"
	"sort"
)

// Holds reports whether a caller holding the given grant list can reach
// name — either by an exact match or because the list carries Wildcard.
func Holds(held []string, name string) bool {
	for _, h := range held {
		if h == Wildcard || h == name {
			return true
		}
	}
	return false
}

// ExpandGrants turns a worker's raw grant list (which may carry Wildcard)
// into the concrete, sorted, deduplicated set of connection names it
// resolves to for project. A name the project does not have is dropped
// silently — the caller (Servers) is responsible for logging anything it
// skips. Wildcard on a project with no connections expands to nothing.
func ExpandGrants(r *Registry, project string, grants []string) []string {
	all := r.Names(project) // already sorted
	if Holds(grants, Wildcard) {
		return all
	}
	have := make(map[string]bool, len(all))
	for _, n := range all {
		have[n] = true
	}
	seen := make(map[string]bool, len(grants))
	out := make([]string, 0, len(grants))
	for _, g := range grants {
		if g == Wildcard || !have[g] || seen[g] {
			continue
		}
		seen[g] = true
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// CanGrant enforces the trust rule (design decision 3): a caller holding
// `held` may transition a worker's grants from `before` to `after` only if
// every name added by that transition (present in after, absent from
// before) is itself held by the caller — adding Wildcard requires the caller
// to hold Wildcard. Removing a name is always allowed, even by a caller
// holding nothing. The error names the first offending connection found
// (in the order `after` lists them).
func CanGrant(held, before, after []string) error {
	beforeSet := make(map[string]bool, len(before))
	for _, b := range before {
		beforeSet[b] = true
	}
	for _, a := range after {
		if beforeSet[a] {
			continue // already granted: not an addition
		}
		if !Holds(held, a) {
			return fmt.Errorf("cannot grant %q: caller does not hold it (holds %v)", a, held)
		}
	}
	return nil
}

// Covers reports whether held covers every entry of target — the "cannot
// steer someone stronger" rule (design decision 3): a caller may mutate a
// worker's configuration only while holding everything that worker holds.
// Wildcard in target requires Wildcard in held (held explicitly listing
// every current connection is not enough, since target's "*" also covers
// connections added later). An empty target is always covered.
func Covers(held, target []string) bool {
	if len(target) == 0 {
		return true
	}
	for _, t := range target {
		if t == Wildcard {
			if !Holds(held, Wildcard) {
				return false
			}
			continue
		}
		if !Holds(held, t) {
			return false
		}
	}
	return true
}
