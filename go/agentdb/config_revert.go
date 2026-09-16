package agentdb

// config_revert.go — reverting one configuration change (design §D).
//
// The whole design fits in one sentence, and it is the sentence
// `TestRestoreIsForward` already enforces for restores: a revert is a FORWARD
// compensating write through the ordinary logged mutations. Nothing rewrites
// history, nothing deletes a record, and the revert itself appears in the log
// as an ordinary change with a rationale naming what it undid. Git revert,
// never git reset.
//
// Three properties fall out of that and are worth stating before the code:
//
//  1. **Only the newest change to an entity can be reverted** (Decision D2).
//     Payloads are whole rows, so writing event #180's predecessor forward
//     after #195 touched the same worker would silently erase #195's change
//     as well. The refusal names the intervening records so a human can see
//     what is in the way.
//
//  2. **Reverting a revert is therefore refused, not doubled.** The first
//     revert appends a new record for the entity, so the original is no
//     longer newest and the second attempt hits rule 1. Reverting the
//     REVERT is the way back, and it works, because that record IS newest.
//
//  3. **"Previous" means the preceding record for the same entity**, not a
//     fold of the whole project. FoldTo answers "what did everything look
//     like at time T", which is a different question and a much more
//     expensive one; the chain for one entity is what a revert needs.
//
// Not everything is revertable, and the refusals say why rather than doing
// something approximate:
//
//   - `topology_apply` records a DECISION, not a row. Nothing folds it back
//     into a table, and "un-applying" a topology would mean deleting workers
//     a human may have edited since. Revert the individual changes instead.
//   - images and skills are append-only at the tool surface (§13, §14): there
//     is no delete verb to compensate a create with.
//   - `connection_connect`/`connection_disconnect` carry metadata only; the
//     sealed credential is deliberately kept out of the log, so there is no
//     state to write forward. The console's buttons are the way back.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// ErrRevertRefused is a revert this design will not perform: a non-newest
// target, or an entity kind with no inverse. It is a refusal, never a failure —
// nothing was written.
var ErrRevertRefused = errors.New("agentdb: revert refused")

// RevertEvent writes the compensating change for one config event and returns
// the record that change appended.
//
// cw carries the human's reason. It is REQUIRED in spirit and defaulted in
// code: a revert with no reason is the one change in the log where "why" is
// most obviously knowable, so an empty rationale is filled in with the event
// being reverted rather than left blank.
func (s *Store) RevertEvent(ctx context.Context, project, eventID string, cw ConfigWrite) (*ConfigEvent, error) {
	target, err := s.GetConfigEvent(ctx, project, eventID)
	if err != nil {
		return nil, err
	}
	ref, err := EntityRefFor(target)
	if err != nil {
		return nil, fmt.Errorf("%w: %s (seq %d) cannot be keyed to an entity: %v",
			ErrRevertRefused, target.Action, target.Seq, err)
	}
	if err := revertableKind(ref, target); err != nil {
		return nil, err
	}

	// The entity's own chain, newest first. Bounded by the entity filter, so
	// this is a short list even in a project with a long history.
	chain, err := s.ListConfigEvents(ctx, ConfigEventQuery{Project: project, Entity: ref.String()})
	if err != nil {
		return nil, err
	}
	if err := requireNewest(chain, target, ref); err != nil {
		return nil, err
	}
	// chain[0] is the target; chain[1] is what the entity looked like before it.
	var previous *ConfigEvent
	if len(chain) > 1 {
		previous = chain[1]
	}

	if strings.TrimSpace(cw.Rationale) == "" {
		cw.Rationale = fmt.Sprintf("revert of %s (seq %d, event %s)", target.Action, target.Seq, target.ID)
	}

	// One transaction: the compensating mutation and its own config event
	// commit together, exactly as every other configuration write does.
	var written *ConfigEvent
	var deferred []*ConfigEvent
	err = s.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txs := s.txStore(tx, &deferred)
		return revertInto(ctx, txs, project, ref, target, previous, cw)
	})
	if err != nil {
		return nil, err
	}
	// The post-commit hook fires for the compensating write like any other, so
	// `config.changed` is emitted for the revert too — a revert is a change.
	for _, ev := range deferred {
		written = ev
		if s.configHook != nil {
			s.configHook(ctx, ev)
		}
	}
	if written == nil {
		return nil, fmt.Errorf("agentdb: revert of %s wrote no config event", eventID)
	}
	return written, nil
}

// revertableKind refuses the kinds that have no inverse, with the reason.
func revertableKind(ref EntityRef, target *ConfigEvent) error {
	switch ref.Kind {
	case EntityTopology:
		return fmt.Errorf("%w: %s records a decision, not a row — applying a topology wrote "+
			"workers, subscriptions, schedules and settings, and there is no single change to "+
			"put back. Revert the individual records it created instead",
			ErrRevertRefused, target.Action)
	case EntityImage, EntitySkill:
		return fmt.Errorf("%w: %s is append-only — %ss have no delete verb at the tool surface, "+
			"so there is nothing to compensate a create with",
			ErrRevertRefused, target.Action, ref.Kind)
	case EntityConnection:
		return fmt.Errorf("%w: %s — a connection credential is not in the log, so there is nothing "+
			"to put back — use Connect Google or Disconnect in Settings",
			ErrRevertRefused, target.Action)
	}
	return nil
}

// requireNewest implements Decision D2. The refusal NAMES the records in the
// way, because "not the newest" without saying what changed since is a dead
// end for whoever is reading it.
func requireNewest(chain []*ConfigEvent, target *ConfigEvent, ref EntityRef) error {
	if len(chain) == 0 || chain[0].ID == target.ID {
		return nil
	}
	var after []string
	for _, ev := range chain {
		if ev.ID == target.ID {
			break
		}
		after = append(after, fmt.Sprintf("%s (seq %d)", ev.Action, ev.Seq))
	}
	if len(after) == 0 {
		// The target is not in its own entity's chain at all. Only reachable if
		// the payload stopped keying to the same entity, which is corruption —
		// and silently reverting something else would be much worse.
		return fmt.Errorf("%w: %s (seq %d) is not in the history of %s",
			ErrRevertRefused, target.Action, target.Seq, ref)
	}
	return fmt.Errorf("%w: %s (seq %d) is not the newest change to %s — %s came after it. "+
		"Payloads are whole rows, so putting this one back would erase %s too. "+
		"Revert the newest change first",
		ErrRevertRefused, target.Action, target.Seq, ref,
		strings.Join(after, ", "), pluralThose(len(after)))
}

func pluralThose(n int) string {
	if n == 1 {
		return "that one"
	}
	return "those"
}

// revertInto performs the compensating mutation through the ordinary logged
// store methods on tx.
//
// The shape is the same for every kind: a delete is compensated by recreating
// the row the tombstone preserved (with its ORIGINAL id — a restored
// subscription that came back under a new id would not be the same edge in the
// org chart); a create with no predecessor is compensated by a delete; anything
// else is compensated by writing the predecessor's payload forward.
func revertInto(
	ctx context.Context,
	s *Store,
	project string,
	ref EntityRef,
	target, previous *ConfigEvent,
	cw ConfigWrite,
) error {
	restoring := IsDeleteAction(target.Action)

	switch ref.Kind {
	case EntityWorker:
		if restoring {
			w, err := workerFromPayload(target)
			if err != nil {
				return err
			}
			_, err = s.UpsertWorker(ctx, w, cw)
			return err
		}
		if previous == nil {
			return s.DeleteWorker(ctx, project, ref.Key, cw)
		}
		w, err := workerFromPayload(previous)
		if err != nil {
			return err
		}
		_, err = s.UpsertWorker(ctx, w, cw)
		return err

	case EntitySubscription:
		if restoring {
			sub, err := subscriptionFromPayload(target)
			if err != nil {
				return err
			}
			_, err = s.CreateSubscription(ctx, sub, cw)
			return err
		}
		if previous == nil {
			return s.DeleteSubscription(ctx, project, ref.Key, cw)
		}
		sub, err := subscriptionFromPayload(previous)
		if err != nil {
			return err
		}
		_, err = s.UpdateSubscription(ctx, sub, cw)
		return err

	case EntitySchedule:
		if restoring {
			sch, err := scheduleFromPayload(target)
			if err != nil {
				return err
			}
			_, err = s.CreateSchedule(ctx, sch, cw)
			return err
		}
		if previous == nil {
			return s.DeleteSchedule(ctx, project, ref.Key, cw)
		}
		sch, err := scheduleFromPayload(previous)
		if err != nil {
			return err
		}
		_, err = s.UpdateSchedule(ctx, sch, cw)
		return err

	case EntityProjectPrompt:
		// The narrow payload, deliberately (Decision D6): reverting a prompt
		// write means calling SetProjectPrompt forward with the previous
		// prompt, for which {project, system_prompt} is exactly sufficient.
		// Widening it would reintroduce the hazard the comment in
		// project_settings.go exists to prevent.
		if previous == nil {
			return fmt.Errorf("%w: %s is the first project prompt this project ever had, and "+
				"the prompt cannot be blank — write a new one instead",
				ErrRevertRefused, target.Action)
		}
		prompt, ok := previous.PayloadString("system_prompt")
		if !ok {
			return fmt.Errorf("%w: the previous project prompt record (seq %d) carries no system_prompt",
				ErrRevertRefused, previous.Seq)
		}
		_, _, err := s.SetProjectPrompt(ctx, project, prompt, cw)
		return err

	case EntityProjectSettings:
		if previous == nil {
			return fmt.Errorf("%w: %s is the first settings write this project ever had; there is "+
				"no earlier state to put back", ErrRevertRefused, target.Action)
		}
		ps, err := projectSettingsFromPayload(previous)
		if err != nil {
			return err
		}
		ps.Project = project
		_, err = s.PutProjectSettings(ctx, ps, cw)
		return err
	}

	return fmt.Errorf("%w: no revert is defined for %s", ErrRevertRefused, ref.Kind)
}

// The payload decoders. Each round-trips the stored JSONMap back into the row
// type it was written from — the payload IS the full row (§15.2), so this is a
// re-hydration and not a reconstruction.

func workerFromPayload(ev *ConfigEvent) (*Worker, error) {
	var w Worker
	if err := decodePayload(ev, &w); err != nil {
		return nil, err
	}
	return &w, nil
}

func subscriptionFromPayload(ev *ConfigEvent) (*Subscription, error) {
	var sub Subscription
	if err := decodePayload(ev, &sub); err != nil {
		return nil, err
	}
	return &sub, nil
}

func scheduleFromPayload(ev *ConfigEvent) (*Schedule, error) {
	var sch Schedule
	if err := decodePayload(ev, &sch); err != nil {
		return nil, err
	}
	return &sch, nil
}

func projectSettingsFromPayload(ev *ConfigEvent) (*ProjectSettings, error) {
	var ps ProjectSettings
	if err := decodePayload(ev, &ps); err != nil {
		return nil, err
	}
	return &ps, nil
}

// decodePayload re-hydrates a stored payload into the row type it was written
// from. It goes through JSON rather than a map walk so the struct tags decide,
// exactly as they did on the way in — a hand-rolled reader would be a second
// definition of the row shape and would drift.
//
// A payload that will not decode is an ERROR, never a partial row: reverting to
// half a worker would look like a successful revert and leave the project in a
// state nobody chose.
func decodePayload(ev *ConfigEvent, dst any) error {
	raw, err := json.Marshal(ev.Payload)
	if err != nil {
		return fmt.Errorf("%w: payload of %s (seq %d) is not encodable: %v",
			ErrRevertRefused, ev.Action, ev.Seq, err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("%w: payload of %s (seq %d) does not read back as %T: %v",
			ErrRevertRefused, ev.Action, ev.Seq, dst, err)
	}
	return nil
}
