# build stage
FROM golang:1.20-alpine as build-stage

# Install dependencies and download fb2mobi
RUN apk add --no-cache unzip curl expat && \
    curl -L https://github.com/rupor-github/fb2converter/releases/download/v1.67.1/fb2c_linux_amd64.zip -o fb2c_linux_amd64.zip && \
    mkdir /external_fb2mobi && \
    # Unzip fb2c_linux_amd64.zip to /external_fb2mobi  \
    unzip fb2c_linux_amd64.zip -d /external_fb2mobi && \
    chmod +x /external_fb2mobi/fb2c && \
    chmod +x /external_fb2mobi/kindlegen

# Copy the source code and set the working directory
COPY . /app
WORKDIR /app

# Install the dependencies
RUN go mod download

# Build the binary
RUN go build -o bin/gopds cmd/*

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
