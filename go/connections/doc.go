// Package connections lets a session reach an external service (GitHub, Gmail,
// Google Docs, …) through an MCP tool without the credential ever entering a
// container (design/2026-09-11-project-connections.md).
//
// A connection is defined once, per project, in the operator's project map:
// a name, a description, an upstream MCP URL, and an auth recipe that names
// environment variables rather than holding secrets (Spec, Registry). A
// worker is granted a list of connection names (agentdb.Worker.Connections,
// "*" meaning every connection the project has); the session it runs gets one
// MCP server entry per grant, pointing back at agentd's own `/connect/{name}/`
// route rather than at the upstream directly (Servers). When the session
// calls that tool, agentd's proxy (NewProxy) re-reads the grant, swaps the
// session's bearer token for the real credential, and streams the request to
// the upstream — the container never sees the upstream credential, and the
// database and git never see it either (it lives only in agentd's
// environment, named by the project map).
//
// The trust rule this package enforces (Holds, CanGrant, Covers) is: a worker
// may grant only a connection it holds itself, because reach into the outside
// world — an email drafted, a PR opened — cannot be reverted the way a config
// change can. Removing a grant is always allowed, since that only reduces
// reach. There is no architect-specific code: the rule is one function,
// applied uniformly to whichever worker is asking.
package connections
