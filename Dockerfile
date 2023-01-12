FROM alpine/helm:3.10.2

RUN apk add --no-cache bash git

COPY argo-compare /bin/argo-compare

ENTRYPOINT ["/bin/argo-compare"]
CMD ["--help"]
