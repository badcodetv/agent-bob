package httpapi

import "net/http"

// Whoami serves GET /agent/whoami (onboarding-work-plan §1.1): who the
// caller's credential is and whether it is the operator — the only identity
// PutProjectSettings lets change a project's token budgets and
// job-concurrency cap. It reads only what apiAuthMiddleware already put on
// the request; it needs no store and is available on every deployment,
// including the sqlite fallback that leaves ProjectSettings nil.
func (h *Handlers) Whoami(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	writeJSON(w, whoamiResponse{
		Email:    id.UserEmail,
		Project:  id.Customer,
		Operator: id.Operator,
	})
}

// whoamiResponse is the whole of what GET /agent/whoami answers.
type whoamiResponse struct {
	Email    string `json:"email"`
	Project  string `json:"project"`
	Operator bool   `json:"operator"`
}
