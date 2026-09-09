// Left sidebar: project switcher + "New session" + the library's
// ChatHistoryDrawer (session rows with the filter-by-user select).
import { useEffect, useMemo, useState } from "react";
import {
  Alert,
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  Select,
  TextField,
  Typography,
} from "@mui/material";
import AddIcon from "@mui/icons-material/Add";
import { ChatHistoryDrawer, useAgentChat, useAgentSessions } from "@agentkit/chat-ui";
import { AuthState } from "./auth";

const NEW_PROJECT_SENTINEL = "__new-project__";

export default function Sidebar({
  auth,
  project,
  onSwitchProject,
  onCreateProject,
  onSignOut,
}: {
  auth: AuthState;
  project: string;
  onSwitchProject: (projectID: string) => void;
  onCreateProject: (projectID: string, goal: string) => Promise<void>;
  onSignOut: () => void;
}) {
  const { sessions, refresh, select } = useAgentSessions();
  const { createSession, session, isCreating } = useAgentChat();
  const [userFilter, setUserFilter] = useState<string>("me");
  const [searchQuery, setSearchQuery] = useState("");
  // A new project needs TWO things — an id and a goal — and window.prompt can
  // only ask for one. It also cannot mark a field required, cannot show the
  // server's refusal, and cannot say what the goal is FOR.
  const [newProjectOpen, setNewProjectOpen] = useState(false);
  const [newProjectId, setNewProjectId] = useState("");
  const [newProjectGoal, setNewProjectGoal] = useState("");
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);

  const submitNewProject = async (e: React.FormEvent) => {
    e.preventDefault();
    setCreating(true);
    setCreateError(null);
    try {
      await onCreateProject(newProjectId.trim(), newProjectGoal.trim());
      setNewProjectOpen(false);
      setNewProjectId("");
      setNewProjectGoal("");
    } catch (err) {
      setCreateError(err instanceof Error ? err.message : String(err));
    } finally {
      setCreating(false);
    }
  };

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
              setCreateError(null);
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
      <Dialog open={newProjectOpen} onClose={() => (creating ? undefined : setNewProjectOpen(false))} fullWidth maxWidth="sm">
        <DialogTitle>New project</DialogTitle>
        <Box component="form" onSubmit={submitNewProject}>
          <DialogContent sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
            <DialogContentText sx={{ fontSize: 14 }}>
              Creating a project starts an interview. It asks what the project is for and how you
              would know it is working, then writes that down for you to approve.
            </DialogContentText>
            <TextField
              size="small"
              fullWidth
              autoFocus
              required
              label="Project id"
              placeholder="apples-oranges"
              helperText="Kebab-case. This is the namespace everything in the project lives under."
              value={newProjectId}
              onChange={(e) => setNewProjectId(e.target.value)}
              slotProps={{ htmlInput: { "data-testid": "new-project-input" } }}
            />
            <TextField
              size="small"
              fullWidth
              multiline
              minRows={2}
              required
              label="What is this project for?"
              helperText="Your goal — the interview starts from this."
              placeholder="e.g. send a weekly newsletter that brings people back into the shop"
              value={newProjectGoal}
              onChange={(e) => setNewProjectGoal(e.target.value)}
              slotProps={{ htmlInput: { "data-testid": "new-project-goal" } }}
            />
            {createError !== null && <Alert severity="error">{createError}</Alert>}
          </DialogContent>
          <DialogActions>
            <Button onClick={() => setNewProjectOpen(false)} disabled={creating} sx={{ textTransform: "none" }}>
              Cancel
            </Button>
            <Button
              type="submit"
              variant="contained"
              disabled={creating || !newProjectId.trim() || !newProjectGoal.trim()}
              data-testid="new-project-create"
              sx={{ textTransform: "none" }}
            >
              {creating ? "Creating…" : "Create project"}
            </Button>
          </DialogActions>
        </Box>
      </Dialog>

      <ChatHistoryDrawer
        open
        onClose={() => {}}
        sessions={sessions}
        activeSessionId={session?.id}
        onSelectSession={select}
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
