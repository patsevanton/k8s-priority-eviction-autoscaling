FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /load-generator .

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /load-generator /load-generator

USER nonroot

ENTRYPOINT ["/load-generator"]
