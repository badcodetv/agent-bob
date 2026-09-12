// Project picker: shown after login when the account maps to more than one
// project (or has a wildcard grant). A project is a pure namespace over
// sessions (the customer claim); wildcard users can mint a brand-new one here.
//
// The "new project" form itself is CreateProjectForm (web G6): this picker
// and Sidebar.tsx's "+ New project…" dialog used to carry two independent
// copies, and only the Sidebar's said what creating a project actually does.
import { Box, Button, Paper, Typography } from "@mui/material";
import { CreateProjectForm } from "@agentkit/chat-ui";
import { AuthState } from "./auth";

export default function ProjectPicker({
  auth,
  onSelect,
  onCreate,
  onSignOut,
}: {
  auth: AuthState;
  onSelect: (projectID: string) => void;
  onCreate: (projectID: string, goal: string) => Promise<void>;
  onSignOut: () => void;
}) {
  return (
    <Box sx={{ display: "flex", alignItems: "center", justifyContent: "center", height: "100vh", bgcolor: "#f8fafc" }}>
      <Paper sx={{ p: 4, width: 360, display: "flex", flexDirection: "column", gap: 2 }} data-testid="project-picker">
        <Typography variant="h6" sx={{ fontWeight: 600 }}>Choose a project</Typography>
        <Typography variant="body2" color="text.secondary">{auth.email}</Typography>
        <Box sx={{ display: "flex", flexDirection: "column", gap: 1 }}>
          {auth.projects.map((p) => (
            <Button
              key={p.id}
              variant="outlined"
              onClick={() => onSelect(p.id)}
              data-testid={`project-option-${p.id}`}
              sx={{ justifyContent: "flex-start", textTransform: "none" }}
            >
              {p.id}
            </Button>
          ))}
          {auth.projects.length === 0 && !auth.wildcard && (
            <Typography variant="body2" color="text.secondary">No projects for this account.</Typography>
          )}
        </Box>
        {auth.wildcard && <CreateProjectForm onCreate={onCreate} />}
        <Button size="small" onClick={onSignOut} sx={{ alignSelf: "flex-start", textTransform: "none" }}>
          Sign out
        </Button>
      </Paper>
    </Box>
  );
}
