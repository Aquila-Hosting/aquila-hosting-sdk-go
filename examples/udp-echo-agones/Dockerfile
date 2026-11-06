# syntax=docker/dockerfile:1

# Built with this directory as the context. It has no Aquila code in it.
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /udp-echo-agones .

FROM gcr.io/distroless/static:nonroot
COPY --from=build /udp-echo-agones /udp-echo-agones
USER 65532:65532
EXPOSE 7777/udp
ENTRYPOINT ["/udp-echo-agones"]
