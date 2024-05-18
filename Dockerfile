FROM alpine:3.19 AS downloader

ENV HELM_VERSION=3.15.0
ENV DIFF_SO_FANCY_VERSION=1.4.4

WORKDIR /tmp

RUN ARCH="" && \
    case `uname -m` in \
        x86_64)  ARCH='amd64'; ;; \
        aarch64) ARCH='arm64'; ;; \
        *) echo "unsupported architecture"; exit 1 ;; \
    esac \
    && apk add --no-cache wget git patch \
    && wget --progress=dot:giga -O helm.tar.gz "https://get.helm.sh/helm-v${HELM_VERSION}-linux-${ARCH}.tar.gz" \
    && tar -xf helm.tar.gz "linux-${ARCH}/helm" \
    && mv "linux-${ARCH}/helm" /usr/bin/helm

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

FROM alpine:3.19

RUN apk add --no-cache perl ncurses

COPY --from=downloader /usr/bin/helm /usr/bin/helm
COPY --from=downloader /usr/local/bin/lib /usr/local/bin/lib
COPY --from=downloader /usr/local/bin/diff-so-fancy /usr/local/bin/diff-so-fancy

COPY argo-compare /bin/argo-compare

ENTRYPOINT ["/bin/argo-compare"]
CMD ["--help"]
