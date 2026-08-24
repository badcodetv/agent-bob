import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";

export default defineConfig({
  plugins: [react()],
  // Two entry points, not one SPA with a route: `index.html` is the console
  // shell (login → project picker → workspace) and `embed.html` is the
  // tokened single-session iframe page, which must carry none of that shell.
  // nginx serves them apart (deploy/web.nginx.conf).
  build: {
    rollupOptions: {
      input: {
        main: path.resolve(__dirname, "index.html"),
        embed: path.resolve(__dirname, "embed.html"),
      },
    },
  },
  resolve: {
    // The alias points at web/'s BUILT dist, not at web/src. That is what makes
    // this app a real consumer of the published package (O12): if
    // tsconfig.build.json stops emitting, or the `exports` map stops matching
    // what is emitted, this build breaks — which is the point. web/ must
    // therefore be built before this app is built or typechecked; both
    // deploy/web.Dockerfile and the examples-web CI job do it.
    //
    // Three entries, one per `exports` subpath. Vite alias keys match exactly,
    // so a bare "@agentkit/chat-ui" entry alone would not catch "/pure".
    alias: {
      "@agentkit/chat-ui/pure": path.resolve(__dirname, "../../web/dist/pure.js"),
      "@agentkit/chat-ui/components": path.resolve(__dirname, "../../web/dist/components/index.js"),
      "@agentkit/chat-ui": path.resolve(__dirname, "../../web/dist/index.js"),
    },
    // Six, not the ten this list used to carry. The four that left —
    // react-markdown, remark-gfm, prism-react-renderer, @untitledui/file-icons —
    // were here only because chat-ui's source imported them while they sat in
    // ITS devDependencies; O12 made them its runtime `dependencies`, so they now
    // resolve out of web/node_modules and this app neither declares nor
    // deduplicates them.
    //
    // These six stay, and they are still load-bearing. Measured with the list
    // emptied and `vite build --sourcemap`: @mui/material and @emotion/react
    // each resolve from BOTH examples/web/node_modules and web/node_modules,
    // because the aliased dist/ files sit under web/ and resolve their bare
    // imports from there. Two emotion caches produce styles that never apply and
    // two @mui/material copies give the chat components a different
    // ThemeProvider from the shell's. react/react-dom happened to collapse to
    // one copy in that run; they stay listed because the failure mode — "invalid
    // hook call" — is severe and the guarantee should not be incidental.
    // @mui/icons-material is here because this app imports icons directly
    // (src/Sidebar.tsx) as well as through chat-ui.
    dedupe: [
      "react",
      "react-dom",
      "@mui/material",
      "@mui/icons-material",
      "@emotion/react",
      "@emotion/styled",
    ],
  },
});
