FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /butler ./cmd/butler

FROM scratch
COPY --from=build /butler /butler
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENTRYPOINT ["/butler"]
CMD ["-config", "/etc/butler/butler.yaml"]
