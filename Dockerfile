# syntax=docker/dockerfile:1.7
ARG DEBIAN_IMAGE=docker.io/library/debian:bookworm-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171

FROM ${DEBIAN_IMAGE} AS fetcher
ARG ELEMENTS_VERSION=23.3.4
ARG ELEMENTS_SHA256=a758151ace3f21008ab162067ffce9e0e526a1b5d55995e2c30d9cd7ccda41a0
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /tmp/elements
RUN curl -fL --retry 5 --retry-delay 2 \
      -o elements.tar.gz \
      "https://github.com/ElementsProject/elements/releases/download/elements-${ELEMENTS_VERSION}/elements-${ELEMENTS_VERSION}-x86_64-linux-gnu.tar.gz" \
    && echo "${ELEMENTS_SHA256}  elements.tar.gz" | sha256sum -c - \
    && tar -xzf elements.tar.gz --strip-components=2 \
      "elements-${ELEMENTS_VERSION}/bin/elementsd" \
      "elements-${ELEMENTS_VERSION}/bin/elements-cli"

FROM ${DEBIAN_IMAGE}
ARG ELEMENTS_VERSION=23.3.4
ARG LOCAL_UID=10001
ARG LOCAL_GID=10001
LABEL org.opencontainers.image.title="elements-localnet node" \
      org.opencontainers.image.version="${ELEMENTS_VERSION}" \
      org.opencontainers.image.source="https://github.com/ElementsProject/elements"
RUN groupadd --gid "${LOCAL_GID}" elements \
    && useradd --uid "${LOCAL_UID}" --gid elements --home-dir /home/elements --create-home elements \
    && install -d -o elements -g elements /data /config
COPY --from=fetcher /tmp/elements/elementsd /usr/local/bin/elementsd
COPY --from=fetcher /tmp/elements/elements-cli /usr/local/bin/elements-cli
COPY --chmod=0755 scripts/node-healthcheck.sh /usr/local/bin/node-healthcheck
USER elements:elements
VOLUME ["/data"]
EXPOSE 7040 7042
ENTRYPOINT ["elementsd"]
CMD ["-datadir=/data", "-conf=/config/elements.conf", "-printtoconsole=1"]
