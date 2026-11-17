ARG TARGETARCH

# Web UI files are platform-independent, so build them once on the build platform.
FROM --platform=$BUILDPLATFORM oven/bun:1.4.4-alpine@sha256:4cf82f5686b196504fe119db3fcd9185b191f82ece82c40680e597228ec6070a AS web

WORKDIR /src
COPY package.json bun.lock bunfig.toml tsconfig.json components.json ./
RUN bun install --frozen-lockfile --ignore-scripts
COPY scripts scripts
COPY src/web src/web
RUN bun run build

FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS builder

WORKDIR /src
RUN apk add --no-cache git
COPY . .
COPY --from=web /src/src/assets/js src/assets/js
COPY --from=web /src/src/assets/vendor src/assets/vendor

RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Build native Go compiler processes for every target instead of emulating the
# compiler under QEMU, which is particularly slow for older ARM builders.
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
# The Alpine-hosted linker otherwise records a musl interpreter. The 64-bit
# final stages use glibc so upstream can load its matching verified runtime.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    signtools_ldflags="-s -w -buildid="; \
    case "$TARGETARCH" in \
      amd64) signtools_ldflags="$signtools_ldflags -I /lib64/ld-linux-x86-64.so.2" ;; \
      arm64) signtools_ldflags="$signtools_ldflags -I /lib/ld-linux-aarch64.so.1" ;; \
    esac; \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath -ldflags="$signtools_ldflags" -buildvcs=false -o "SignTools"

# The 64-bit App Store engine loads upstream's verified Unicorn shared library.
# Keep its glibc loader and dependencies available even with CGO_ENABLED=0.
FROM debian:bookworm-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc AS runtime-amd64
FROM runtime-amd64 AS runtime-arm64

# Existing 32-bit deployments retain the signing service. The upstream App
# Store engine reports its unsupported architecture explicitly on these hosts.
FROM alpine:3.22@sha256:49340b346798875b5592e5d420241f2f9cfba5f47e62ebb1446a1b34734e898f AS runtime-386
FROM runtime-386 AS runtime-arm

FROM runtime-${TARGETARCH}

WORKDIR /

# for HTTPS calls to builders and app URLs
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
# Go stores large multipart form uploads under /tmp
COPY --from=builder --chmod=1777 /tmp /tmp
COPY --from=builder "/src/SignTools" "/"
COPY LICENSE /usr/share/licenses/SignTools/LICENSE
COPY third_party/ipatool/LICENSE /usr/share/licenses/SignTools/ipatool.LICENSE
COPY third_party/unfairplay/LICENSE /usr/share/licenses/SignTools/unfairplay.LICENSE

# Upstream runtime/framework caches contain public assets, not account sessions.
ENV XDG_CACHE_HOME=/data/cache

ENTRYPOINT ["/SignTools"]
EXPOSE 8080
