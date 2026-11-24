FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -o /out/gateway ./cmd/gateway \
 && CGO_ENABLED=0 go build -o /out/workspace ./cmd/workspace \
 && CGO_ENABLED=0 go build -o /out/mock-server ./cmd/mock-server \
 && CGO_ENABLED=0 go build -o /out/data-server ./cmd/data-server \
 && CGO_ENABLED=0 go build -o /out/github-server ./cmd/github-server \
 && CGO_ENABLED=0 go build -o /out/sampledb-server ./cmd/sampledb-server \
 && CGO_ENABLED=0 go build -o /out/files-server ./cmd/files-server

FROM alpine:3.22
RUN adduser -D -u 65532 app
WORKDIR /app
COPY --from=build /out/ /usr/local/bin/
COPY fixtures /app/fixtures
COPY web/dist /app/web/dist
USER app
EXPOSE 8090
ENTRYPOINT ["workspace"]
CMD ["-listen", "0.0.0.0:8090", "-listen-mode", "container", "-web-dir", "/app/web/dist"]
