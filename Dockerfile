# Frontend build stage
FROM node:20.16.0-alpine3.20 AS frontend-build
WORKDIR /app
COPY booksdump-frontend/ /app
RUN yarn && yarn build

# Build stage
FROM golang:1.20-alpine AS build-stage
RUN apk add --no-cache unzip curl expat && \
    curl -L https://github.com/rupor-github/fb2converter/releases/download/v1.67.1/fb2c_linux_amd64.zip -o fb2c_linux_amd64.zip && \
    unzip fb2c_linux_amd64.zip -d /external_fb2mobi && \
    chmod +x /external_fb2mobi/fb2c /external_fb2mobi/kindlegen && \
    rm fb2c_linux_amd64.zip && \
    apk del unzip curl
COPY . /app
WORKDIR /app
COPY --from=frontend-build /app/build /app/booksdump-frontend/build
RUN go mod download && \
    go install github.com/swaggo/swag/cmd/swag@latest && \
    swag init --generalInfo cmd/main.go && \
    go build -o bin/gopds cmd/*

# Add this line to create the version file
ARG VERSION=dev-version
RUN echo $VERSION > /app/version

# Production stage
FROM alpine:3.14 AS production-stage
COPY --from=build-stage /app/bin/gopds /gopds/gopds
COPY --from=build-stage /external_fb2mobi /gopds/external_fb2mobi
COPY --from=build-stage /app/version /gopds/version
WORKDIR /gopds
EXPOSE 8085
CMD ["/gopds/gopds"]
