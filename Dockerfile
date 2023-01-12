FROM alpine:3.17

RUN apk add --no-cache git

COPY argo-compare /bin/argo-compare
