FROM alpine
RUN apk update && apk --no-cache add ca-certificates && rm -rf /var/cache/apk/*
ENV HOME /tmp
COPY storage /
COPY helm /
USER 65534
