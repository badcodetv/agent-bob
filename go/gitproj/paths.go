package gitproj

import (
	"fmt"
	"regexp"
	"strings"
)

// DefaultSubfolder is the default root of the projection inside a project's
// git clone (design doc §B "Layout"): everything this package writes lives
// under `orange/` unless a project has configured a different subfolder.
const DefaultSubfolder = "orange"

// nameRe is the one shape a name segment is allowed to take. It is
// deliberately restrictive — DNS-label shaped, like a Kubernetes name —
// rather than "whatever characters happen to work": the only names that
// reach this package are validated ones. See the package comment and §D of
// the design doc for why that matters: an unvalidated name is how a worker
// prompt or an inbound commit turns into a write outside `orange/`.
var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

const maxNameLen = 63

// Kind identifies which projected file a path names.
type Kind string

const (
	KindSettings     Kind = "settings"
	KindWorker       Kind = "worker"
	KindSkill        Kind = "skill"
	KindSubscription Kind = "subscription"
	KindSchedule     Kind = "schedule"
	KindImage        Kind = "image"
	KindMemory       Kind = "memory"
)

// ValidateName reports whether name is a safe path segment: non-empty, at
// most 63 characters, lowercase alphanumeric with internal (never leading or
// trailing) single dashes — the same shape Kubernetes uses for object names,
// chosen for the same reason: it is unambiguous under path-joining and
// filesystem/URL normalisation, with no `.`, `/`, or case-folding surprises
// to exploit.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("gitproj: name must not be empty")
	}
	if len(name) > maxNameLen {
		return fmt.Errorf("gitproj: name %q exceeds max length %d", name, maxNameLen)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("gitproj: invalid name %q: must match %s", name, nameRe.String())
	}
	return nil
}

// SettingsPath returns the fixed path of the project settings file.
func SettingsPath(subfolder string) string {
	return join(subfolder, "settings.md")
}

// WorkerPath returns the path a worker named name renders to. It returns an
// error if name is not a valid path segment (see ValidateName).
func WorkerPath(subfolder, name string) (string, error) {
	return itemPath(subfolder, "workers", name)
}

// SkillPath returns the path a skill named name renders to.
func SkillPath(subfolder, name string) (string, error) {
	return itemPath(subfolder, "skills", name)
}

// SubscriptionPath returns the path a subscription with the given id renders
// to. Subscription and schedule "names" are opaque IDs, not human-chosen
// names, but they pass through the same ValidateName check: an ID is still
// attacker-influenced data once anything downstream of the database (a
// UUID-typo'd migration, a restored backup) could hand this package a
// surprising string, so it gets no special trust.
func SubscriptionPath(subfolder, id string) (string, error) {
	return itemPath(subfolder, "subscriptions", id)
}

// SchedulePath returns the path a schedule with the given id renders to.
func SchedulePath(subfolder, id string) (string, error) {
	return itemPath(subfolder, "schedules", id)
}

// ImagePath returns the path an image named name renders to.
func ImagePath(subfolder, name string) (string, error) {
	return itemPath(subfolder, "images", name)
}

// MemoryPath returns the path a named-document memory named name renders to.
func MemoryPath(subfolder, name string) (string, error) {
	return itemPath(subfolder, "memory", name)
}

func itemPath(subfolder, dir, name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	return join(subfolder, dir, name+".md"), nil
}

func join(subfolder string, parts ...string) string {
	if subfolder == "" {
		subfolder = DefaultSubfolder
	}
	return strings.Join(append([]string{subfolder}, parts...), "/")
}

// dirKinds maps the directory a projected file lives under to the Kind it
// represents. settings.md is handled separately in ParsePath since it has
// no directory of its own.
var dirKinds = map[string]Kind{
	"workers":       KindWorker,
	"skills":        KindSkill,
	"subscriptions": KindSubscription,
	"schedules":     KindSchedule,
	"images":        KindImage,
	"memory":        KindMemory,
}

// ParsePath turns a repo-relative path back into (kind, name). It is the
// inverse of the *Path constructors above, and it is exactly as strict:
// anything that is not one of the exact shapes those constructors produce
// under subfolder is rejected, by construction rather than by a denylist of
// bad inputs. In particular a path is only accepted if, after rejecting `..`
// and `.` segments and any leading slash, it still starts with subfolder —
// so nothing this function accepts can resolve outside it.
func ParsePath(subfolder, path string) (kind Kind, name string, err error) {
	if subfolder == "" {
		subfolder = DefaultSubfolder
	}

	if path == "" {
		return "", "", fmt.Errorf("gitproj: empty path")
	}
	if strings.HasPrefix(path, "/") {
		return "", "", fmt.Errorf("gitproj: path %q must be repo-relative, not absolute", path)
	}

	segments := strings.Split(path, "/")
	for _, seg := range segments {
		if seg == "" {
			return "", "", fmt.Errorf("gitproj: path %q has an empty segment", path)
		}
		if seg == "." || seg == ".." {
			return "", "", fmt.Errorf("gitproj: path %q contains a %q segment", path, seg)
		}
	}

	if segments[0] != subfolder {
		return "", "", fmt.Errorf("gitproj: path %q is not under subfolder %q", path, subfolder)
	}
	segments = segments[1:]

	if len(segments) == 1 && segments[0] == "settings.md" {
		return KindSettings, "", nil
	}

	if len(segments) != 2 {
		return "", "", fmt.Errorf("gitproj: path %q does not match a known projection shape", path)
	}

	dir, file := segments[0], segments[1]
	k, ok := dirKinds[dir]
	if !ok {
		return "", "", fmt.Errorf("gitproj: path %q: unknown directory %q", path, dir)
	}

	if !strings.HasSuffix(file, ".md") {
		return "", "", fmt.Errorf("gitproj: path %q: file must end in .md", path)
	}
	name = strings.TrimSuffix(file, ".md")
	if err := ValidateName(name); err != nil {
		return "", "", fmt.Errorf("gitproj: path %q: %w", path, err)
	}

	return k, name, nil
}
