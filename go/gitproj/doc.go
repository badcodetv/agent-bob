// Package gitproj is the pure render/parse layer for the git projection
// (design/2026-09-09-git-projection.md). It knows how to turn a name into a
// safe repo-relative path and how to read and write the YAML-frontmatter
// markdown format that every projected file uses. It does not know about git,
// HTTP, or the database — Render and Parse in this package are pure
// functions of their arguments, nothing more.
//
// That purity is not a style preference, it is the property the whole design
// leans on. The projection worker decides whether a commit is needed by
// comparing freshly rendered bytes against what is already in the git tree;
// that comparison is only meaningful if rendering the same configuration
// twice produces byte-identical output, in whatever order the caller happens
// to supply it. So this package treats determinism as a correctness
// requirement, not a nicety: frontmatter keys are written in a fixed,
// tested order every time, never map iteration order.
//
// Path handling lives here for the same reason paths.go documents in detail:
// a name is data that arrived from a worker prompt, a config event, or (once
// import lands) an inbound git commit written by anyone with push access to
// the mirror. An unvalidated name is a path-traversal primitive, so this
// package is the one place that turns a name into a path and the one place
// that turns a path back into a name, and both directions are pinned by
// test.
package gitproj
