FROM alpine/helm:3.10.2

ENV DIFF_SO_FANCY_VERSION=1.4.3

RUN apk add --no-cache bash git perl ncurses

RUN git clone -b v${DIFF_SO_FANCY_VERSION} https://github.com/so-fancy/diff-so-fancy /diff-so-fancy \
 && mv /diff-so-fancy/diff-so-fancy /usr/local/bin/diff-so-fancy \
 && mv /diff-so-fancy/lib /usr/local/bin \
 && rm -rf /diff-so-fancy \
 && chmod +x /usr/local/bin/diff-so-fancy

COPY argo-compare /bin/argo-compare

ENTRYPOINT ["/bin/argo-compare"]
CMD ["--help"]
