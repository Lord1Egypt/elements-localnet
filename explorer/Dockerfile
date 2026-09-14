# syntax=docker/dockerfile:1.7
ARG GO_IMAGE=docker.io/library/golang:1.25.7-bookworm@sha256:564e366a28ad1d70f460a2b97d1d299a562f08707eb0ecb24b659e5bd6c108e1
ARG RUNTIME_IMAGE=docker.io/library/debian:bookworm-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171

FROM ${GO_IMAGE} AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
# The explorer index is pure Go (modernc.org/sqlite), so the final binary is
# statically linked and needs no C runtime at all.
RUN CGO_ENABLED=0 GOOS=linux go vet ./... \
    && CGO_ENABLED=0 GOOS=linux go test ./... \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/explorer .

# rootfs only supplies the passwd entries and the database directory whose
# ownership the named volume inherits.
FROM ${RUNTIME_IMAGE} AS rootfs
ARG LOCAL_UID=10004
ARG LOCAL_GID=10004
RUN groupadd --gid "${LOCAL_GID}" explorer \
    && useradd --uid "${LOCAL_UID}" --gid explorer --home-dir /nonexistent --no-create-home explorer \
    && install -d -o "${LOCAL_UID}" -g "${LOCAL_GID}" -m 0750 /var/lib/elements-explorer

FROM scratch
ARG LOCAL_UID=10004
ARG LOCAL_GID=10004
COPY --from=rootfs /etc/passwd /etc/group /etc/
COPY --from=rootfs --chown=${LOCAL_UID}:${LOCAL_GID} /var/lib/elements-explorer /var/lib/elements-explorer
COPY --from=builder /out/explorer /usr/local/bin/explorer
USER ${LOCAL_UID}:${LOCAL_GID}
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/explorer"]
