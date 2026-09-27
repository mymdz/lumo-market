FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/lumo ./cmd/lumo

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=build /out/lumo /app/lumo
EXPOSE 8080
ENTRYPOINT ["/app/lumo", "-config", "/app/config.yaml"]
CMD ["run"]
