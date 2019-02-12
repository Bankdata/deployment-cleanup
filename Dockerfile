FROM alpine
RUN apk --no-cache add ca-certificates
ADD storage /
ADD helm /
