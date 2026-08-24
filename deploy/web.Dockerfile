# Build context: agent-library  (root) — needs both web/ and examples/web/.
FROM node:20-slim AS build
WORKDIR /src
COPY web ./web
COPY examples/web ./examples/web

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
