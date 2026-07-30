FROM docker.1ms.run/library/golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/prp-demo .

FROM docker.1ms.run/library/alpine:3.22
RUN addgroup -S prp && adduser -S -G prp prp
WORKDIR /app
COPY --from=build /out/prp-demo /app/prp-demo
RUN mkdir -p /data && chown -R prp:prp /data
USER prp
EXPOSE 8080
ENTRYPOINT ["/app/prp-demo"]
