# syntax=docker/dockerfile:1.7
ARG GO_IMAGE=docker.io/library/golang:1.25.7-bookworm@sha256:564e366a28ad1d70f460a2b97d1d299a562f08707eb0ecb24b659e5bd6c108e1
ARG RUNTIME_IMAGE=docker.io/library/debian:bookworm-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171
FROM ${GO_IMAGE} AS builder
WORKDIR /src
COPY go.mod ./
COPY . ./
RUN CGO_ENABLED=0 GOOS=linux go test ./... \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/network-status .

FROM ${RUNTIME_IMAGE}
ARG LOCAL_UID=10003
ARG LOCAL_GID=10003
RUN groupadd --gid "${LOCAL_GID}" status \
    && useradd --uid "${LOCAL_UID}" --gid status --home-dir /nonexistent --no-create-home status
COPY --from=builder /out/network-status /usr/local/bin/network-status
USER status:status
EXPOSE 8080
ENTRYPOINT ["network-status"]
