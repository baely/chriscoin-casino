# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/casino ./cmd/casino

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/casino /casino
ENV CASINO_ENV=production \
    CASINO_ADDR=:8080
EXPOSE 8080
USER 65532:65532
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s CMD ["/casino", "healthcheck"]
ENTRYPOINT ["/casino"]
CMD ["serve"]
