// @vitest-environment jsdom
// C5: the storage half of `firsts.ts` — narrates a first once, keeps it
// showing for the rest of that mount, persists the seen-set per project so a
// later mount does not repeat it, and never resets it.

import React from 'react'
import { render, screen } from '@testing-library/react'
import { describe, it, expect, beforeEach } from 'vitest'
import useFirsts, { firstsSeenKey, readFirstsSeen, writeFirstsSeen } from './useFirsts.js'
import type { DeskFirstRecord } from './desk.js'

function Probe({ projectId, records }: { projectId: string; records: DeskFirstRecord[] }) {
  const { toNarrate } = useFirsts({ projectId, records })
  return <span data-testid="narrated">{toNarrate.map((f) => f.kind).join(',')}</span>
}

beforeEach(() => {
  window.localStorage.clear()
})

describe('storage', () => {
  it('keys the seen-set per project, beside the nav reveal keys', () => {
    expect(firstsSeenKey('acme')).toBe('agentkit.firsts.acme')
  })

  it('reads back only what it wrote, and drops rubbish', () => {
    writeFirstsSeen('acme', ['first-worker', 'first-ask'])
    expect(readFirstsSeen('acme')).toEqual(['first-worker', 'first-ask'])

    window.localStorage.setItem('agentkit.firsts.junk', 'not json')
    expect(readFirstsSeen('junk')).toEqual([])

    window.localStorage.setItem('agentkit.firsts.mixed', JSON.stringify(['first-worker', 'not-a-kind', 7]))
    expect(readFirstsSeen('mixed')).toEqual(['first-worker'])
  })
})

describe('useFirsts', () => {
  it('narrates a first record, and keeps showing it for the rest of this mount', () => {
    const records: DeskFirstRecord[] = [{ kind: 'first-worker', id: 'w1', createdAtMs: 1000 }]
    const { rerender } = render(<Probe projectId="acme" records={records} />)
    expect(screen.getByTestId('narrated').textContent).toBe('first-worker')

    // It is ALSO durably marked seen (a later mount will not narrate it
    // again — the next test covers that) — but within THIS mount, re-passing
    // the very same records must not make the sentence vanish the instant
    // the seen-set catches up to it. That collapse is the trap: recomputing
    // "is this seen yet" from storage on every render would hide the
    // sentence one render after showing it, before a person had read it.
    rerender(<Probe projectId="acme" records={records} />)
    expect(screen.getByTestId('narrated').textContent).toBe('first-worker')
    expect(readFirstsSeen('acme')).toEqual(['first-worker'])
  })

  it('does not narrate a kind a previous MOUNT already marked seen', () => {
    const records: DeskFirstRecord[] = [{ kind: 'first-worker', id: 'w1', createdAtMs: 1000 }]
    render(<Probe projectId="acme" records={records} />)
    expect(readFirstsSeen('acme')).toEqual(['first-worker'])

    // A fresh mount for the same project (e.g. navigating back to the Desk)
    // reads the seen-set back and does not narrate again.
    render(<Probe projectId="acme" records={records} />)
    expect(screen.getAllByTestId('narrated').at(-1)?.textContent).toBe('')
  })

  it('deleting the worker does not reset the seen-set', () => {
    const withWorker: DeskFirstRecord[] = [{ kind: 'first-worker', id: 'w1', createdAtMs: 1000 }]
    render(<Probe projectId="acme" records={withWorker} />)
    expect(readFirstsSeen('acme')).toEqual(['first-worker'])

    // The worker is deleted: the next fold has no worker_create record at all
    // for this project (desk.ts simply never tags one). A FRESH mount (a new
    // visit) must not narrate it again.
    render(<Probe projectId="acme" records={[]} />)
    expect(screen.getAllByTestId('narrated').at(-1)?.textContent).toBe('')

    // Nor does hiring a replacement worker resurrect it — "first" was spent,
    // and the seen-set still reads back exactly as it was.
    const rehired: DeskFirstRecord[] = [{ kind: 'first-worker', id: 'w2', createdAtMs: 9000 }]
    render(<Probe projectId="acme" records={rehired} />)
    expect(screen.getAllByTestId('narrated').at(-1)?.textContent).toBe('')
    expect(readFirstsSeen('acme')).toEqual(['first-worker'])
  })

  it('keeps each project’s seen-set separate', () => {
    writeFirstsSeen('acme', ['first-worker'])
    const records: DeskFirstRecord[] = [{ kind: 'first-worker', id: 'w1', createdAtMs: 1000 }]
    render(<Probe projectId="beta" records={records} />)
    // beta has never seen it, even though acme has.
    expect(screen.getByTestId('narrated').textContent).toBe('first-worker')
    expect(readFirstsSeen('beta')).toEqual(['first-worker'])
    expect(readFirstsSeen('acme')).toEqual(['first-worker'])
  })

  it('narrates independently per kind — one already-seen kind does not block a new one', () => {
    writeFirstsSeen('acme', ['first-worker'])
    const records: DeskFirstRecord[] = [
      { kind: 'first-worker', id: 'w1', createdAtMs: 1000 },
      { kind: 'first-memory', id: 'm1', createdAtMs: 2000 },
    ]
    render(<Probe projectId="acme" records={records} />)
    expect(screen.getByTestId('narrated').textContent).toBe('first-memory')
  })
})
