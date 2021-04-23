FROM golang:latest as builder
RUN apt-get update && apt-get install -y nocache git ca-certificates && update-ca-certificates
ENV USER=klovercloud
ENV UID=1000
RUN adduser \
    --disabled-password \
    --uid "${UID}" \
    "${USER}"
WORKDIR /app
COPY go.mod go.sum ./
#RUN go env -w GOPROXY="https://goproxy.io,direct"
RUN go mod download
COPY . .
RUN go install github.com/GeertJohan/go.rice/rice
WORKDIR /app/http
RUN rm -rf rice-box.go
RUN rice embed-go
WORKDIR /app
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /app/bin/filebrowser .



FROM debian:buster
WORKDIR /app
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group
VOLUME /db
VOLUME /srv-fb
COPY .docker.json /app/.filebrowser.json
COPY --from=builder /app/bin /app
EXPOSE 8000
CMD ["./filebrowser"]
