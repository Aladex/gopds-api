# Frontend build stage
FROM node:20.16.0-alpine3.20 AS frontend-build
WORKDIR /app
COPY booksdump-frontend/package.json booksdump-frontend/yarn.lock ./
RUN yarn install --frozen-lockfile
COPY booksdump-frontend/ .
RUN yarn build

# Build stage
FROM golang:1.23-alpine AS build-stage
RUN apk add --no-cache unzip curl expat ca-certificates && \
    curl -L https://github.com/rupor-github/fb2converter/releases/download/v1.67.1/fb2c_linux_amd64.zip -o fb2c_linux_amd64.zip && \
    unzip fb2c_linux_amd64.zip -d /external_fb2mobi && \
    chmod +x /external_fb2mobi/fb2c /external_fb2mobi/kindlegen && \
    rm fb2c_linux_amd64.zip && \
    apk del unzip curl

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend-build /app/build /app/booksdump-frontend/build

RUN go install github.com/swaggo/swag/cmd/swag@latest && \
    swag init --generalInfo cmd/main.go && \
    CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o bin/gopds cmd/*

# Add version file
ARG VERSION=dev-version
RUN echo $VERSION > /app/version

# Production stage
FROM alpine:3.20 AS production-stage
RUN apk --no-cache add ca-certificates tzdata && \
    addgroup -g 1000 gopds && \
    adduser -D -s /bin/sh -u 1000 -G gopds gopds

WORKDIR /gopds
COPY --from=build-stage /app/bin/gopds ./gopds
COPY --from=build-stage /external_fb2mobi ./external_fb2mobi
COPY --from=build-stage /app/version ./version

RUN chown -R gopds:gopds /gopds
USER gopds

EXPOSE 8085
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8085/health || exit 1

CMD ["./gopds"]
