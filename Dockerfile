FROM alpine:3.23.4 AS downloader

ARG TARGETARCH

ENV HELM_VERSION=3.21.0
ENV DIFF_SO_FANCY_VERSION=1.4.10
ENV KUBECONFORM_VERSION=0.7.0

WORKDIR /tmp

RUN apk add --no-cache wget git patch \
    && wget --progress=dot:giga -O helm.tar.gz "https://get.helm.sh/helm-v${HELM_VERSION}-linux-${TARGETARCH}.tar.gz" \
    && wget -qO helm.tar.gz.sha256 "https://get.helm.sh/helm-v${HELM_VERSION}-linux-${TARGETARCH}.tar.gz.sha256sum" \
    && echo "$(awk '{print $1}' helm.tar.gz.sha256)  helm.tar.gz" | sha256sum -c - \
    && tar -xf helm.tar.gz "linux-${TARGETARCH}/helm" \
    && mv "linux-${TARGETARCH}/helm" /usr/bin/helm \
    && rm -f helm.tar.gz helm.tar.gz.sha256

RUN wget --progress=dot:giga -O kubeconform.tar.gz "https://github.com/yannh/kubeconform/releases/download/v${KUBECONFORM_VERSION}/kubeconform-linux-${TARGETARCH}.tar.gz" \
    && wget -qO kubeconform-checksums "https://github.com/yannh/kubeconform/releases/download/v${KUBECONFORM_VERSION}/CHECKSUMS" \
    && grep "kubeconform-linux-${TARGETARCH}.tar.gz$" kubeconform-checksums | sha256sum -c - \
    && tar -xf kubeconform.tar.gz kubeconform \
    && mv kubeconform /usr/bin/kubeconform \
    && rm -f kubeconform.tar.gz kubeconform-checksums

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

FROM alpine:3.23.4

ARG TARGETARCH

RUN apk add --no-cache perl ncurses \
 && adduser --disabled-password --gecos '' app

COPY --from=downloader /usr/bin/helm /usr/bin/helm
COPY --from=downloader /usr/bin/kubeconform /usr/bin/kubeconform
COPY --from=downloader /usr/local/bin/lib /usr/local/bin/lib
COPY --from=downloader /usr/local/bin/diff-so-fancy /usr/local/bin/diff-so-fancy

COPY linux/${TARGETARCH}/argo-compare /bin/argo-compare

USER app

ENTRYPOINT ["/bin/argo-compare"]
CMD ["--help"]
