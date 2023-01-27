# Start by building the application.
FROM golang:latest as build

WORKDIR /go/src/scan2webdav
COPY . .

RUN go mod download
RUN CGO_ENABLED=0 go build -o /go/bin/scan2webdav

# Now copy it into our base image.
FROM gcr.io/distroless/static-debian11
COPY --from=build /go/bin/scan2webdav /
CMD ["/scan2webdav"]