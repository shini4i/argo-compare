FROM alpine:3.19 as downloader

ENV HELM_VERSION=3.15.0

WORKDIR /tmp

RUN ARCH="" && \
    case `uname -m` in \
        x86_64)  ARCH='amd64'; ;; \
        aarch64) ARCH='arm64'; ;; \
        *) echo "unsupported architecture"; exit 1 ;; \
    esac \
    && apk add --no-cache wget \
    && wget --progress=dot:giga -O helm.tar.gz "https://get.helm.sh/helm-v${HELM_VERSION}-linux-${ARCH}.tar.gz" \
    && tar -xf helm.tar.gz "linux-${ARCH}/helm" \
    && mv "linux-${ARCH}/helm" /usr/bin/helm

FROM alpine:3.19

ENV DIFF_SO_FANCY_VERSION=1.4.3

RUN apk add --no-cache bash git perl ncurses patch

RUN git clone -b v${DIFF_SO_FANCY_VERSION} https://github.com/so-fancy/diff-so-fancy /diff-so-fancy \
 && mv /diff-so-fancy/diff-so-fancy /usr/local/bin/diff-so-fancy \
 && mv /diff-so-fancy/lib /usr/local/bin \
 && rm -rf /diff-so-fancy \
 && chmod +x /usr/local/bin/diff-so-fancy

COPY --from=downloader /usr/bin/helm /usr/bin/helm

COPY argo-compare /bin/argo-compare

# We need to apply a patch to the diff-so-fancy to not treat files in different directories as
# renamed files. This is because we need to render manifests
# from source and destination branches to different directories.
COPY patch/diff-so-fancy.patch /tmp/diff-so-fancy.patch

WORKDIR /usr/local/bin

RUN patch < /tmp/diff-so-fancy.patch \
 && rm /tmp/diff-so-fancy.patch

WORKDIR /

ENTRYPOINT ["/bin/argo-compare"]
CMD ["--help"]
