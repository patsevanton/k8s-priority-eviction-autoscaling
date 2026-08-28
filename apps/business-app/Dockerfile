FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /business-app .

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /business-app /business-app

USER nonroot
EXPOSE 8080

ENTRYPOINT ["/business-app"]
