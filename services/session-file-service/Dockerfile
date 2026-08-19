ARG DOCKERHUB_MIRROR=docker.io
FROM ${DOCKERHUB_MIRROR}/library/golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/session-file-service .

ARG DOCKERHUB_MIRROR=docker.io
FROM ${DOCKERHUB_MIRROR}/library/alpine:3.22
RUN addgroup -S sessionfiles && adduser -S -G sessionfiles sessionfiles
COPY --from=build /out/session-file-service /app/session-file-service
USER sessionfiles
EXPOSE 8080
ENTRYPOINT ["/app/session-file-service"]
