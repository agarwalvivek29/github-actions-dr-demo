FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
RUN CGO_ENABLED=0 go build -trimpath -o /out/dr-controller ./cmd/dr-controller \
 && CGO_ENABLED=0 go build -trimpath -o /out/fake-status ./cmd/fake-status

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/ /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/dr-controller"]
