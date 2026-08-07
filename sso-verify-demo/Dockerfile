FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY sso-verify-demo-linux-amd64 /app/sso-verify-demo

EXPOSE 8090
ENTRYPOINT ["/app/sso-verify-demo"]
