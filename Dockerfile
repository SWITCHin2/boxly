# Boxly — single image, single pod.
# The admin UI + public /pools page are embedded into the Go binary via
# //go:embed, so the "frontend" and "backend" are one process. No second
# container or static-file server needed.

# ---- build ----
FROM golang:1.26 AS build
WORKDIR /src

# Cache deps first.
COPY go.mod go.sum ./
RUN go mod download

# Build the control plane (boxlyd) and the CLI (boxly). CGO off → fully static,
# so it runs on a distroless/scratch base. The embedded HTML/logo come along
# automatically because they live under internal/server/admin.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/boxlyd ./cmd/boxlyd \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/boxly  ./cmd/boxly

# ---- runtime ----
# distroless static + nonroot: tiny, no shell, runs as uid 65532, ships CA certs.
FROM gcr.io/distroless/static-nonroot:latest
COPY --from=build /out/boxlyd /usr/local/bin/boxlyd
COPY --from=build /out/boxly  /usr/local/bin/boxly

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/boxlyd"]
