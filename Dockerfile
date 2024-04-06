FROM alpine/helm:3.14.3

ENV DIFF_SO_FANCY_VERSION=1.4.3

RUN apk add --no-cache bash git perl ncurses patch

RUN git clone -b v${DIFF_SO_FANCY_VERSION} https://github.com/so-fancy/diff-so-fancy /diff-so-fancy \
 && mv /diff-so-fancy/diff-so-fancy /usr/local/bin/diff-so-fancy \
 && mv /diff-so-fancy/lib /usr/local/bin \
 && rm -rf /diff-so-fancy \
 && chmod +x /usr/local/bin/diff-so-fancy

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
