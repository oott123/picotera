FROM node:25.8.1-trixie AS frontend-builder
COPY . /app
WORKDIR /app
RUN npm install -g pnpm@10 && \
    pnpm install --frozen-lockfile && \
    pnpm --dir dashboard build

FROM golang:1.27.2-trixie AS backend-builder
COPY . /app
COPY --from=frontend-builder /app/dashboard/dist /app/dashboard/dist
WORKDIR /app
RUN find pkg/server/static/dist -mindepth 1 ! -name index.html ! -name .gitignore -delete && \
    cp -r dashboard/dist/. pkg/server/static/dist/ && \
    go build -o picotera ./cmd/picotera && \
    go build -o picotera-llmbridge-plugin ./cmd/picotera-llmbridge-plugin

FROM gcr.io/distroless/base-debian13 AS runtime
COPY LICENSE /app/LICENSE
COPY --from=backend-builder /app/picotera /app/picotera
COPY --from=backend-builder /app/picotera-llmbridge-plugin /app/picotera-llmbridge-plugin
COPY THIRD_PARTY_NOTICES.md /app/THIRD_PARTY_NOTICES.md
ENV PICOTERA_LLMBRIDGE_PLUGIN_PATH=/app/picotera-llmbridge-plugin
WORKDIR /app
ENTRYPOINT ["/app/picotera"]
