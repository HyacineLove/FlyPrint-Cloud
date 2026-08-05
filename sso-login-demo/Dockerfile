ARG DOCKERHUB_MIRROR=docker.1ms.run
FROM ${DOCKERHUB_MIRROR}/library/golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY web_style.css ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sso-login-demo .

FROM ${DOCKERHUB_MIRROR}/library/alpine:3.22
RUN addgroup -S demo && adduser -S -G demo demo
WORKDIR /app
COPY --from=build /out/sso-login-demo /app/sso-login-demo
RUN mkdir -p /data && chown -R demo:demo /data
USER demo
EXPOSE 8080
ENTRYPOINT ["/app/sso-login-demo"]
