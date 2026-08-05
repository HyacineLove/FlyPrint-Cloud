ARG DOCKERHUB_MIRROR=docker.1ms.run
FROM ${DOCKERHUB_MIRROR}/library/golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
COPY web_style.css ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/site-portal .

FROM ${DOCKERHUB_MIRROR}/library/alpine:3.22
RUN addgroup -S portal && adduser -S -G portal portal
WORKDIR /app
COPY --from=build /out/site-portal /app/site-portal
USER portal
EXPOSE 8080
ENTRYPOINT ["/app/site-portal"]
