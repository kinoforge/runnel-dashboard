FROM golang:1.27.4-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/runnel-dashboard ./cmd/runnel-dashboard

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/runnel-dashboard /runnel-dashboard
USER nonroot
EXPOSE 8710
ENTRYPOINT ["/runnel-dashboard"]
CMD ["serve", "-config", "/etc/runnel-dashboard/dashboard.yaml"]
