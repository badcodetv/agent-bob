package connections

import (
	"strings"

	"github.com/badcodetv/agent-bob/agentdb"
)

// Servers resolves a worker's grants into the MCP server map a session's
// container receives: one entry per expanded grant that names a connection
// the project actually has AND is currently available, pointing back at
// agentd's own proxy rather than at the upstream — the container never learns
// the real URL or credential, only "${SESSION_TOKEN}" (the sandbox already
// resolves that from its environment, unchanged by this design). A trailing
// slash on selfURL is trimmed so the join is never doubled. Grants naming an
// unknown or unavailable connection (Registry.Availability, which for a
// google_account connection asks its AccountSource) are silently skipped here; the caller
// (agentd's wiring) is responsible for logging what it skipped, since only it
// has a logger threaded through.
func Servers(r *Registry, project string, grants []string, selfURL string) agentdb.MCPServers {
	selfURL = strings.TrimSuffix(selfURL, "/")
	out := agentdb.MCPServers{}
	for _, name := range ExpandGrants(r, project, grants) {
		if ok, _ := r.Availability(project, name); !ok {
			continue
		}
		out[name] = agentdb.MCPServerConfig{
			URL:     selfURL + "/connect/" + name + "/",
			Headers: map[string]string{"Authorization": "${SESSION_TOKEN}"},
		}
	}
	return out
}
