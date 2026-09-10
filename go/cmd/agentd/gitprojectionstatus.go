package main

// gitprojectionstatus.go — what GET /agent/git-projection reads (G23).
//
// # Why an adapter exists at all
//
// httpapi owns the route and the wire shape. It declares two seams onto the
// projection's durable state:
//
//   - httpapi.GitProjectionStore — the state row. *agentdb.Store satisfies it
//     directly, and httpapi auto-fills it from AgentDB.
//   - httpapi.GitProjectionNotesStore — an OPTIONAL extension, found by type
//     assertion, reporting the per-file quarantine and ignored lists.
//
// agentdb cannot satisfy the second one: its methods would have to return
// []httpapi.GitProjectionNote, and httpapi already imports agentdb. So the
// translation happens HERE, in the one package that is allowed to know about
// both, and main.go hands the result to httpapi.Config.GitProjection instead of
// letting it auto-fill.
//
// # The one piece of judgement in this file
//
// 🔴 The route classifies health by MATCHING THE ENGINE'S ERROR TEXT, because
// when it was written the state row carried no code (DI17). It is finished and
// is not edited here. The state row now carries a KIND — decided in
// gitprojection.go at the point the error was raised, by errors.Is against
// gitproj's own sentinels — and the kind is the authority.
//
// So this adapter makes the two agree, in the kind's favour, by handing the
// route a state whose message spells what the kind says:
//
//   - unrenderable / not_fast_forward: the stored message is already the
//     sentinel's own text, because the error WAS that sentinel — so in the
//     normal case nothing is rewritten and the console shows the real sentence,
//     field path and all. Only if the two disagree (gitproj reworded a sentinel
//     and nobody noticed) does the kind win, and the message is rendered into a
//     shape the matcher agrees with. That is the whole difference the kind
//     buys: a reword now degrades a field name instead of silently
//     reclassifying a production failure.
//   - quarantined: the route reports "quarantined" from a NON-EMPTY quarantine
//     list and an EMPTY message — an inbound rejection is not an outbound
//     publish failure and must not be shown as one. The per-file notes say
//     strictly more than the summary sentence did, so the summary is dropped
//     when, and only when, the notes are there to replace it.
//
// Nothing else is touched. In particular the watermarks, the SHAs and
// last_error_at are passed through exactly as stored.

import (
	"context"
	"strings"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/gitproj"
	"github.com/badcodetv/agent-bob/httpapi"
)

// gitProjectionStatusStore is the slice of *agentdb.Store this file needs.
type gitProjectionStatusStore interface {
	GetGitProjectionState(ctx context.Context, project string) (*agentdb.GitProjectionState, error)
	GetGitProjectionNotes(ctx context.Context, project string) (quarantine, ignored []agentdb.GitProjectionNote, err error)
}

// gitProjectionStatusSource implements both of httpapi's seams.
type gitProjectionStatusSource struct{ store gitProjectionStatusStore }

var (
	_ httpapi.GitProjectionStore      = (*gitProjectionStatusSource)(nil)
	_ httpapi.GitProjectionNotesStore = (*gitProjectionStatusSource)(nil)
)

// newGitProjectionStatusSource returns the seam, or a nil INTERFACE when there
// is no store.
//
// It takes the CONCRETE store rather than the interface above so that the
// sqlite fallback — where agentDB is a nil *agentdb.Store — returns a nil
// interface value and not a non-nil interface wrapping a nil pointer. httpapi
// branches on `cfg.GitProjection == nil` to answer state_available=false, and
// a typed nil would sail past that check and call through a nil receiver.
// Tests build the struct directly with a fake.
func newGitProjectionStatusSource(store *agentdb.Store) httpapi.GitProjectionStore {
	if store == nil {
		return nil
	}
	return &gitProjectionStatusSource{store: store}
}

func (s *gitProjectionStatusSource) GetGitProjectionState(ctx context.Context, project string) (*agentdb.GitProjectionState, error) {
	st, err := s.store.GetGitProjectionState(ctx, project)
	if err != nil || st == nil {
		return st, err
	}
	out := *st // a copy: the route must not be able to write back through this
	out.LastError = s.messageForKind(ctx, project, out.LastErrorKind, out.LastError)
	return &out, nil
}

// messageForKind is the kind-wins translation described in the file header.
func (s *gitProjectionStatusSource) messageForKind(ctx context.Context, project, kind, msg string) string {
	if strings.TrimSpace(msg) == "" {
		return msg
	}
	switch kind {
	case agentdb.GitProjectionErrorUnrenderable:
		if strings.Contains(msg, gitUnrenderableMarker) {
			return msg
		}
		// gitproj reworded its refusal. Say the kind in the shape the route
		// reads, keeping the original sentence: the operator loses the parsed
		// FIELD NAME (which is a real loss, and why the tripwire test in
		// httpapi is worth keeping) and keeps the diagnosis.
		return (&gitproj.UnrenderableError{
			Struct: "projection",
			Field:  "field",
			Reason: msg,
		}).Error()
	case agentdb.GitProjectionErrorNotFastForward:
		if strings.Contains(msg, gitproj.ErrNotFastForward.Error()) {
			return msg
		}
		return gitproj.ErrNotFastForward.Error() + ": " + msg
	case agentdb.GitProjectionErrorNeedsAdoption:
		// G27. The message IS the operator instruction — it names
		// POST /agent/git-bootstrap — so it is passed through untouched and
		// deliberately not rewritten into any sentinel's shape. httpapi's
		// classifier has no word for this kind and reports "failing" with this
		// sentence attached, which is the honest answer: the projection is not
		// publishing, and the sentence says exactly why and what to do.
		return msg
	case agentdb.GitProjectionErrorQuarantined:
		// An inbound rejection, not a publish failure. Drop the summary only
		// when the per-file notes that replace it actually exist — with no
		// notes to show, "failing, and here is the sentence" beats a bare
		// "quarantined" with nothing under it.
		quarantine, _, err := s.store.GetGitProjectionNotes(ctx, project)
		if err == nil && len(quarantine) > 0 {
			return ""
		}
		return msg
	default:
		return msg
	}
}

// gitUnrenderableMarker is the fixed half of gitproj.UnrenderableError.Error().
// It is spelled from the type itself rather than typed out, so this file cannot
// drift from the engine the way a literal would.
var gitUnrenderableMarker = func() string {
	full := (&gitproj.UnrenderableError{Struct: "S", Field: "F", Reason: "R"}).Error()
	const prefix = "gitproj: S.F"
	return strings.TrimSuffix(strings.TrimPrefix(full, prefix), "R")
}()

// GitProjectionNotes reports the per-file half: which file quarantined the last
// inbound push, and which edits were understood and deliberately not applied.
func (s *gitProjectionStatusSource) GitProjectionNotes(ctx context.Context, project string) (quarantine, ignored []httpapi.GitProjectionNote, err error) {
	q, i, err := s.store.GetGitProjectionNotes(ctx, project)
	if err != nil {
		return nil, nil, err
	}
	return toHTTPGitProjectionNotes(q), toHTTPGitProjectionNotes(i), nil
}

func toHTTPGitProjectionNotes(in []agentdb.GitProjectionNote) []httpapi.GitProjectionNote {
	if len(in) == 0 {
		return nil
	}
	out := make([]httpapi.GitProjectionNote, 0, len(in))
	for _, n := range in {
		out = append(out, httpapi.GitProjectionNote{Path: n.Path, Reason: n.Reason, At: n.NotedAt})
	}
	return out
}
