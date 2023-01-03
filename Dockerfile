# build stage
FROM golang:1.15-alpine as build-stage

# Install dependencies and download fb2mobi
RUN apk add --no-cache xz curl expat && \
    curl -L https://github.com/rupor-github/fb2mobi/releases/download/3.6.67/fb2mobi_cli_linux_x86_64_glibc_2.23.tar.xz -o fb2mobi.tar.xz && \
    mkdir /external_fb2mobi && \
    tar -xf fb2mobi.tar.xz -C /external_fb2mobi && \
    chmod +x /external_fb2mobi/fb2mobi && \
    chmod +x /external_fb2mobi/kindlegen

# Copy the source code and set the working directory
COPY . /app
WORKDIR /app

# Get the required dependencies and create the bindata
RUN go get -u github.com/go-bindata/go-bindata/... && \
    go mod download && \
    go-bindata -pkg email -o email/bindata.go -fs -prefix "email/templates" email/templates/...

# Build the binary
RUN go build -ldflags "-w -s" -o bin/gopds cmd/*

# production stage
FROM alpine:3.12 as production-stage

# Copy the built binary and fb2mobi from the build stage
COPY --from=build-stage /app/bin/gopds /gopds/gopds
COPY --from=build-stage /external_fb2mobi /gopds/external_fb2mobi

# Set the working directory and expose the necessary port
WORKDIR /gopds
EXPOSE 8085

# Run the gopds binary
CMD ["/gopds/gopds"]
