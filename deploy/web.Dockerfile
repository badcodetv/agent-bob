# Build context: agent-library  (root) — needs web/, examples/web/ and docs/.
FROM node:20-slim AS build
WORKDIR /src
COPY web ./web
COPY examples/web ./examples/web
# The guide (ticket C1): examples/web's build reads docs/guide/*.md at build
# time (scripts/build-guide.mjs) and bakes each page into the bundle as static
# HTML — no server, no CMS. The WHOLE docs/ tree is copied, not just
# docs/guide/, because docs/guide/ does not exist on every branch (it is
# written by a separate workstream) and a COPY of a path that is absent in the
# build context fails the build outright; docs/ itself always exists. Missing
# or empty docs/guide/ is not an error to the script — it emits an empty guide
# and the shell says so (GuidePage.tsx) — so this stays correct either way.
COPY docs ./docs

# web/ is BUILT first, because examples/web now consumes its emitted `dist`
# rather than aliasing its TypeScript source (O12). Without this step the Vite
# alias in examples/web/vite.config.ts resolves to a path that does not exist
# and the app build fails with "Failed to resolve import @agentkit/chat-ui".
#
# npm, not yarn: web/ tracks package-lock.json only.
WORKDIR /src/web
RUN npm ci && npm run build

WORKDIR /src/examples/web
RUN corepack enable && yarn install --frozen-lockfile && yarn build

FROM nginx:1.27-alpine
COPY deploy/web.nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/examples/web/dist /usr/share/nginx/html
EXPOSE 8080
