# syntax=docker/dockerfile:1.7
ARG DEBIAN_IMAGE=docker.io/library/debian:bookworm-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171
FROM ${DEBIAN_IMAGE}
ARG LOCAL_UID=10002
ARG LOCAL_GID=10002
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl jq util-linux \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid "${LOCAL_GID}" producer \
    && useradd --uid "${LOCAL_UID}" --gid producer --home-dir /home/producer --create-home producer \
    && install -d -o producer -g producer /producer-lock /producer-status
COPY --chmod=0755 producer/block-producer.sh /usr/local/bin/block-producer
COPY --chmod=0755 producer/healthcheck.sh /usr/local/bin/producer-healthcheck
USER producer:producer
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=3 \
  CMD ["/usr/local/bin/producer-healthcheck"]
ENTRYPOINT ["block-producer"]
