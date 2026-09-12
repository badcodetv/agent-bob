// Left sidebar: project switcher + "New session" + the library's
// ChatHistoryDrawer (session rows with the filter-by-user select).
//
// The "+ New project…" dialog's form is CreateProjectForm (web G6), shared
// with ProjectPicker.tsx — this file's copy (the interview sentence, the
// "Project id" label) is the one that won.
import { useEffect, useMemo, useState } from "react";
import {
  Box,
  Button,
  Dialog,
  DialogContent,
  DialogTitle,
  Select,
  Typography,
} from "@mui/material";
import AddIcon from "@mui/icons-material/Add";
import { ChatHistoryDrawer, CreateProjectForm, useAgentChat, useAgentSessions } from "@agentkit/chat-ui";
import { AuthState } from "./auth";

const NEW_PROJECT_SENTINEL = "__new-project__";

export default function Sidebar({
  auth,
  project,
  onSwitchProject,
  onCreateProject,
  onSignOut,
  onboardSessionId = null,
  inInterview = false,
  onOpenOnboarding,
}: {
  auth: AuthState;
  project: string;
  onSwitchProject: (projectID: string) => void;
  onCreateProject: (projectID: string, goal: string) => Promise<void>;
  onSignOut: () => void;
  /** The `onboard` session's id, while known (design §3 G1 / A2). */
  onboardSessionId?: string | null;
  /** True while this project's interview is unresolved. */
  inInterview?: boolean;
  /** Opens the onboarding view — used instead of resuming plain chat when the
   *  `onboard` session is clicked while `inInterview` is true. */
  onOpenOnboarding?: () => void;
}) {
  const { sessions, refresh, select } = useAgentSessions();

  // While a project is in interview, its `onboard` session is not a chat like
  // any other: clicking it should return the person to the onboarding view
  // (charter and all), not to a bare transcript (design §3 G1).
  const selectSession = (id: string) => {
    if (inInterview && onboardSessionId !== null && id === onboardSessionId && onOpenOnboarding) {
      onOpenOnboarding();
      return;
    }
    select(id);
  };
  const { createSession, session, isCreating } = useAgentChat();
  const [userFilter, setUserFilter] = useState<string>("me");
  const [searchQuery, setSearchQuery] = useState("");
  // A new project needs TWO things — an id and a goal — and window.prompt can
  // only ask for one. It also cannot mark a field required, cannot show the
  // server's refusal, and cannot say what the goal is FOR.
  const [newProjectOpen, setNewProjectOpen] = useState(false);

  useEffect(() => {
    void refresh({ userEmail: userFilter === "me" ? undefined : userFilter });
  }, [refresh, userFilter]);

  // Distinct creators among the loaded sessions feed the per-user filter options.
  const users = useMemo(
    () => Array.from(new Set(sessions.map((s) => s.user_email).filter(Boolean))),
    [sessions],
  );

  const newSession = async () => {
    const id = await createSession({ customer: project, workflow_id: "agent" });
    if (id) void refresh({ userEmail: userFilter === "me" ? undefined : userFilter });
  };

  return (
    <Box data-testid="session-sidebar" sx={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <Box sx={{ p: 1.5, borderBottom: "1px solid rgba(0,0,0,0.06)", display: "flex", flexDirection: "column", gap: 1 }}>
        <Select
          native
          size="small"
          value={project}
          onChange={(e) => {
            if (e.target.value === NEW_PROJECT_SENTINEL) {
              setNewProjectOpen(true);
              return;
            }
            onSwitchProject(e.target.value);
          }}
          inputProps={{ "data-testid": "project-switcher" }}
          sx={{ fontSize: 13 }}
        >
          {auth.projects.map((p) => (
            <option key={p.id} value={p.id}>{p.id}</option>
          ))}
          {auth.wildcard && <option value={NEW_PROJECT_SENTINEL}>＋ New project…</option>}
        </Select>
        <Button
          variant="contained"
          size="small"
          startIcon={<AddIcon />}
          onClick={newSession}
          disabled={isCreating}
          data-testid="new-session"
          sx={{ textTransform: "none" }}
        >
          New session
        </Button>
        <Box sx={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
          <Typography sx={{ fontSize: 11, color: "#6b7280", overflow: "hidden", textOverflow: "ellipsis" }}>
            {auth.email}
          </Typography>
          <Button size="small" onClick={onSignOut} sx={{ textTransform: "none", fontSize: 11, minWidth: 0 }}>
            Sign out
          </Button>
        </Box>
      </Box>
      <Dialog open={newProjectOpen} onClose={() => setNewProjectOpen(false)} fullWidth maxWidth="sm">
        <DialogTitle>New project</DialogTitle>
        <DialogContent>
          <CreateProjectForm
            onCreate={onCreateProject}
            onCreated={() => setNewProjectOpen(false)}
            onCancel={() => setNewProjectOpen(false)}
            autoFocus
          />
        </DialogContent>
      </Dialog>

      <ChatHistoryDrawer
        open
        onClose={() => {}}
        sessions={sessions}
        activeSessionId={session?.id}
        onSelectSession={selectSession}
        users={users}
        selectedUserEmail={userFilter}
        onUserFilterChange={setUserFilter}
        currentUserEmail={auth.email}
        searchQuery={searchQuery}
        onSearchChange={setSearchQuery}
      />
    </Box>
  );
}
