FROM alpine:3.20 AS downloader

ARG TARGETARCH

ENV HELM_VERSION=3.15.2
ENV DIFF_SO_FANCY_VERSION=1.4.4

WORKDIR /tmp

RUN apk add --no-cache wget git patch \
    && wget --progress=dot:giga -O helm.tar.gz "https://get.helm.sh/helm-v${HELM_VERSION}-linux-${TARGETARCH}.tar.gz" \
    && tar -xf helm.tar.gz "linux-${TARGETARCH}/helm" \
    && mv "linux-${TARGETARCH}/helm" /usr/bin/helm

RUN git clone -b v${DIFF_SO_FANCY_VERSION} https://github.com/so-fancy/diff-so-fancy /diff-so-fancy \
 && mv /diff-so-fancy/diff-so-fancy /usr/local/bin/diff-so-fancy \
 && mv /diff-so-fancy/lib /usr/local/bin \
 && chmod +x /usr/local/bin/diff-so-fancy

# We need to apply a patch to the diff-so-fancy to not treat files in different directories as
# renamed files. This is because we need to render manifests
# from source and destination branches to different directories.
COPY patch/diff-so-fancy.patch /tmp/diff-so-fancy.patch

WORKDIR /usr/local/bin

RUN patch < /tmp/diff-so-fancy.patch

FROM alpine:3.20

RUN apk add --no-cache perl ncurses \
 && adduser --disabled-password --gecos '' app

COPY --from=downloader /usr/bin/helm /usr/bin/helm
COPY --from=downloader /usr/local/bin/lib /usr/local/bin/lib
COPY --from=downloader /usr/local/bin/diff-so-fancy /usr/local/bin/diff-so-fancy

COPY argo-compare /bin/argo-compare

USER app

ENTRYPOINT ["/bin/argo-compare"]
CMD ["--help"]
