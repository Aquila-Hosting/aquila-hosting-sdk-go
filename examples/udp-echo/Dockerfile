# syntax=docker/dockerfile:1

# Built with packages/sdk-go as the context, so the example compiles against
# the SDK in this tree.

# IMAGE_VERSION tags the image. The docs quickstart pins it, so bump both.
ARG IMAGE_VERSION=1.0.0
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod ./
COPY *.go openapi.yaml ./
COPY examples/udp-echo/ ./examples/udp-echo/
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /udp-echo ./examples/udp-echo

FROM gcr.io/distroless/static:nonroot
COPY --from=build /udp-echo /udp-echo
USER 65532:65532
EXPOSE 7777/udp
ENTRYPOINT ["/udp-echo"]
