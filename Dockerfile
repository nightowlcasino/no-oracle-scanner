FROM golang:1.17-alpine AS builder

WORKDIR /app

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY *.go ./

RUN go build -o /no-oracle-scanner

FROM alpine:3.15.4

COPY --from=builder /no-oracle-scanner /
CMD [ "/no-oracle-scanner" ]