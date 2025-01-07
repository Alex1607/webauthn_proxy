FROM golang:1.23.4-alpine3.21 AS builder

WORKDIR /opt/src
COPY . /opt/src
RUN go build -o /opt/webauthn_proxy .


FROM alpine:3.21

WORKDIR /opt
COPY config /opt/config
COPY static /opt/static

COPY --from=builder /opt/webauthn_proxy /opt/webauthn_proxy
RUN chown -R root:nobody /opt

EXPOSE 8080
USER nobody
ENTRYPOINT ["/opt/webauthn_proxy"]
